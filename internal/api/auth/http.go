// Package auth wires the user-facing auth routes:
//
//	GET   /auth/oidc/login    — initiate OIDC code+PKCE flow
//	GET   /auth/oidc/callback — exchange code, upsert user, set session cookie
//	POST  /auth/local/login   — local user/password login (solo deployments)
//	POST  /auth/logout        — revoke session + clear cookie
//
// The handlers compose the slice-034 packages: oidc.Authenticator drives
// the IdP flow, users.Store provisions/verifies, sessions.Store persists
// the opaque cookie-id session row.
package auth

import (
	"encoding/json"
	"errors"
	"net/http"
	"time"

	"github.com/google/uuid"

	"github.com/mgoodric/security-atlas/internal/auth/oidc"
	"github.com/mgoodric/security-atlas/internal/auth/sessions"
	"github.com/mgoodric/security-atlas/internal/auth/users"
	"github.com/mgoodric/security-atlas/internal/tenancy"
)

// Handler bundles the auth routes' dependencies.
type Handler struct {
	oidc          *oidc.Authenticator
	users         *users.Store
	sessions      *sessions.Store
	secureCookies bool
}

// New constructs a Handler. secureCookies=false is for local-dev HTTP
// fixtures only; production MUST set it true.
func New(o *oidc.Authenticator, u *users.Store, s *sessions.Store, secureCookies bool) *Handler {
	return &Handler{oidc: o, users: u, sessions: s, secureCookies: secureCookies}
}

// LocalLoginRequest is the JSON body for POST /auth/local/login.
type LocalLoginRequest struct {
	TenantID string `json:"tenant_id"`
	Email    string `json:"email"`
	Password string `json:"password"`
}

// LocalLogin verifies (tenant, email, password) and on success establishes
// a session cookie. Returns 401 on any failure (no oracle).
func (h *Handler) LocalLogin(w http.ResponseWriter, r *http.Request) {
	var req LocalLoginRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeAuthError(w, http.StatusBadRequest, "invalid JSON body")
		return
	}
	tenantID, err := uuid.Parse(req.TenantID)
	if err != nil {
		writeAuthError(w, http.StatusBadRequest, "invalid tenant_id")
		return
	}
	ctx, err := tenancy.WithTenant(r.Context(), tenantID.String())
	if err != nil {
		writeAuthError(w, http.StatusBadRequest, "invalid tenant_id")
		return
	}
	usr, err := h.users.VerifyLocalLogin(ctx, tenantID, req.Email, req.Password)
	if err != nil {
		if errors.Is(err, users.ErrInvalidCredentials) {
			writeAuthError(w, http.StatusUnauthorized, "invalid credentials")
			return
		}
		writeAuthError(w, http.StatusInternalServerError, "login failed")
		return
	}
	sess, err := h.sessions.Create(ctx, sessions.CreateInput{
		TenantID:  usr.TenantID,
		UserID:    usr.ID,
		UserAgent: userAgent(r),
		IPAddress: clientIP(r),
	})
	if err != nil {
		writeAuthError(w, http.StatusInternalServerError, "session create failed")
		return
	}
	sessions.SetCookie(w, sess.ID, sess.ExpiresAt, h.secureCookies)
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]any{
		"user_id":   usr.ID,
		"tenant_id": usr.TenantID,
		"display":   usr.DisplayName,
	})
}

// OIDCLogin handles GET /auth/oidc/login?tenant_id=...&idp=...
// It generates state + PKCE, looks up the IdP, and 302-redirects to the
// IdP's authorize endpoint.
func (h *Handler) OIDCLogin(w http.ResponseWriter, r *http.Request) {
	tenantIDStr := r.URL.Query().Get("tenant_id")
	idpName := r.URL.Query().Get("idp")
	if tenantIDStr == "" || idpName == "" {
		writeAuthError(w, http.StatusBadRequest, "tenant_id and idp query parameters are required")
		return
	}
	tenantID, err := uuid.Parse(tenantIDStr)
	if err != nil {
		writeAuthError(w, http.StatusBadRequest, "invalid tenant_id")
		return
	}
	ctx, err := tenancy.WithTenant(r.Context(), tenantID.String())
	if err != nil {
		writeAuthError(w, http.StatusBadRequest, "invalid tenant_id")
		return
	}
	result, err := h.oidc.BeginLogin(ctx, oidc.LoginInput{
		TenantID: tenantID,
		IdpName:  idpName,
	}, h.secureCookies)
	if err != nil {
		writeAuthError(w, http.StatusBadRequest, "OIDC begin: "+err.Error())
		return
	}
	for _, c := range result.Cookies {
		http.SetCookie(w, c)
	}
	http.Redirect(w, r, result.AuthURL, http.StatusFound)
}

