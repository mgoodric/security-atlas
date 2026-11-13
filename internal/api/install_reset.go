// Slice 073 — admin endpoint that re-arms the bootstrap-token file.
//
// Used by `atlas-cli credentials issue --reset-bootstrap [--force]`
// (AC-8). The CLI calls this endpoint with {force}, and the platform
//   (a) clears platform_status.bootstrap_token_consumed_at,
//   (b) mints a fresh admin credential for the caller's tenant, and
//   (c) writes its bearer to ${ATLAS_DATA_DIR}/bootstrap-token.
//
// The endpoint requires super_admin, or an admin credential on a
// single-tenant install (#1851), and runs the foot-gun gate (refuse without
// --force if first_signin_at is set). It is the ONLY runtime path that
// mints an admin credential for the bootstrap-token file or writes that
// file outside of the cold-start path in cmd/atlas (#1885, ADR 0003).
//
// #1892 bounds that credential. It expires (default 24h, at most 7d), it
// is marked bootstrap-recovery in api_keys.purpose, each new reset revokes
// the recovery keys earlier resets minted, and the first sign-in by a
// different credential revokes it (install_state.go). Every mint is logged
// with the caller's identity, never the bearer, and a user caller is
// recorded in api_keys.minted_by. The key's own identity (UserID) stays
// its credential id: it never acts as the user who minted it.

package api

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/mgoodric/security-atlas/internal/api/authctx"
	"github.com/mgoodric/security-atlas/internal/api/credstore"
	"github.com/mgoodric/security-atlas/internal/auth/jwtmw"
	"github.com/mgoodric/security-atlas/internal/platform"
)

// PlatformResetter extends PlatformStatus with the ResetBootstrap entry
// point. Separated so unit tests can implement just what each handler
// needs.
type PlatformResetter interface {
	ResetBootstrap(ctx context.Context, force bool) error
	TenantCount(ctx context.Context) (int, error)
}

// AttachPlatformResetter wires the slice-073 ResetBootstrap path. cmd/atlas
// passes the same *platform.Status that backs PlatformStatus. Unit servers
// leave it nil and the route returns 503.
func (s *Server) AttachPlatformResetter(pr PlatformResetter) {
	s.platformResetter = pr
}

// resetBootstrapRequest is the JSON body for the admin endpoint. The
// bootstrap credential is minted server-side, so the caller supplies no
// token (#1885); a `token` field sent by an older CLI is ignored. TenantID,
// when set, must be the caller's tenant: the credential is always minted
// for the caller's tenant, and a mismatch is refused rather than ignored.
//
// TTLSeconds is the recovery key's lifetime (#1892). 0 or absent means
// defaultRecoveryTTL; anything outside (0, maxRecoveryTTL] is refused.
// There is no non-expiring recovery key.
type resetBootstrapRequest struct {
	Force      bool   `json:"force"`
	TenantID   string `json:"tenant_id,omitempty"`
	TTLSeconds int64  `json:"ttl_seconds,omitempty"`
}

// defaultRecoveryTTL is longer than the 8h session cookie the bearer-paste
// sign-in sets (web/app/login/actions.ts), so a session opened with the
// recovery key does not outlive it by much, nor die before the cookie.
// maxRecoveryTTL caps an operator override. ADR 0003 records both.
const (
	defaultRecoveryTTL = 24 * time.Hour
	maxRecoveryTTL     = 7 * 24 * time.Hour
)

// resetBootstrapResponse is the JSON response shape. BearerToken is the
// re-issued bootstrap admin credential's plaintext, returned once.
// RevokedCredentialIDs lists the recovery keys earlier resets minted that
// this reset revoked.
type resetBootstrapResponse struct {
	FileWritten          bool     `json:"file_written"`
	StatusReset          bool     `json:"status_reset"`
	CredentialID         string   `json:"credential_id"`
	TenantID             string   `json:"tenant_id"`
	BearerToken          string   `json:"bearer_token"`
	ExpiresAt            string   `json:"expires_at"`
	RevokedCredentialIDs []string `json:"revoked_credential_ids"`
}

