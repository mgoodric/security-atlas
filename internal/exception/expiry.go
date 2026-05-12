package exception

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/mgoodric/security-atlas/internal/db/dbx"
	"github.com/mgoodric/security-atlas/internal/tenancy"
)

// DefaultExpiryInterval is the cadence at which Expirer.Run sweeps for
// expired exceptions. Daily (per AC-5) is the canvas-implied cadence; the
// platform binary can override via ATLAS_EXCEPTION_EXPIRY_INTERVAL when
// operators want a snappier dev loop.
const DefaultExpiryInterval = 24 * time.Hour

// Expirer is the auto-expiry tick loop (AC-5). It runs as the migrator
// role (BYPASSRLS) because the sweep crosses tenants -- there is no single
// tenant context for "all expired exceptions across the system". For each
// tenant with active exceptions, it applies the GUC inside a per-tenant
// transaction and runs ExpireActiveExceptionsBefore, paired with one
// audit-log row per expired exception (anti-criterion P0: no silent
// expiry).
type Expirer struct {
	pool   *pgxpool.Pool
	logger *slog.Logger
}

// NewExpirer constructs an Expirer. The pool MUST be connected as the
// migrator role (BYPASSRLS) -- ListTenantsWithActiveExceptions enumerates
// every tenant, which an app-role pool would scope away. logger may be
// nil; a discard logger is substituted.
func NewExpirer(pool *pgxpool.Pool, logger *slog.Logger) *Expirer {
	if logger == nil {
		logger = slog.New(slog.NewTextHandler(discardWriter{}, nil))
	}
	return &Expirer{pool: pool, logger: logger}
}

// Run executes the tick loop until ctx is cancelled. Each tick sweeps all
// tenants with active exceptions. Ticker fires on `interval`; the first
// sweep runs immediately so a fresh deploy doesn't sit silent for 24h.
func (e *Expirer) Run(ctx context.Context, interval time.Duration) error {
	if interval <= 0 {
		interval = DefaultExpiryInterval
	}
	e.logger.Info("exception expirer starting", "interval", interval.String())
	if _, err := e.SweepOnce(ctx); err != nil && !errors.Is(err, context.Canceled) {
		e.logger.Error("exception expirer initial sweep", "err", err.Error())
	}
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			e.logger.Info("exception expirer stopping")
			return nil
		case <-ticker.C:
			if _, err := e.SweepOnce(ctx); err != nil && !errors.Is(err, context.Canceled) {
				e.logger.Error("exception expirer sweep", "err", err.Error())
			}
		}
	}
}

// SweepOnce runs a single expiry sweep across all tenants. Returns the
// count of expired rows for observability. Exposed for integration tests
// and called from Run's tick loop.
func (e *Expirer) SweepOnce(ctx context.Context) (int, error) {
	tenantIDs, err := e.listTenants(ctx)
	if err != nil {
		return 0, fmt.Errorf("list tenants: %w", err)
	}
	now := time.Now().UTC()
	total := 0
	for _, tenantID := range tenantIDs {
		select {
		case <-ctx.Done():
			return total, ctx.Err()
		default:
		}
		n, err := e.sweepTenant(ctx, tenantID, now)
		if err != nil {
			e.logger.Error("exception sweep tenant", "tenant_id", tenantID.String(), "err", err.Error())
			continue
		}
		total += n
		if n > 0 {
			e.logger.Info("expired exceptions", "tenant_id", tenantID.String(), "count", n)
		}
	}
	return total, nil
}

// listTenants enumerates every tenant with at least one active exception.
// Runs as the migrator role (BYPASSRLS).
func (e *Expirer) listTenants(ctx context.Context) ([]uuid.UUID, error) {
	conn, err := e.pool.Acquire(ctx)
	if err != nil {
		return nil, fmt.Errorf("acquire: %w", err)
	}
	defer conn.Release()
	q := dbx.New(conn)
	rows, err := q.ListTenantsWithActiveExceptions(ctx)
	if err != nil {
		return nil, fmt.Errorf("query tenants: %w", err)
	}
	out := make([]uuid.UUID, len(rows))
	for i, r := range rows {
		out[i] = uuid.UUID(r.Bytes)
	}
	return out, nil
}

// sweepTenant expires every active row in the tenant whose expires_at <
// now. Pairs each expired row with an exception_audit_log row in the same
// transaction so the audit trail is atomic with the state change.
//
// Idempotent: if no active rows have expired since the last sweep, the
// UPDATE returns zero rows and no audit rows are written.
func (e *Expirer) sweepTenant(ctx context.Context, tenantID uuid.UUID, now time.Time) (int, error) {
	tenantCtx, err := tenancy.WithTenant(ctx, tenantID.String())
	if err != nil {
		return 0, fmt.Errorf("with tenant: %w", err)
	}
	tx, err := e.pool.Begin(tenantCtx)
	if err != nil {
		return 0, fmt.Errorf("begin tx: %w", err)
	}
	defer func() { _ = tx.Rollback(tenantCtx) }()

	if err := tenancy.ApplyTenant(tenantCtx, tx); err != nil {
		return 0, err
	}
	q := dbx.New(tx)
	expired, err := q.ExpireActiveExceptionsBefore(tenantCtx, dbx.ExpireActiveExceptionsBeforeParams{
		TenantID:  pgtype.UUID{Bytes: tenantID, Valid: true},
		ExpiresAt: pgTimestamptz(now),
	})
	if err != nil {
		return 0, fmt.Errorf("expire active: %w", err)
	}
	// Anti-criterion P0: every expiry writes an audit log row. The action
	// is `expired`, the actor is the SystemActor constant so audit-trail
	// review can segregate system-driven transitions from human ones.
	for _, row := range expired {
		if _, alErr := q.WriteExceptionAuditLog(tenantCtx, dbx.WriteExceptionAuditLogParams{
			ID:          pgtype.UUID{Bytes: uuid.New(), Valid: true},
			TenantID:    pgtype.UUID{Bytes: tenantID, Valid: true},
			ExceptionID: row.ID,
			Action:      ActionExpired,
			Actor:       SystemActor,
			FromState:   stringPtr(StateActive),
			ToState:     StateExpired,
			Reason:      fmt.Sprintf("expires_at=%s < now=%s", row.ExpiresAt.Time.UTC().Format(time.RFC3339), now.Format(time.RFC3339)),
		}); alErr != nil {
			return 0, fmt.Errorf("write expired audit log: %w", alErr)
		}
	}
	if err := tx.Commit(tenantCtx); err != nil {
		return 0, fmt.Errorf("commit: %w", err)
	}
	return len(expired), nil
}

// discardWriter satisfies io.Writer for a discard slog handler.
type discardWriter struct{}

func (discardWriter) Write(p []byte) (int, error) { return len(p), nil }
