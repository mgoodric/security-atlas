// Slice 073: public install-state endpoint + elevated mark-first-signin
// handler. Both surfaces back the first-time login UX in web/app/login.

package api

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"time"

	"github.com/google/uuid"

	"github.com/mgoodric/security-atlas/internal/platform"
)

// PlatformStatus is the narrow interface over *platform.Status that the
// HTTP handlers depend on. Lets us test the handlers with a fake without
// touching the database.
type PlatformStatus interface {
	IsFirstInstall(ctx context.Context) (bool, error)
	MarkFirstSignin(ctx context.Context, at time.Time) (bool, error)
	// BootstrapTenantID returns the canonical bootstrap-tenant id during
	// fresh-install state. Returns uuid.Nil with a nil error when no
	// bootstrap tenant can be resolved — the handler treats that as
	// "no tenant_id to include" and degrades gracefully (slice 210).
	BootstrapTenantID(ctx context.Context) (uuid.UUID, error)
}

// AttachPlatformStatus wires the slice-073 Status reader/writer onto the
// server. cmd/atlas constructs it once at startup with the app pool (for
// the public read) and the migrate pool (for the elevated write). Unit
// servers leave it nil and the install-state routes return 503 — the
// fresh-install detection cannot proceed without a DB.
func (s *Server) AttachPlatformStatus(ps PlatformStatus) {
	s.platformStatus = ps
}

// AttachBootstrapTokenPath registers the on-disk location of the
// bootstrap-token file. cmd/atlas sets it at startup based on
// ATLAS_DATA_DIR; on first sign-in the platform deletes the file as
// part of the mark-first-signin handler. Unit servers leave it empty
// and the handler skips the file step.
func (s *Server) AttachBootstrapTokenPath(path string) {
	s.bootstrapTokenPath = path
}

// AttachLogger wires the platform logger onto the server for handlers
// that log non-request-bound events (slice-073 bootstrap-token deletion
// is the first such surface). Unit servers leave it nil and the
// handlers use slog.Default().
func (s *Server) AttachLogger(l *slog.Logger) {
	s.logger = l
}

// installStateResponse is the public response shape for GET /v1/install-state.
//
// Slice 210: TenantID is included only on fresh-install responses, and only
// when a bootstrap tenant can be resolved (see PlatformStatus.BootstrapTenantID).
// `omitempty` keeps the response shape unchanged for post-first-install
// installs (which never carry a tenant_id) and for the graceful-degradation
// path where the tenant lookup failed.
type installStateResponse struct {
	FirstInstall bool   `json:"first_install"`
	TenantID     string `json:"tenant_id,omitempty"`
}

// handleInstallState answers GET /v1/install-state with
// {"first_install": bool}. The endpoint is INTENTIONALLY public — same
// precedent as /health (slice 037) and /v1/version (slice 072 parallel).
// "Is this a fresh install?" is platform metadata, not tenant data
// (P0-A4).
//
// Cache-Control: no-store because the flag flips at first sign-in and
// the UI needs to see it flip without a CDN or browser caching the
// pre-flip value.
//
// Failures return 503 with a generic error: this endpoint is a hint to
// the UI, not a hard dependency. The login page renders the existing
// copy when the endpoint errors (production-safe fallback).
func (s *Server) handleInstallState(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	if s.platformStatus == nil {
		w.WriteHeader(http.StatusServiceUnavailable)
		_, _ = w.Write([]byte(`{"error":"install state not configured"}`))
		return
	}
	first, err := s.platformStatus.IsFirstInstall(r.Context())
	if err != nil {
		// Logging is best-effort; the response is the hint to the UI.
		s.installLogger().Warn("install-state read failed", slog.String("error", err.Error()))
		w.WriteHeader(http.StatusServiceUnavailable)
		_, _ = w.Write([]byte(`{"error":"install state unavailable"}`))
		return
	}
	resp := installStateResponse{FirstInstall: first}
	// Slice 210 — when in fresh-install state, resolve the bootstrap
	// tenant id so slice 209's login form can auto-populate its hidden
	// `tenant_id` field. Failure is non-fatal: a backend hiccup must not
	// break the login render. The endpoint stays HTTP 200 with the field
	// omitted (P0-A3: post-first-install responses are unchanged).
	if first {
		tid, terr := s.platformStatus.BootstrapTenantID(r.Context())
		if terr != nil {
			s.installLogger().Warn("install-state bootstrap-tenant lookup failed",
				slog.String("error", terr.Error()))
		} else if tid != uuid.Nil {
			resp.TenantID = tid.String()
		}
	}
	_ = json.NewEncoder(w).Encode(resp)
}

