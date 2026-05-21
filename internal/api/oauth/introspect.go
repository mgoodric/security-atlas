// introspect.go implements the slice-190 `POST /oauth/introspect`
// endpoint per RFC 7662.
//
// Authentication shape: ONLY client_credentials (Basic header or
// form params). Introspection is a resource-server capability — the
// caller proves they are a trusted machine identity. Unlike
// `/oauth/revoke`, there is NO self-introspection path (the user
// already has the token's content from issuance — they don't need
// to ask for it back).
//
// Response semantics (P0-190-5 — RFC 7662 §2.2):
//
//   - 200 with `{"active": true, ...claims}` for valid tokens.
//   - 200 with `{"active": false}` for revoked / expired / invalid /
//     unknown tokens. NEVER 401 for the token state — 401 is
//     reserved for the INSPECTOR's auth failure, not the target
//     token's invalidity.
//   - 400 on missing `token`.
//   - 401 on a failed inspector authentication.
//
// Anti-criteria honored:
//
//   - P0-190-5: revoked/expired tokens get 200 + {active:false}, not
//     401.
//   - P0-190-6: signature verification goes through
//     tokensign.Signer.Verify — no direct keystore access.
package oauth

import (
	"encoding/json"
	"net/http"
	"time"

	"github.com/mgoodric/security-atlas/internal/auth/jwt"
	"github.com/mgoodric/security-atlas/internal/auth/oauthclient"
	"github.com/mgoodric/security-atlas/internal/auth/revocation"
	"github.com/mgoodric/security-atlas/internal/auth/tokensign"
)

// IntrospectionEndpoint owns the dependencies for
// `POST /oauth/introspect`.
type IntrospectionEndpoint struct {
	signer  *tokensign.Signer
	revoked *revocation.Store
	clients *oauthclient.Store
	issuer  string
	now     func() time.Time
}

// IntrospectionEndpointConfig is the constructor parameter bag.
type IntrospectionEndpointConfig struct {
	Issuer string

	// Now is the clock; nil falls back to time.Now. Tests inject a
	// pinned clock so the introspection result is deterministic.
	Now func() time.Time
}

// NewIntrospectionEndpoint builds an IntrospectionEndpoint. signer +
// revoked + clients must be non-nil; the constructor panics on
// missing required deps because this is wired once at startup.
func NewIntrospectionEndpoint(signer *tokensign.Signer, revoked *revocation.Store, clients *oauthclient.Store, cfg IntrospectionEndpointConfig) *IntrospectionEndpoint {
	if signer == nil {
		panic("oauth: NewIntrospectionEndpoint: signer is nil")
	}
	if revoked == nil {
		panic("oauth: NewIntrospectionEndpoint: revoked store is nil")
	}
	if clients == nil {
		panic("oauth: NewIntrospectionEndpoint: clients store is nil")
	}
	if cfg.Issuer == "" {
		panic("oauth: NewIntrospectionEndpoint: Issuer is empty")
	}
	now := cfg.Now
	if now == nil {
		now = time.Now
	}
	return &IntrospectionEndpoint{
		signer:  signer,
		revoked: revoked,
		clients: clients,
		issuer:  cfg.Issuer,
		now:     now,
	}
}

// ServeHTTP implements `POST /oauth/introspect` per RFC 7662.
func (e *IntrospectionEndpoint) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	ct := r.Header.Get("Content-Type")
	if !isFormContentType(ct) {
		writeOAuthError(w, http.StatusBadRequest, "invalid_request",
			"Content-Type must be application/x-www-form-urlencoded")
		return
	}
	if err := r.ParseForm(); err != nil {
		writeOAuthError(w, http.StatusBadRequest, "invalid_request", "malformed form body")
		return
	}

	tokenIn := r.FormValue("token")
	if tokenIn == "" {
		writeOAuthError(w, http.StatusBadRequest, "invalid_request", "token is required")
		return
	}

	// AUTHENTICATE the inspector. Same client_credentials shape as
	// the revoke endpoint's path (a). NO self-inspection path —
	// introspection is a resource-server capability.
	if !e.authenticateInspector(r) {
		writeOAuthError(w, http.StatusUnauthorized, "invalid_client",
			"client authentication failed")
		return
	}

	// Verify signature. Failure → {active: false}.
	claims, verr := e.signer.Verify(r.Context(), tokenIn)
	if verr != nil {
		writeInactive(w)
		return
	}

	// Validate temporal/identity claims. Expired/wrong-iss/wrong-aud
	// → {active: false}.
	if err := jwt.Validate(claims, jwt.ValidationParams{
		ExpectedIssuer:   e.issuer,
		ExpectedAudience: e.issuer,
		Now:              e.now(),
	}); err != nil {
		writeInactive(w)
		return
	}

	// Check revocation. Revoked → {active: false} (P0-190-5).
	if claims.ID != "" {
		isRevoked, rerr := e.revoked.IsRevoked(r.Context(), claims.ID)
		if rerr != nil {
			// DB failure on revocation check fails CLOSED — report
			// the token as inactive rather than risking a false
			// "active" reply when we don't know revocation state.
			writeInactive(w)
			return
		}
		if isRevoked {
			writeInactive(w)
			return
		}
	}

	writeActive(w, claims)
}

// authenticateInspector verifies the caller has a valid
// client_credentials. Returns true on success.
func (e *IntrospectionEndpoint) authenticateInspector(r *http.Request) bool {
	if clientID, clientSecret, ok := r.BasicAuth(); ok && clientID != "" && clientSecret != "" {
		_, err := e.clients.Verify(r.Context(), clientID, clientSecret)
		return err == nil
	}
	clientID := r.FormValue("client_id")
	clientSecret := r.FormValue("client_secret")
	if clientID == "" || clientSecret == "" {
		return false
	}
	_, err := e.clients.Verify(r.Context(), clientID, clientSecret)
	return err == nil
}

// writeInactive writes the RFC 7662 §2.2 inactive-token response.
// Body shape is locked: a single `active: false` field.
func writeInactive(w http.ResponseWriter) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write([]byte(`{"active":false}`))
}

// writeActive writes the RFC 7662 §2.2 active-token response with
// the standard fields + the atlas:* custom claims.
func writeActive(w http.ResponseWriter, c jwt.AtlasClaims) {
	body := map[string]any{
		"active":                  true,
		"token_type":              "Bearer",
		"sub":                     c.Subject,
		"aud":                     c.Audience,
		"iss":                     c.Issuer,
		"exp":                     c.ExpiresAt,
		"iat":                     c.IssuedAt,
		"jti":                     c.ID,
		"atlas:idp_issuer":        c.IDPIssuer,
		"atlas:current_tenant_id": c.CurrentTenantID,
		"atlas:available_tenants": c.AvailableTenants,
		"atlas:roles":             c.Roles,
		"atlas:super_admin":       c.SuperAdmin,
	}
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(http.StatusOK)
	_ = json.NewEncoder(w).Encode(body)
}
