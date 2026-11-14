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
	"time"

	"github.com/google/uuid"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/timestamppb"

	adminv1 "github.com/mgoodric/security-atlas/gen/proto/admin/v1"
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
//
// /v1 accepts only JWTs, so in practice the caller here holds a
// super_admin JWT. An admin API key, including ATLAS_BOOTSTRAP_TOKEN,
// reaches the same reset over gRPC (AdminCredentialsService.ResetBootstrap,
// #2121); both transports share resetBootstrap below.
func (s *Server) handleResetBootstrap(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")

	if s.platformResetter == nil {
		writeResetFailure(w, &resetFailure{resetUnavailable, "platform resetter not configured"})
		return
	}
	cred, ok := authctx.CredentialFromContext(r.Context())
	if !ok {
		w.WriteHeader(http.StatusUnauthorized)
		_, _ = w.Write([]byte(`{"error":"missing credential"}`))
		return
	}
	claims := jwtmw.FromContext(r.Context())
	superAdmin := claims != nil && claims.SuperAdmin
	if f := s.authorizeResetBootstrap(r.Context(), cred, superAdmin); f != nil {
		writeResetFailure(w, f)
		return
	}

	var req resetBootstrapRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		w.WriteHeader(http.StatusBadRequest)
		_, _ = w.Write([]byte(`{"error":"invalid JSON body"}`))
		return
	}
	resp, f := s.resetBootstrap(r.Context(), cred, callerSubject(r.Context(), cred), "http", req)
	if f != nil {
		writeResetFailure(w, f)
		return
	}
	_ = json.NewEncoder(w).Encode(resp)
}

// resetFailureKind classifies a refused or failed reset independently of
// the transport that carried it; HTTP and gRPC each map it to their own
// status.
type resetFailureKind int

const (
	resetUnavailable resetFailureKind = iota
	resetBadRequest
	resetForbidden
	resetConflict
	resetInternal
)

// resetFailure is a reset that did not complete. msg is safe to return to
// the caller: it never carries an underlying error's text.
type resetFailure struct {
	kind resetFailureKind
	msg  string
}

func writeResetFailure(w http.ResponseWriter, f *resetFailure) {
	code := http.StatusInternalServerError
	switch f.kind {
	case resetUnavailable:
		code = http.StatusServiceUnavailable
	case resetBadRequest:
		code = http.StatusBadRequest
	case resetForbidden:
		code = http.StatusForbidden
	case resetConflict:
		code = http.StatusConflict
	}
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(map[string]string{"error": f.msg})
}

// resetBootstrap runs an authorized reset: it validates the request, resets
// platform_status, mints the recovery admin key, writes it to the
// bootstrap-token file, revokes the recovery keys earlier resets minted,
// and logs the mint. The caller has already passed authorizeResetBootstrap.
// transport ("http" or "grpc") is recorded in the log.
func (s *Server) resetBootstrap(ctx context.Context, cred credstore.Credential, subject, transport string, req resetBootstrapRequest) (resetBootstrapResponse, *resetFailure) {
	// The re-issued bootstrap credential is an admin credential, so that
	// signing in with it after recovery gives an admin session (#1885). It
	// is minted here, behind authorizeResetBootstrap, never taken from the
	// caller: the admin gRPC Issue RPC mints only non-admin keys, and a
	// caller-supplied token cannot be trusted to be an admin one. Fail
	// closed without a credential store.
	if s.credStore == nil {
		return resetBootstrapResponse{}, &resetFailure{resetUnavailable, "credential store not configured"}
	}
	// A super_admin JWT with no current tenant carries the all-zeros uuid,
	// which is not a tenant (#1892): refuse it like a missing one, before
	// anything is reset.
	if cred.TenantID == "" || cred.TenantID == uuid.Nil.String() {
		return resetBootstrapResponse{}, &resetFailure{resetBadRequest, "caller credential carries no tenant"}
	}
	if req.TenantID != "" && req.TenantID != cred.TenantID {
		return resetBootstrapResponse{}, &resetFailure{resetBadRequest, "tenant_id must be the caller's tenant"}
	}
	ttl := defaultRecoveryTTL
	if req.TTLSeconds != 0 {
		// Range-check the seconds before converting: a huge value would
		// wrap time.Duration negative, pass a check on the product, and
		// fail only at the mint, after the status was already reset.
		if req.TTLSeconds < 0 || req.TTLSeconds > int64(maxRecoveryTTL/time.Second) {
			return resetBootstrapResponse{}, &resetFailure{resetBadRequest, "ttl_seconds must be between 1 and 604800 (7d)"}
		}
		ttl = time.Duration(req.TTLSeconds) * time.Second
	}

	if err := s.platformResetter.ResetBootstrap(ctx, req.Force); err != nil {
		if errors.Is(err, platform.ErrResetForbidden) {
			return resetBootstrapResponse{}, &resetFailure{resetConflict, "refusing to reset; pass --force after first sign-in"}
		}
		if errors.Is(err, platform.ErrWriteNotConfigured) {
			return resetBootstrapResponse{}, &resetFailure{resetUnavailable, "write pool not configured"}
		}
		s.installLogger().Warn("reset-bootstrap status write failed",
			slog.String("error", err.Error()))
		return resetBootstrapResponse{}, &resetFailure{resetInternal, "reset bootstrap failed"}
	}

	// Minted after the status reset, so a refused reset (409) leaves no
	// live admin credential behind.
	issued, bearer, err := s.credStore.IssueRecoveryAdmin(cred.TenantID, ttl, credstore.MinterUserID(cred))
	if err != nil {
		s.installLogger().Warn("reset-bootstrap credential issue failed",
			slog.String("error", err.Error()))
		return resetBootstrapResponse{}, &resetFailure{resetInternal, "status reset succeeded but credential issue failed"}
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
			return resetBootstrapResponse{}, &resetFailure{resetInternal, "status reset succeeded but file write failed"}
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
		slog.String("caller_subject", subject),
		slog.String("transport", transport),
		slog.String("minted_by", issued.MintedBy),
		slog.Bool("force", req.Force),
		slog.Any("revoked_credential_ids", resp.RevokedCredentialIDs))
	return resp, nil
}