// handleResetBootstrap is the admin-only recovery endpoint.
//
// Pre-conditions enforced (in order):
//
//	(1) super_admin, or an admin credential on a single-tenant install
//	    (authorizeResetBootstrap).
//	(2) Either first_signin_at IS NULL, or force == true.
//
// Either failure returns a non-2xx without writing anything. On success
// the platform_status row is reset, a fresh admin credential is minted for
// the caller's tenant, and its bearer is written to the bootstrap-token
// file at the configured path and returned once in the response.
func (s *Server) handleResetBootstrap(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")

	if s.platformResetter == nil {
		w.WriteHeader(http.StatusServiceUnavailable)
		_, _ = w.Write([]byte(`{"error":"platform resetter not configured"}`))
		return
	}

	if !s.authorizeResetBootstrap(w, r) {
		return
	}

	var req resetBootstrapRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		w.WriteHeader(http.StatusBadRequest)
		_, _ = w.Write([]byte(`{"error":"invalid JSON body"}`))
		return
	}
	// The re-issued bootstrap credential is an admin credential, so that
	// signing in with it after recovery gives an admin session (#1885). It
	// is minted here, behind authorizeResetBootstrap, never taken from the
	// caller: the admin gRPC Issue RPC mints only non-admin keys, and a
	// caller-supplied token cannot be trusted to be an admin one. Fail
	// closed without a credential store.
	if s.credStore == nil {
		w.WriteHeader(http.StatusServiceUnavailable)
		_, _ = w.Write([]byte(`{"error":"credential store not configured"}`))
		return
	}
	cred, _ := authctx.CredentialFromContext(r.Context())
	// A super_admin JWT with no current tenant carries the all-zeros uuid,
	// which is not a tenant (#1892): refuse it like a missing one, before
	// anything is reset.
	if cred.TenantID == "" || cred.TenantID == uuid.Nil.String() {
		w.WriteHeader(http.StatusBadRequest)
		_, _ = w.Write([]byte(`{"error":"caller credential carries no tenant"}`))
		return
	}
	if req.TenantID != "" && req.TenantID != cred.TenantID {
		w.WriteHeader(http.StatusBadRequest)
		_, _ = w.Write([]byte(`{"error":"tenant_id must be the caller's tenant"}`))
		return
	}
	ttl := defaultRecoveryTTL
	if req.TTLSeconds != 0 {
		ttl = time.Duration(req.TTLSeconds) * time.Second
		if req.TTLSeconds < 0 || ttl > maxRecoveryTTL {
			w.WriteHeader(http.StatusBadRequest)
			_, _ = w.Write([]byte(`{"error":"ttl_seconds must be between 1 and 604800 (7d)"}`))
			return
		}
	}

	if err := s.platformResetter.ResetBootstrap(r.Context(), req.Force); err != nil {
		if errors.Is(err, platform.ErrResetForbidden) {
			w.WriteHeader(http.StatusConflict)
			_, _ = w.Write([]byte(`{"error":"refusing to reset; pass --force after first sign-in"}`))
			return
		}
		if errors.Is(err, platform.ErrWriteNotConfigured) {
			w.WriteHeader(http.StatusServiceUnavailable)
			_, _ = w.Write([]byte(`{"error":"write pool not configured"}`))
			return
		}
		s.installLogger().Warn("reset-bootstrap status write failed",
			slog.String("error", err.Error()))
		w.WriteHeader(http.StatusInternalServerError)
		_, _ = w.Write([]byte(`{"error":"reset bootstrap failed"}`))
		return
	}

	// Minted after the status reset, so a refused reset (409) leaves no
	// live admin credential behind.
	issued, bearer, err := s.credStore.IssueRecoveryAdmin(cred.TenantID, ttl, minterUserID(cred))
	if err != nil {
		s.installLogger().Warn("reset-bootstrap credential issue failed",
			slog.String("error", err.Error()))
		w.WriteHeader(http.StatusInternalServerError)
		_, _ = w.Write([]byte(`{"error":"status reset succeeded but credential issue failed"}`))
		return
	}

	resp := resetBootstrapResponse{
		StatusReset:          true,
		CredentialID:         issued.ID,
		TenantID:             issued.TenantID,
		BearerToken:          bearer,
		ExpiresAt:            issued.ExpiresAt().Format(time.RFC3339),
		RevokedCredentialIDs: []string{},
	}
	if s.bootstrapTokenPath != "" {
		if err := platform.WriteBootstrapToken(s.bootstrapTokenPath, bearer); err != nil {
			// The marker is already reset; the file write failure is
			// surfaced but does not undo the status reset (operators can
			// inspect stderr / re-run the CLI to retry). The bearer is not
			// returned on this path, so revoke the credential rather than
			// leave an admin key that no one holds.
			if rerr := s.credStore.Revoke(issued.ID); rerr != nil {
				s.installLogger().Warn("reset-bootstrap revoke after failed file write failed",
					slog.String("error", rerr.Error()))
			}
			s.installLogger().Warn("reset-bootstrap file write failed",
				slog.String("error", err.Error()))
			w.WriteHeader(http.StatusInternalServerError)
			_, _ = w.Write([]byte(`{"error":"status reset succeeded but file write failed"}`))
			return
		}
		resp.FileWritten = true
	}

	// The file now holds the new key, so the keys earlier resets minted
	// are no longer in it: revoke them, matched by marker and id. A
	// failure leaves those keys to expire on their TTL; it does not undo
	// this reset.
	revoked, rerr := s.credStore.RevokePurpose(credstore.PurposeBootstrapRecovery, issued.ID)
	if rerr != nil {
		s.installLogger().Warn("reset-bootstrap revoke of earlier recovery keys failed",
			slog.String("error", rerr.Error()))
	}
	if revoked != nil {
		resp.RevokedCredentialIDs = revoked
	}

	// The record of the mint. Never the bearer, not even its last four.
	s.installLogger().Info("reset-bootstrap minted recovery admin credential",
		slog.String("credential_id", issued.ID),
		slog.String("tenant_id", issued.TenantID),
		slog.String("expires_at", resp.ExpiresAt),
		slog.String("caller_credential_id", cred.ID),
		slog.String("caller_subject", callerSubject(r.Context(), cred)),
		slog.String("minted_by", issued.MintedBy),
		slog.Bool("force", req.Force),
		slog.Any("revoked_credential_ids", resp.RevokedCredentialIDs))
	_ = json.NewEncoder(w).Encode(resp)
}

