package staleness

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/mgoodric/security-atlas/internal/audit/notifications"
	"github.com/mgoodric/security-atlas/internal/db/dbx"
	"github.com/mgoodric/security-atlas/internal/eval"
	"github.com/mgoodric/security-atlas/internal/freshness"
	"github.com/mgoodric/security-atlas/internal/tenancy"
)

// FreshnessLister is the read surface the rollup consumes — exactly
// freshness.Store.List. Narrowed to an interface so the rollup is unit-test-
// seamable and so the dependency direction is explicit: staleness depends on
// the freshness READ MODEL, never the evidence ledger (invariant #2).
type FreshnessLister interface {
	List(ctx context.Context) ([]freshness.ControlFreshness, error)
}

// Store is the slice-439 rollup producer's data-access + write layer. It runs
// ONE tenant's rollup per call, under the tenant GUC the caller (the
// scheduler) has already established. Its only writes are slice-029
// notification rows + slice-439 idempotency-ledger rows — never the evidence
// ledger.
type Store struct {
	pool      *pgxpool.Pool
	freshness FreshnessLister
	now       func() time.Time

	// approachingWindow is the early-warning band width. Overridable for tests.
	approachingWindow time.Duration
}

// NewStore wires a Store over the application pool. `fresh` is the freshness
// read model (freshness.NewStore(pool)). The pool must be the atlas_app role
// (NOBYPASSRLS) so RLS is enforced.
func NewStore(pool *pgxpool.Pool, fresh FreshnessLister) *Store {
	return &Store{
		pool:              pool,
		freshness:         fresh,
		now:               func() time.Time { return time.Now().UTC() },
		approachingWindow: DefaultApproachingWindow,
	}
}

// TenantReport tallies one tenant's rollup pass.
type TenantReport struct {
	Recipients     int
	AlertsWritten  int
	AlertsDeduped  int
	DigestsWritten int
	DigestsDeduped int
	StaleControls  int
	ApprControls   int
}

// classified is one control's freshness projected to a band + the digest item.
type classified struct {
	band Band
	item DigestItem
}