// grpcBootstrapResetter serves AdminCredentialsService.ResetBootstrap with
// the HTTP endpoint's reset (#2121). gRPC authenticates API keys, not JWTs,
// so the caller is never super_admin here: it is admitted only as an admin
// key on a single-tenant install, which is the locked-out operator holding
// ATLAS_BOOTSTRAP_TOKEN or another admin key.
type grpcBootstrapResetter struct{ s *Server }

func (g grpcBootstrapResetter) ResetBootstrap(ctx context.Context, req *adminv1.ResetBootstrapRequest) (*adminv1.ResetBootstrapResponse, error) {
	s := g.s
	if s.platformResetter == nil {
		return nil, status.Error(codes.Unavailable, "platform resetter not configured")
	}
	cred, ok := authctx.CredentialFromContext(ctx)
	if !ok {
		return nil, status.Error(codes.Unauthenticated, "missing credential")
	}
	if f := s.authorizeResetBootstrap(ctx, cred, false); f != nil {
		return nil, f.grpcStatus()
	}
	// AsDuration saturates rather than wrapping, and resetBootstrap
	// range-checks the seconds, so an oversized ttl is refused there.
	ttl := req.GetTtl().AsDuration()
	if ttl < 0 || (ttl > 0 && ttl < time.Second) {
		return nil, status.Error(codes.InvalidArgument, "ttl must be at least 1s")
	}
	resp, f := s.resetBootstrap(ctx, cred, cred.UserID, "grpc", resetBootstrapRequest{
		Force:      req.GetForce(),
		TenantID:   req.GetTenantId(),
		TTLSeconds: int64(ttl / time.Second),
	})
	if f != nil {
		return nil, f.grpcStatus()
	}
	out := &adminv1.ResetBootstrapResponse{
		CredentialId:         resp.CredentialID,
		TenantId:             resp.TenantID,
		BearerToken:          resp.BearerToken,
		FileWritten:          resp.FileWritten,
		RevokedCredentialIds: resp.RevokedCredentialIDs,
	}
	if exp, err := time.Parse(time.RFC3339, resp.ExpiresAt); err == nil {
		out.ExpiresAt = timestamppb.New(exp)
	}
	return out, nil
}

func (f *resetFailure) grpcStatus() error {
	code := codes.Internal
	switch f.kind {
	case resetUnavailable:
		code = codes.Unavailable
	case resetBadRequest:
		code = codes.InvalidArgument
	case resetForbidden:
		code = codes.PermissionDenied
	case resetConflict:
		code = codes.FailedPrecondition
	}
	return status.Error(code, f.msg)
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

// authorizeResetBootstrap admits a caller holding `atlas:super_admin`, or
// an admin credential (including a per-tenant admin API key) while the
// install has exactly one tenant (#1851, ADR 0003 amendment). The reset
// clears install-wide markers and rewrites the install's bootstrap-token
// file, so on a multi-tenant install a tenant admin reaching it is a
// cross-tenant effect. On a single-tenant install the only tenant admin is
// the install's operator, and the fallback keeps the CLI recovery path
// working when no browser sign-in (and so no JWT) is possible. superAdmin
// is true only for a verified JWT's claim; a gRPC caller never has one.
func (s *Server) authorizeResetBootstrap(ctx context.Context, cred credstore.Credential, superAdmin bool) *resetFailure {
	if superAdmin {
		return nil
	}
	if !cred.IsAdmin {
		return &resetFailure{resetForbidden, "super_admin required"}
	}
	n, err := s.platformResetter.TenantCount(ctx)
	if err != nil {
		s.installLogger().Warn("reset-bootstrap tenant count failed",
			slog.String("error", err.Error()))
		return &resetFailure{resetInternal, "reset bootstrap failed"}
	}
	if n != 1 {
		return &resetFailure{resetForbidden, "super_admin required on a multi-tenant install"}
	}
	return nil
}