// OIDCCallback handles GET /auth/oidc/callback?code=...&state=...
// It verifies state (CSRF), exchanges code, upserts user, and sets the
// session cookie. The tenant_id is bound to the flow via the IdP cookie's
// preceding /auth/oidc/login resolution.
//
// Slice 198 — OIDC-first-install bootstrap branch:
//
// AFTER the OIDC callback succeeds but BEFORE the standard UpsertOIDC
// call, the handler invokes BootstrapFirstInstallOrUpsert. When the
// tenants table is empty, the bootstrap branch atomically creates the
// Default Tenant + the OIDC user + the admin role + the super_admin
// grant + the audit-log row, and the handler establishes the session
// against the newly-synthesized tenant_id (the URL query parameter is
// ignored on this branch — the operator's browser hit /auth/oidc/login
// with no tenants yet, so any tenant_id they supplied is a placeholder).
//
// On the established-install branch (count(*) > 0), the bootstrap call
// returns Bootstrapped=false and the handler falls through to the
// existing UpsertOIDC + session-create path unchanged.
func (h *Handler) OIDCCallback(w http.ResponseWriter, r *http.Request) {
	// Tenant is recovered from the flow cookies by the resolver.
	tenantIDStr := r.URL.Query().Get("tenant_id")
	tenantID, err := uuid.Parse(tenantIDStr)
	if err != nil {
		writeAuthError(w, http.StatusBadRequest, "invalid or missing tenant_id")
		return
	}
	ctx, err := tenancy.WithTenant(r.Context(), tenantID.String())
	if err != nil {
		writeAuthError(w, http.StatusBadRequest, "invalid tenant_id")
		return
	}
	result, err := h.oidc.HandleCallback(ctx, r, tenantID)
	if err != nil {
		if errors.Is(err, oidc.ErrStateMismatch) {
			writeAuthError(w, http.StatusBadRequest, "state mismatch (CSRF guard tripped)")
			return
		}
		writeAuthError(w, http.StatusBadGateway, "OIDC callback: "+err.Error())
		return
	}

	// Slice 198 bootstrap branch. The call is a no-op (returns
	// Bootstrapped=false) when the Store has no auth pool wired OR
	// when count(*) FROM tenants > 0 — i.e., on every login after the
	// first install. The branch returns ErrBootstrapUnavailable when
	// the auth pool isn't wired; on that path we fall through to the
	// existing UpsertOIDC code (which itself enforces the
	// pre-slice-198 "tenant_id required" guarantee).
	boot, bootErr := h.users.BootstrapFirstInstallOrUpsert(r.Context(), users.BootstrapInput{
		Email:       result.Email,
		DisplayName: result.DisplayName,
		Issuer:      result.Issuer,
		Subject:     result.Subject,
	})
	if bootErr != nil && !errors.Is(bootErr, users.ErrBootstrapUnavailable) {
		writeAuthError(w, http.StatusInternalServerError, "bootstrap: "+bootErr.Error())
		return
	}

	var usr users.User
	if boot.Bootstrapped {
		// Bootstrap branch already created the user row under the
		// synthesized tenant. Skip UpsertOIDC and proceed straight to
		// session establishment under the new tenant.
		bootCtx, ctxErr := tenancy.WithTenant(r.Context(), boot.TenantID.String())
		if ctxErr != nil {
			writeAuthError(w, http.StatusInternalServerError, "bootstrap tenant context: "+ctxErr.Error())
			return
		}
		usr, err = h.users.GetByID(bootCtx, boot.TenantID, boot.UserID)
		if err != nil {
			writeAuthError(w, http.StatusInternalServerError, "bootstrap reload user: "+err.Error())
			return
		}
		// Replace ctx so the session creation below writes under the
		// new tenant.
		ctx = bootCtx
	} else {
		usr, err = h.users.UpsertOIDC(ctx, users.UpsertOIDCInput{
			TenantID:    result.TenantID,
			Email:       result.Email,
			DisplayName: result.DisplayName,
			Issuer:      result.Issuer,
			Subject:     result.Subject,
		})
		if err != nil {
			writeAuthError(w, http.StatusInternalServerError, "user upsert: "+err.Error())
			return
		}
	}
	sess, err := h.sessions.Create(ctx, sessions.CreateInput{
		TenantID:   usr.TenantID,
		UserID:     usr.ID,
		IdpIssuer:  result.Issuer,
		IdpSubject: result.Subject,
		UserAgent:  userAgent(r),
		IPAddress:  clientIP(r),
	})
	if err != nil {
		writeAuthError(w, http.StatusInternalServerError, "session create: "+err.Error())
		return
	}
	sessions.SetCookie(w, sess.ID, sess.ExpiresAt, h.secureCookies)
	oidc.ClearFlowCookies(w, h.secureCookies)
	// Redirect to platform root after successful sign-in.
	http.Redirect(w, r, "/", http.StatusFound)
}

// Logout handles POST /auth/logout. It revokes the session row and clears
// the cookie. The session id is read from the cookie; if absent the
// handler still clears the cookie and returns 204 (idempotent).
func (h *Handler) Logout(w http.ResponseWriter, r *http.Request) {
	c, err := r.Cookie(sessions.CookieName)
	if err == nil && c.Value != "" {
		// Best-effort revoke. We need the tenant context to write to the
		// session row; the cookie alone does not carry it. The Read flow
		// would normally resolve it, but for logout we accept that an
		// unresolvable session is moot — clear the cookie regardless.
		// In v1 we don't enforce server-side revoke on logout when the
		// tenant is unknown; the session falls out via expiry.
		tenantIDStr := r.Header.Get("X-Tenant-Id")
		if tid, err := uuid.Parse(tenantIDStr); err == nil {
			_ = h.sessions.Revoke(r.Context(), tid, c.Value)
		}
	}
	sessions.ClearCookie(w, h.secureCookies)
	w.WriteHeader(http.StatusNoContent)
}

func writeAuthError(w http.ResponseWriter, code int, msg string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(map[string]string{"error": msg})
}

// ResolveSession is a helper for middleware: given a request, parse the
// atlas_session cookie, read the session from the DB, and return the
// session record. Returns an error when the cookie is missing or the
// session is invalid. The middleware uses the session.TenantID for
// downstream RLS plumbing.
func (h *Handler) ResolveSession(r *http.Request, tenantID uuid.UUID) (sessions.Session, error) {
	c, err := r.Cookie(sessions.CookieName)
	if err != nil {
		return sessions.Session{}, err
	}
	return h.sessions.Read(r.Context(), tenantID, c.Value)
}

// IdleTimeout is the longest the platform tolerates between session
// reads before retiring the session via expiry. Hint for documentation;
// the actual TTL is configured in sessions.NewStore.
const IdleTimeout = 7 * 24 * time.Hour