// RollupTenant runs the staleness rollup for the tenant currently in ctx. The
// caller MUST have set the tenant GUC (the scheduler does this via
// tenancy.WithTenant before calling). The pass:
//
//  1. reads the freshness read model (every control's derived state),
//  2. classifies each control into a band,
//  3. enumerates the tenant's active users (recipients),
//  4. for each recipient: writes one per-control alert per newly-stale control
//     (idempotent on the recompute period) and one weekly digest (idempotent
//     on the ISO-week), honoring the per-user in_app opt-out.
//
// Every write happens under THIS tenant's GUC; a recipient is always one of
// THIS tenant's users (threat-model I). `weekly` controls whether the digest
// is attempted this pass (the scheduler only sets it on the weekly trigger).
func (s *Store) RollupTenant(ctx context.Context, weekly bool) (TenantReport, error) {
	now := s.now()
	rep := TenantReport{}

	// 1+2: read freshness + classify. Pure read of the read model.
	rows, err := s.freshness.List(ctx)
	if err != nil {
		return rep, fmt.Errorf("staleness: list freshness: %w", err)
	}
	classifiedRows := make([]classified, 0, len(rows))
	for _, r := range rows {
		band := Classify(Cell{ValidUntil: r.ValidUntil, IsStale: r.IsStale}, now, s.approachingWindow)
		if band == BandFresh {
			continue
		}
		if band == BandStale {
			rep.StaleControls++
		} else {
			rep.ApprControls++
		}
		classifiedRows = append(classifiedRows, classified{
			band: band,
			item: DigestItem{
				ControlID:      r.ControlID.String(),
				FreshnessClass: r.FreshnessClass,
				Band:           band.String(),
			},
		})
	}

	// 3+4 run in one tenant tx so the recipient enumeration, the dedup
	// claims, and the notification writes all share the tenant GUC + commit
	// atomically per tenant.
	tenantID, err := tenantIDFromCtx(ctx)
	if err != nil {
		return rep, err
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return rep, fmt.Errorf("staleness: begin tx: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if err := tenancy.ApplyTenant(ctx, tx); err != nil {
		return rep, err
	}
	q := dbx.New(tx)

	users, err := q.ListActiveUsersForTenant(ctx, pgUUID(tenantID))
	if err != nil {
		return rep, fmt.Errorf("staleness: list users: %w", err)
	}
	rep.Recipients = len(users)
	if len(users) == 0 {
		// Nothing to deliver; still commit (no-op) so the tx closes cleanly.
		return rep, tx.Commit(ctx)
	}

	// Pull each user's in_app preference for the evidence_staleness event so
	// an explicit opt-out suppresses delivery (AC-7). Default-on-missing-row.
	period := recomputePeriodKey(now)
	week := isoWeekKey(now)
	periodStart, periodEnd := isoWeekBounds(now)

	for _, u := range users {
		recipient := uuid.UUID(u.ID.Bytes).String()
		optedIn, err := s.inAppEnabled(ctx, q, tenantID, u.ID)
		if err != nil {
			return rep, err
		}
		if !optedIn {
			continue // explicit in_app opt-out for evidence_staleness
		}

		// Per-control alerts: only the stale band fires an alert (approaching
		// is digest-only — the early-warning is summarized, not per-control
		// pinged, to keep alert volume honest).
		for _, c := range classifiedRows {
			if c.band != BandStale {
				continue
			}
			dedup := fmt.Sprintf("alert:%s:%s:%s", c.item.ControlID, c.band.String(), period)
			wrote, err := s.writeIfClaimed(ctx, q, tenantID, recipient, "staleness_alert", dedup, s.alertPayload(c.item))
			if err != nil {
				return rep, err
			}
			if wrote {
				rep.AlertsWritten++
			} else {
				rep.AlertsDeduped++
			}
		}

		// Weekly digest: one per recipient per ISO-week.
		if weekly && (rep.StaleControls > 0 || rep.ApprControls > 0) {
			dedup := fmt.Sprintf("digest:%s", week)
			items := make([]DigestItem, 0, len(classifiedRows))
			for _, c := range classifiedRows {
				items = append(items, c.item)
			}
			payload := BuildDigestPayload(items, rep.StaleControls, rep.ApprControls, periodStart, periodEnd, FreshnessViewPath)
			raw, err := json.Marshal(payload)
			if err != nil {
				return rep, fmt.Errorf("staleness: marshal digest: %w", err)
			}
			wrote, err := s.writeIfClaimedRaw(ctx, q, tenantID, recipient, "staleness_digest", dedup, raw)
			if err != nil {
				return rep, err
			}
			if wrote {
				rep.DigestsWritten++
			} else {
				rep.DigestsDeduped++
			}
		}
	}

	if err := tx.Commit(ctx); err != nil {
		return rep, fmt.Errorf("staleness: commit: %w", err)
	}
	return rep, nil
}

// inAppEnabled reads the user's in_app preference for the evidence_staleness
// event. Default-on-missing-row (slice-108 D3): no row means enabled. An
// explicit enabled=false row is the AC-7 opt-out.
func (s *Store) inAppEnabled(ctx context.Context, q *dbx.Queries, tenantID uuid.UUID, userID pgtype.UUID) (bool, error) {
	rows, err := q.ListUserNotificationPreferences(ctx, dbx.ListUserNotificationPreferencesParams{
		TenantID: pgUUID(tenantID),
		UserID:   userID,
	})
	if err != nil {
		return false, fmt.Errorf("staleness: read prefs: %w", err)
	}
	for _, r := range rows {
		if r.Event == "evidence_staleness" && r.Channel == "in_app" {
			return r.Enabled, nil
		}
	}
	return true, nil // default-on
}

// writeIfClaimed claims the dedup key, and on a fresh claim writes the
// notification with the given typed payload. Returns whether a row was
// written (false = deduped). Idempotency (AC-5/AC-12): the claim's ON CONFLICT
// DO NOTHING makes a re-run a no-op.
func (s *Store) writeIfClaimed(ctx context.Context, q *dbx.Queries, tenantID uuid.UUID, recipient, kind, dedup string, payload any) (bool, error) {
	raw, err := json.Marshal(payload)
	if err != nil {
		return false, fmt.Errorf("staleness: marshal payload: %w", err)
	}
	return s.writeIfClaimedRaw(ctx, q, tenantID, recipient, kind, dedup, raw)
}

func (s *Store) writeIfClaimedRaw(ctx context.Context, q *dbx.Queries, tenantID uuid.UUID, recipient, kind, dedup string, raw []byte) (bool, error) {
	claimID, err := q.ClaimStalenessRollup(ctx, dbx.ClaimStalenessRollupParams{
		ID:              pgUUID(uuid.New()),
		TenantID:        pgUUID(tenantID),
		RecipientUserID: recipient,
		Kind:            kind,
		DedupKey:        dedup,
		NotificationID:  pgtype.UUID{}, // backfilled below on a fresh claim
	})
	if err != nil {
		if err == pgx.ErrNoRows {
			return false, nil // already delivered this logical event — dedup
		}
		return false, fmt.Errorf("staleness: claim %s: %w", kind, err)
	}
	_ = claimID
	// Fresh claim: write the notification row. The notification type is the
	// load-bearing evidence.staleness string the delivery channels consume.
	if _, err := q.CreateNotification(ctx, dbx.CreateNotificationParams{
		ID:              pgUUID(uuid.New()),
		TenantID:        pgUUID(tenantID),
		RecipientUserID: recipient,
		Type:            notifications.TypeEvidenceStaleness,
		Payload:         raw,
	}); err != nil {
		return false, fmt.Errorf("staleness: write notification: %w", err)
	}
	return true, nil
}

func (s *Store) alertPayload(item DigestItem) AlertPayload {
	return AlertPayload{
		Subtype:          "alert",
		ControlID:        item.ControlID,
		FreshnessClass:   item.FreshnessClass,
		Band:             BandStale.String(),
		RecomputeMessage: fmt.Sprintf("Recomputed %s.", RecomputeIntervalText),
		Message:          alertMessage(item.FreshnessClass),
		FreshnessViewURL: FreshnessViewPath,
	}
}

// recomputePeriodKey buckets `now` into the recompute window so a control that
// stays stale across multiple ticks within one window is alerted ONCE. The
// bucket is the UTC truncation to DefaultRecomputeInterval.
func recomputePeriodKey(now time.Time) string {
	bucket := now.UTC().Truncate(DefaultRecomputeInterval)
	return bucket.Format("20060102T1504Z")
}

// isoWeekKey is the digest idempotency bucket: one digest per ISO-week.
func isoWeekKey(now time.Time) string {
	y, w := now.UTC().ISOWeek()
	return fmt.Sprintf("%04d-W%02d", y, w)
}

// isoWeekBounds returns the [Monday 00:00, next-Monday 00:00) UTC bounds of
// the ISO week containing `now` — the period the digest states it covers.
func isoWeekBounds(now time.Time) (time.Time, time.Time) {
	u := now.UTC()
	// Go's Weekday: Sunday=0..Saturday=6. ISO weeks start Monday.
	offset := (int(u.Weekday()) + 6) % 7 // days since Monday
	monday := time.Date(u.Year(), u.Month(), u.Day(), 0, 0, 0, 0, time.UTC).AddDate(0, 0, -offset)
	return monday, monday.AddDate(0, 0, 7)
}

func tenantIDFromCtx(ctx context.Context) (uuid.UUID, error) {
	tenantStr, err := tenancy.TenantFromContext(ctx)
	if err != nil {
		return uuid.Nil, err
	}
	id, err := uuid.Parse(tenantStr)
	if err != nil {
		return uuid.Nil, fmt.Errorf("staleness: parse tenant id: %w", err)
	}
	return id, nil
}

func pgUUID(u uuid.UUID) pgtype.UUID {
	return pgtype.UUID{Bytes: u, Valid: true}
}

// freshnessMaxAgeProbe keeps the eval dependency referenced for the package
// doc's invariant note (the threshold is owned by eval.FreshnessMaxAge, used
// transitively via the freshness read model's valid_until). Compile-time only.
var _ = eval.FreshnessMaxAge
