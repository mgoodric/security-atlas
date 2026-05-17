// Package platform exposes platform-level read/write helpers that sit
// outside the tenancy model. The Status type backs the slice-073 first-time
// login UX: a singleton `platform_status` row records whether the platform
// has ever seen a successful sign-in.
//
// Two surfaces:
//
//   - IsFirstInstall(ctx) — public read. Used by the public GET
//     /v1/install-state endpoint. May run on any pool because the
//     migration grants SELECT to atlas_app and policy public_read is
//     USING (true).
//
//   - MarkFirstSignin(ctx, at) — elevated write. Must run on the migrate
//     pool (BYPASSRLS) because the migration intentionally grants atlas_app
//     no UPDATE policy on platform_status. Idempotent: only writes when
//     first_signin_at IS NULL; subsequent calls are no-ops. This is the
//     load-bearing P0-A1 property — the marker flips exactly once.
//
//   - ResetBootstrap(ctx, force) — elevated write used by the
//     atlas-cli `credentials issue --reset-bootstrap` recovery flag
//     (slice 073 AC-8). Clears bootstrap_token_consumed_at. Refuses
//     unless first_signin_at IS NULL OR force == true. Also runs on the
//     migrate pool.
//
// Why two pools? The read path is hot (every page render of /login on
// every visitor); the write path is rare (once per platform lifetime,
// plus the occasional recovery). Splitting them keeps the read on the
// app pool's connection budget and lets the write path run BYPASSRLS
// without exposing atlas_app to any cross-tenant mutation surface.
package platform

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
)

// DBExecutor is the narrow interface over pgxpool.Pool / pgx.Conn that
// Status uses. Defined for testability and to keep the dependency on
// jackc/pgx narrow (Status doesn't need transactions).
type DBExecutor interface {
	QueryRow(ctx context.Context, sql string, args ...any) pgx.Row
	Exec(ctx context.Context, sql string, args ...any) (pgconn.CommandTag, error)
}

// Status reads and writes the singleton platform_status row.
//
// readPool: any pool with SELECT on platform_status. The app pool is fine
//
//	(the migration's public_read RLS policy is USING (true)).
//
// writePool: must be the migrate pool (BYPASSRLS). atlas_app has no
//
//	INSERT/UPDATE/DELETE policy under FORCE ROW LEVEL SECURITY and
//	would silently no-op (RLS-filtered zero rows) on a write attempt.
//	May be nil for unit-only tests that only exercise reads; in that
//	case MarkFirstSignin and ResetBootstrap return ErrWriteNotConfigured.
type Status struct {
	readPool  DBExecutor
	writePool DBExecutor
}

// ErrWriteNotConfigured is returned by write methods when the Status was
// constructed without a write pool. cmd/atlas configures both pools; unit
// servers that exercise only the read path leave the write pool nil.
var ErrWriteNotConfigured = errors.New("platform: write pool not configured (need migrate-role pool)")

// ErrResetForbidden is returned by ResetBootstrap when first_signin_at is
// already set and force was not supplied. The recovery flag exists for
// the case where the platform was never signed into; re-issuing the
// bootstrap token after a real user is on the system is a foot-gun and
// requires --force.
var ErrResetForbidden = errors.New("platform: refusing to reset bootstrap marker; real user has signed in (pass --force to override)")

// NewStatus constructs a Status. readPool is required; writePool may be
// nil for read-only servers.
func NewStatus(readPool DBExecutor, writePool DBExecutor) *Status {
	return &Status{readPool: readPool, writePool: writePool}
}

// IsFirstInstall returns true when the singleton row's first_signin_at
// column is NULL — i.e. no successful sign-in has yet been recorded.
// Returns an error only when the read fails (no row found counts as
// an error: the migration seeds the row, so its absence is a corrupted
// install and the caller should surface that loudly).
func (s *Status) IsFirstInstall(ctx context.Context) (bool, error) {
	if s.readPool == nil {
		return false, errors.New("platform: read pool not configured")
	}
	var firstSigninAt *time.Time
	err := s.readPool.QueryRow(ctx, `SELECT first_signin_at FROM platform_status`).Scan(&firstSigninAt)
	if err != nil {
		return false, fmt.Errorf("platform: read platform_status: %w", err)
	}
	return firstSigninAt == nil, nil
}

// MarkFirstSignin flips first_signin_at to `at` if it is currently NULL.
// Subsequent calls are no-ops (the UPDATE's WHERE first_signin_at IS NULL
// filters them out). Returns (didWrite, error): didWrite is true on the
// first call that actually flips the marker; false on every subsequent
// idempotent no-op. The caller (the BFF route's upstream handler) uses
// didWrite to know whether to also delete the bootstrap-token file.
//
// `at` is supplied by the caller for testability — production passes
// time.Now().UTC().
func (s *Status) MarkFirstSignin(ctx context.Context, at time.Time) (bool, error) {
	if s.writePool == nil {
		return false, ErrWriteNotConfigured
	}
	tag, err := s.writePool.Exec(
		ctx,
		`UPDATE platform_status
            SET first_signin_at = $1,
                bootstrap_token_consumed_at = $1
            WHERE first_signin_at IS NULL`,
		at.UTC(),
	)
	if err != nil {
		return false, fmt.Errorf("platform: mark first signin: %w", err)
	}
	return tag.RowsAffected() == 1, nil
}

// ResetBootstrap clears bootstrap_token_consumed_at so a re-issued
// bootstrap token can be consumed again. If first_signin_at is already
// set and `force` is false, returns ErrResetForbidden — re-arming
// bootstrap after a real user has signed in is a foot-gun and requires
// explicit intent (slice 073 AC-8, P0-A6).
//
// When force is true and first_signin_at is set, ResetBootstrap also
// clears first_signin_at — the operator is effectively declaring "treat
// this as a fresh install again". This is the recovery path for the case
// where the platform got into a corrupted state and the operator wants
// the install-state endpoint to return first_install=true so the login
// UX flips back to the guidance mode.
func (s *Status) ResetBootstrap(ctx context.Context, force bool) error {
	if s.writePool == nil {
		return ErrWriteNotConfigured
	}
	var firstSigninAt *time.Time
	err := s.writePool.QueryRow(ctx, `SELECT first_signin_at FROM platform_status`).Scan(&firstSigninAt)
	if err != nil {
		return fmt.Errorf("platform: read platform_status: %w", err)
	}
	if firstSigninAt != nil && !force {
		return ErrResetForbidden
	}
	_, err = s.writePool.Exec(
		ctx,
		`UPDATE platform_status
            SET first_signin_at = NULL,
                bootstrap_token_consumed_at = NULL`,
	)
	if err != nil {
		return fmt.Errorf("platform: reset bootstrap: %w", err)
	}
	return nil
}
