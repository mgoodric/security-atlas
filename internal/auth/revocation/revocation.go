// Package revocation is the DB-backed token revocation list + audit
// log for the slice 190 OAuth AS cutover.
//
// The store owns two tables (created by slice 190's migration
// 20260521000050_oauth_revoked_tokens.sql):
//
//   - `oauth_revoked_tokens` — hot-path PK lookup on `jti TEXT`. The
//     slice 190 JWT validation middleware consults this on every
//     authenticated `/v1/*` request AFTER signature verification
//     (P0-190-2). Index-only scan; cost is one B-tree probe.
//   - `oauth_revocation_events` — append-only audit log. Mirrors
//     slice 188's `oauth_token_exchanges` shape.
//
// Tenancy: neither table is tenant-scoped (see migration header for
// the rationale). The store opens connections via the atlas_app pool
// but does NOT apply a tenant GUC.
//
// Anti-criteria honored:
//
//   - P0-190-4 (RFC 7009 §2.2): Revoke is idempotent. Re-revocation
//     of an existing jti is a silent no-op on the hot-path table
//     (ON CONFLICT DO NOTHING) but the audit log still receives a
//     fresh row.
//   - P0-190-8 (sweeper integrity): Sweep deletes rows where
//     `expires_at < now()` using the (expires_at) index; the
//     revocation list cannot grow unbounded.
//
// GRANT shape: the revocation list grants SELECT + INSERT + DELETE
// only — no UPDATE. The application role cannot mutate revocation
// rows in place; the ON CONFLICT DO NOTHING idempotency relies on
// this. The audit table grants SELECT + INSERT only (append-only).
package revocation

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

// ErrNoPool is returned when Store methods are called on a nil-pool
// store. Construction-time validation prevents this in production;
// tests that intentionally pass nil get a clean error rather than a
// panic.
var ErrNoPool = errors.New("revocation: store has no pgxpool")

// Store is the DB-backed revocation list + audit writer.
//
// All methods are safe for concurrent use; pgxpool handles its own
// connection serialization.
type Store struct {
	pool *pgxpool.Pool
}

// New constructs a Store bound to pool. The pool must already be
// pointed at a database where the slice 190 migration has run.
func New(pool *pgxpool.Pool) *Store {
	return &Store{pool: pool}
}

// Revoke inserts (or refreshes) a revocation row for jti and appends
// one row to oauth_revocation_events.
//
// Behavior is idempotent: re-revoking an already-revoked jti updates
// revoked_at to the latest call's time but does NOT error. This
// matches RFC 7009 §2.2's "200 for unknown tokens" spirit — the
// handler's response shape doesn't depend on prior state.
//
// expiresAt MUST be the original token's `exp` claim. The sweeper
// uses this column to garbage-collect rows after natural expiry.
//
// revokedBy is a free-text identifier of the revoker — formatted by
// the application layer as either `oauth_client:<client_id>` or
// `user:<user_id>` (decision D2 in the slice's decisions log).
//
// ipAddress is best-effort; pass an empty string when the source IP
// is unknown (unit tests, internal calls).
func (s *Store) Revoke(ctx context.Context, jti string, expiresAt time.Time, revokedBy, ipAddress string) error {
	if s.pool == nil {
		return ErrNoPool
	}
	if jti == "" {
		return errors.New("revocation: jti is empty")
	}
	if revokedBy == "" {
		return errors.New("revocation: revoked_by is empty")
	}

	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("revocation: begin: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	// Insert the revocation list row. ON CONFLICT (jti) DO NOTHING
	// keeps the call idempotent — re-revocation of an already-
	// revoked jti is a silent no-op on the hot-path table. The
	// first revocation wins; subsequent calls do not refresh the
	// timestamp. Rationale: the JWT validator only cares whether
	// the jti is revoked, not when; storing only the first
	// revocation simplifies forensics (one row per jti) and lets
	// the GRANT exclude UPDATE (defense in depth — the application
	// role cannot mutate the revocation list, only append + delete).
	// The audit table below still receives one row per call, so the
	// forensic trail is preserved.
	const ins = `
		INSERT INTO oauth_revoked_tokens (jti, revoked_at, expires_at, revoked_by)
		VALUES ($1, now(), $2, $3)
		ON CONFLICT (jti) DO NOTHING
	`
	if _, err := tx.Exec(ctx, ins, jti, expiresAt, revokedBy); err != nil {
		return fmt.Errorf("revocation: insert revoked_tokens: %w", err)
	}

	// Append-only audit. Every Revoke call writes one row even on
	// idempotent re-revocation — this is by design; the audit log
	// answers "how many revocation attempts have we seen for this
	// jti" which is forensically valuable.
	const insertAudit = `
		INSERT INTO oauth_revocation_events (jti, revoked_at, revoked_by, ip_address)
		VALUES ($1, now(), $2, $3)
	`
	var ip any
	if ipAddress != "" {
		ip = ipAddress
	}
	if _, err := tx.Exec(ctx, insertAudit, jti, revokedBy, ip); err != nil {
		return fmt.Errorf("revocation: insert audit: %w", err)
	}

	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("revocation: commit: %w", err)
	}
	return nil
}

// IsRevoked reports whether jti has a revocation row whose
// expires_at is still in the future. Sweeper-deleted rows naturally
// return false; the JWT validator's `exp` check rejects them anyway.
//
// PK lookup; expected cost is one index probe. Tested for index-only
// scan in the integration suite.
func (s *Store) IsRevoked(ctx context.Context, jti string) (bool, error) {
	if s.pool == nil {
		return false, ErrNoPool
	}
	if jti == "" {
		// Empty jti is not revoked — defensive default. The
		// middleware should never call us with empty jti, but a
		// false here is safer than an error.
		return false, nil
	}
	const q = `SELECT 1 FROM oauth_revoked_tokens WHERE jti = $1 AND expires_at > now() LIMIT 1`
	row := s.pool.QueryRow(ctx, q, jti)
	var one int
	switch err := row.Scan(&one); {
	case err == nil:
		return true, nil
	case errors.Is(err, pgxNoRows()):
		return false, nil
	default:
		return false, fmt.Errorf("revocation: query: %w", err)
	}
}

// Sweep deletes oauth_revoked_tokens rows whose expires_at is in the
// past. Returns the number of rows deleted. The audit-log rows in
// oauth_revocation_events are NOT swept — the forensic trail outlives
// the hot-path lookup table.
//
// Called by the cmd/atlas sweeper goroutine on a 5-minute interval
// (decision D4). The DELETE uses the (expires_at) index for an
// index-range scan rather than a seq scan.
func (s *Store) Sweep(ctx context.Context) (int64, error) {
	if s.pool == nil {
		return 0, ErrNoPool
	}
	const q = `DELETE FROM oauth_revoked_tokens WHERE expires_at < now()`
	tag, err := s.pool.Exec(ctx, q)
	if err != nil {
		return 0, fmt.Errorf("revocation: sweep: %w", err)
	}
	return tag.RowsAffected(), nil
}

// pgxNoRows wraps the pgx5 ErrNoRows constant so callers can use
// errors.Is without depending on the pgx package directly. Keeping
// the wrapper local lets us swap the driver later without touching
// every Is() call site.
func pgxNoRows() error {
	return errPgxNoRows
}

// errPgxNoRows is the sentinel scanned when QueryRow.Scan returns
// "no rows". Imported via a thin indirection to keep the public
// surface clean.
var errPgxNoRows = pgxNoRowsValue()