// markFirstSigninResponse is the response shape for the elevated POST
// /v1/install/mark-first-signin endpoint.
type markFirstSigninResponse struct {
	Marked     bool `json:"marked"`
	FileDelete bool `json:"file_deleted"`
}

// handleMarkFirstSignin flips platform_status.first_signin_at if it is
// currently NULL AND, on the first successful flip, atomically deletes
// the bootstrap-token file at the configured path.
//
// This handler is NOT public — the slice's anti-criterion is that a
// bearer is required (so a drive-by attacker cannot flip the marker and
// trick the login UX into hiding the fresh-install guidance for the
// next operator). The bearer auth middleware applied by httpHandler
// already gates this route. The leading /v1/install path lives outside
// any /_internal namespace: the bearer-auth middleware is the access
// control, not a path-prefix convention.
//
// The handler is idempotent: a subsequent call is a no-op (marked=false,
// file_deleted=false). The cmd/atlas startup path mints the bootstrap
// fixed-token credential under bootstrap_consumed_at-tracked state; that
// consumption is independent of this slice's flag. This slice's flag is
// "has any user ever signed in?" — a strictly stronger signal that the
// platform is no longer in fresh-install mode.
func (s *Server) handleMarkFirstSignin(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	if s.platformStatus == nil {
		w.WriteHeader(http.StatusServiceUnavailable)
		_, _ = w.Write([]byte(`{"error":"install state not configured"}`))
		return
	}
	marked, err := s.platformStatus.MarkFirstSignin(r.Context(), time.Now().UTC())
	if err != nil {
		if errors.Is(err, platform.ErrWriteNotConfigured) {
			w.WriteHeader(http.StatusServiceUnavailable)
			_, _ = w.Write([]byte(`{"error":"install state write not configured"}`))
			return
		}
		s.installLogger().Warn("mark-first-signin write failed", slog.String("error", err.Error()))
		w.WriteHeader(http.StatusInternalServerError)
		_, _ = w.Write([]byte(`{"error":"mark first signin failed"}`))
		return
	}
	resp := markFirstSigninResponse{Marked: marked, FileDelete: false}
	if marked && s.bootstrapTokenPath != "" {
		// Only the FIRST flip should attempt file deletion; idempotent
		// re-calls (marked=false) leave the (already-absent) file alone.
		// Errors here are best-effort — the marker is already flipped
		// in the DB, so the platform is no longer in fresh-install mode
		// regardless of whether the file unlink succeeded.
		if err := platform.DeleteBootstrapToken(s.bootstrapTokenPath, s.installLogger()); err != nil {
			s.installLogger().Warn("bootstrap-token delete failed",
				slog.String("error", err.Error()))
		} else {
			resp.FileDelete = true
		}
		// The DB filter `WHERE first_signin_at IS NULL` guarantees
		// exactly one handler observes marked=true across all concurrent
		// callers; no in-process dedup is needed.
	}
	_ = json.NewEncoder(w).Encode(resp)
}

// installLogger returns a non-nil slog.Logger, falling back to the
// default when no logger has been attached.
func (s *Server) installLogger() *slog.Logger {
	if s.logger != nil {
		return s.logger
	}
	return slog.Default()
}
