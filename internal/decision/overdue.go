package decision

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/mgoodric/security-atlas/internal/db/dbx"
	"github.com/mgoodric/security-atlas/internal/tenancy"
)

// NotificationTypeOverdue is the notifications.type value for a
// decision-overdue notification. The /v1/me/notifications wire layer
// renders type-specific UI off this constant.
const NotificationTypeOverdue = "decision.overdue"

// DefaultOverdueInterval is the cadence at which Notifier.Run sweeps for
// overdue decisions. Daily (per AC-6) is the canvas-implied cadence; the
// platform binary can override via ATLAS_DECISION_OVERDUE_INTERVAL when
// operators want a snappier dev loop.
const DefaultOverdueInterval = 24 * time.Hour

// overduePayload is the JSON shape stored in notifications.payload for a
// decision-overdue notification.
type overduePayload struct {
	DecisionUUID string `json:"decision_uuid"`
	DecisionID   string `json:"decision_id"`
	Title        string `json:"title"`
	RevisitBy    string `json:"revisit_by"`
}

// Notifier is the daily overdue-decision notification job (AC-6). It runs
// as the migrator role (BYPASSRLS) because the sweep crosses tenants --
// there is no single tenant context for "all overdue decisions across the
// system". For each tenant with overdue decisions, it applies the GUC
// inside a per-tenant transaction and, for each overdue decision that has
// not already been notified, writes one notification to the
// decision_maker plus one `overdue_notified` row to decisions_audit.
//
// Anti-criterion P0: one notification per overdue decision, never
// repeated. The `overdue_notified` audit row is the authoritative
// "already notified" marker -- CountDecisionOverdueNotifications probes
// for it before emitting. The notification + the audit row are written in
// the SAME transaction, so a partial state (notified but not recorded, or
// recorded but not notified) is impossible.
type Notifier struct {
	pool   *pgxpool.Pool
	logger *slog.Logger
}

// NewNotifier constructs a Notifier. The pool MUST be connected as the
// migrator role (BYPASSRLS) -- ListTenantsWithOverdueDecisions enumerates
// every tenant, which an app-role pool would scope away. logger may be
// nil; a discard logger is substituted.
func NewNotifier(pool *pgxpool.Pool, logger *slog.Logger) *Notifier {
	if logger == nil {
		logger = slog.New(slog.NewTextHandler(discardWriter{}, nil))
	}
	return &Notifier{pool: pool, logger: logger}
}

// Run executes the tick loop until ctx is cancelled. Each tick sweeps all
// tenants with overdue decisions. Ticker fires on `interval`; the first
// sweep runs immediately so a fresh deploy doesn't sit silent for 24h.
func (n *Notifier) Run(ctx context.Context, interval time.Duration) error {
	if interval <= 0 {
		interval = DefaultOverdueInterval
	}
	n.logger.Info("decision overdue notifier starting", "interval", interval.String())
	if _, err := n.SweepOnce(ctx); err != nil && !errors.Is(err, context.Canceled) {
		n.logger.Error("decision overdue notifier initial sweep", "err", err.Error())
	}
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			n.logger.Info("decision overdue notifier stopping")
			return nil
		case <-ticker.C:
			if _, err := n.SweepOnce(ctx); err != nil && !errors.Is(err, context.Canceled) {
				n.logger.Error("decision overdue notifier sweep", "err", err.Error())
			}
		}
	}
}

// SweepOnce runs a single overdue sweep across all tenants. Returns the
// count of notifications emitted for observability. Exposed for
// integration tests and called from Run's tick loop.
func (n *Notifier) SweepOnce(ctx context.Context) (int, error) {
	today := time.Now().UTC()
	tenantIDs, err := n.listTenants(ctx, today)
	if err != nil {
		return 0, fmt.Errorf("list tenants with overdue decisions: %w", err)
	}
	total := 0
	for _, tenantID := range tenantIDs {
		select {
		case <-ctx.Done():
			return total, ctx.Err()
		default:
		}
		emitted, err := n.sweepTenant(ctx, tenantID, today)
		if err != nil {
			n.logger.Error("decision overdue sweep tenant", "tenant_id", tenantID.String(), "err", err.Error())
			continue
		}
		total += emitted
		if emitted > 0 {
			n.logger.Info("decision overdue notifications emitted", "tenant_id", tenantID.String(), "count", emitted)
		}
	}
	return total, nil
}

