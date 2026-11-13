// Slice 073 — admin endpoint that re-arms the bootstrap-token file.
//
// Used by `atlas-cli credentials issue --reset-bootstrap [--force]`
// (AC-8). The CLI:
//   1. Calls the existing AdminCredentialsService.Issue RPC to mint a
//      fresh admin bearer.
//   2. Calls this endpoint with {token, force} to ask the platform to
//      (a) clear platform_status.bootstrap_token_consumed_at
//      (b) write the new token to ${ATLAS_DATA_DIR}/bootstrap-token.
//
// The endpoint requires super_admin, or an admin credential on a
// single-tenant install (#1851), accepts only a live credential this server
// issued as the token, runs the foot-gun gate (refuse without --force if first_signin_at is
// set), and is the ONLY runtime path that writes the bootstrap-token
// file outside of the cold-start path in cmd/atlas.

package api

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"

	"github.com/mgoodric/security-atlas/internal/api/authctx"
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

// resetBootstrapRequest is the JSON body for the admin endpoint.
type resetBootstrapRequest struct {
	Token string `json:"token"`
	Force bool   `json:"force"`
}

// resetBootstrapResponse is the JSON response shape.
type resetBootstrapResponse struct {
	FileWritten bool `json:"file_written"`
	StatusReset bool `json:"status_reset"`
}

// handleResetBootstrap is the admin-only recovery endpoint.
//
// Pre-conditions enforced (in order):
//
//	(1) super_admin, or an admin credential on a single-tenant install
//	    (authorizeResetBootstrap).
//	(2) token authenticates as a live credential in the credential store.
//	(3) Either first_signin_at IS NULL, or force == true.
//
// Either failure returns a non-2xx without writing anything. On success
// the platform_status row is reset AND the new token is written to the
// bootstrap-token file at the configured path.
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
	if req.Token == "" {
		w.WriteHeader(http.StatusBadRequest)
		_, _ = w.Write([]byte(`{"error":"token is required"}`))
		return
	}
	// The token lands in the bootstrap-token file that the login page
	// points operators at, so it must be a live credential this server
	// issued, not a caller-chosen string (#1851). Fail closed without a
	// credential store.
	if s.credStore == nil {
		w.WriteHeader(http.StatusServiceUnavailable)
		_, _ = w.Write([]byte(`{"error":"credential store not configured"}`))
		return
	}
	if _, err := s.credStore.Authenticate(req.Token); err != nil {
		w.WriteHeader(http.StatusBadRequest)
		_, _ = w.Write([]byte(`{"error":"token is not a live atlas credential"}`))
		return
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

	resp := resetBootstrapResponse{StatusReset: true}
	if s.bootstrapTokenPath != "" {
		if err := platform.WriteBootstrapToken(s.bootstrapTokenPath, req.Token); err != nil {
			// The marker is already reset; the file write failure is
			// surfaced but does not undo the status reset (operators can
			// inspect stderr / re-run the CLI to retry the file write).
			s.installLogger().Warn("reset-bootstrap file write failed",
				slog.String("error", err.Error()))
			w.WriteHeader(http.StatusInternalServerError)
			_, _ = w.Write([]byte(`{"error":"status reset succeeded but file write failed"}`))
			return
		}
		resp.FileWritten = true
	}
	_ = json.NewEncoder(w).Encode(resp)
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