// callerSubject names who called: the verified JWT's subject when there is
// one, otherwise the credential's UserID (an API key's own id, or the user
// it was issued by).
func callerSubject(ctx context.Context, cred credstore.Credential) string {
	if claims := jwtmw.FromContext(ctx); claims != nil && claims.Subject != "" {
		return claims.Subject
	}
	return cred.UserID
}

// minterUserID returns the users(id) uuid behind the caller, for
// api_keys.minted_by (attribution only, never the new key's identity), or
// "" when the caller is not a user. A JWT carries "user:<uuid>"; a key
// issued by a user carries the bare uuid; a bootstrap or machine
// credential carries neither and records no minter.
func minterUserID(cred credstore.Credential) string {
	id := strings.TrimPrefix(cred.UserID, "user:")
	if _, err := uuid.Parse(id); err != nil {
		return ""
	}
	return id
}

// authorizeResetBootstrap admits a caller whose verified JWT carries
// `atlas:super_admin`, or an admin credential (including a per-tenant admin
// API key) while the install has exactly one tenant (#1851, ADR 0003
// amendment). The reset clears install-wide markers and rewrites the
// install's bootstrap-token file, so on a multi-tenant install a tenant
// admin reaching it is a cross-tenant effect. On a single-tenant install
// the only tenant admin is the install's operator, and the fallback keeps
// the CLI recovery path working when no browser sign-in (and so no JWT) is
// possible. Missing credential is 401; anything else refused is 403.
func (s *Server) authorizeResetBootstrap(w http.ResponseWriter, r *http.Request) bool {
	cred, ok := authctx.CredentialFromContext(r.Context())
	if !ok {
		w.WriteHeader(http.StatusUnauthorized)
		_, _ = w.Write([]byte(`{"error":"missing credential"}`))
		return false
	}
	if claims := jwtmw.FromContext(r.Context()); claims != nil && claims.SuperAdmin {
		return true
	}
	if !cred.IsAdmin {
		w.WriteHeader(http.StatusForbidden)
		_, _ = w.Write([]byte(`{"error":"super_admin required"}`))
		return false
	}
	n, err := s.platformResetter.TenantCount(r.Context())
	if err != nil {
		s.installLogger().Warn("reset-bootstrap tenant count failed",
			slog.String("error", err.Error()))
		w.WriteHeader(http.StatusInternalServerError)
		_, _ = w.Write([]byte(`{"error":"reset bootstrap failed"}`))
		return false
	}
	if n != 1 {
		w.WriteHeader(http.StatusForbidden)
		_, _ = w.Write([]byte(`{"error":"super_admin required on a multi-tenant install"}`))
		return false
	}
	return true
}