// listTenants enumerates every tenant with at least one active, overdue
// decision. Runs as the migrator role (BYPASSRLS).
func (n *Notifier) listTenants(ctx context.Context, today time.Time) ([]uuid.UUID, error) {
	conn, err := n.pool.Acquire(ctx)
	if err != nil {
		return nil, fmt.Errorf("acquire: %w", err)
	}
	defer conn.Release()
	q := dbx.New(conn)
	rows, err := q.ListTenantsWithOverdueDecisions(ctx, pgDateValue(today))
	if err != nil {
		return nil, fmt.Errorf("query tenants: %w", err)
	}
	out := make([]uuid.UUID, len(rows))
	for i, r := range rows {
		out[i] = uuid.UUID(r.Bytes)
	}
	return out, nil
}

// sweepTenant emits notifications for every not-yet-notified overdue
// decision in the tenant. For each, it writes the notification row and the
// `overdue_notified` audit row in the SAME transaction so the dedup marker
// and the notification can never diverge.
//
// Idempotent: a decision that already has an `overdue_notified` audit row
// is skipped. Re-running the sweep on the same day -- or any later day
// while the decision stays active + overdue -- emits nothing further for
// that decision (P0 anti-criterion).
func (n *Notifier) sweepTenant(ctx context.Context, tenantID uuid.UUID, today time.Time) (int, error) {
	tenantCtx, err := tenancy.WithTenant(ctx, tenantID.String())
	if err != nil {
		return 0, fmt.Errorf("with tenant: %w", err)
	}
	tx, err := n.pool.Begin(tenantCtx)
	if err != nil {
		return 0, fmt.Errorf("begin tx: %w", err)
	}
	defer func() { _ = tx.Rollback(tenantCtx) }()

	if err := tenancy.ApplyTenant(tenantCtx, tx); err != nil {
		return 0, err
	}
	q := dbx.New(tx)

	overdue, err := q.ListOverdueDecisions(tenantCtx, dbx.ListOverdueDecisionsParams{
		TenantID:  pgUUID(tenantID),
		RevisitBy: pgDateValue(today),
	})
	if err != nil {
		return 0, fmt.Errorf("list overdue decisions: %w", err)
	}

	emitted := 0
	for _, row := range overdue {
		d := decisionFromRow(row)

		// Dedup probe: skip if this decision already has an
		// `overdue_notified` audit row (P0 anti-criterion).
		already, err := q.CountDecisionOverdueNotifications(tenantCtx, dbx.CountDecisionOverdueNotificationsParams{
			TenantID:   pgUUID(tenantID),
			DecisionID: pgUUID(d.ID),
		})
		if err != nil {
			return 0, fmt.Errorf("count overdue notifications: %w", err)
		}
		if already > 0 {
			continue
		}

		revisit := ""
		if d.RevisitBy != nil {
			revisit = d.RevisitBy.UTC().Format("2006-01-02")
		}
		payload, err := json.Marshal(overduePayload{
			DecisionUUID: d.ID.String(),
			DecisionID:   d.DecisionID,
			Title:        d.Title,
			RevisitBy:    revisit,
		})
		if err != nil {
			return 0, fmt.Errorf("marshal overdue payload: %w", err)
		}

		// AC-6: the recipient is the decision's decision_maker.
		if _, err := q.CreateNotification(tenantCtx, dbx.CreateNotificationParams{
			ID:              pgUUID(uuid.New()),
			TenantID:        pgUUID(tenantID),
			RecipientUserID: d.DecisionMaker,
			Type:            NotificationTypeOverdue,
			Payload:         payload,
		}); err != nil {
			return 0, fmt.Errorf("create overdue notification: %w", err)
		}

		// The authoritative dedup marker, written in the same tx.
		if _, err := q.WriteDecisionAudit(tenantCtx, dbx.WriteDecisionAuditParams{
			ID:         pgUUID(uuid.New()),
			TenantID:   pgUUID(tenantID),
			DecisionID: pgUUID(d.ID),
			Action:     ActionOverdueNotified,
			Actor:      SystemActor,
			Detail:     fmt.Sprintf("recipient=%s revisit_by=%s", d.DecisionMaker, revisit),
		}); err != nil {
			return 0, fmt.Errorf("write overdue_notified audit: %w", err)
		}
		emitted++
	}

	if err := tx.Commit(tenantCtx); err != nil {
		return 0, fmt.Errorf("commit: %w", err)
	}
	return emitted, nil
}

// discardWriter satisfies io.Writer for a discard slog handler.
type discardWriter struct{}

func (discardWriter) Write(p []byte) (int, error) { return len(p), nil }
