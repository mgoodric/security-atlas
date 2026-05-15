// Package drift is the CONTROL DRIFT read model of the security-atlas
// evidence pipeline (slice 016). It is a read-only consumer of the
// append-only control evaluation ledger (`control_evaluations`, slice 012):
// it reads the per-(control, scope_cell) evaluation history, rolls it up to a
// per-control pass/fail signal, captures a daily snapshot of the passing-
// control set into the append-only `control_drift_snapshots` ledger, and
// derives the signed drift delta `controls_passing_today -
// controls_passing_yesterday` (canvas §7.1).
//
// Constitutional invariant #2 — ingestion and evaluation are separated
// stages — is enforced structurally: this package's only writer
// (Store.insertSnapshot) has exactly one INSERT target,
// `control_drift_snapshots`. It imports no ledger write path. It NEVER writes
// `control_evaluations` or `evidence_records`.
//
// The drift definition (a canvas-interpretation judgment call, recorded in
// docs/audit-log/016-evidence-freshness-drift-decisions.md):
//
//   - A control "passes" on a calendar day iff EVERY applicable
//     (control, scope_cell) tuple's LATEST evaluation that day has
//     result='pass' AND freshness_status='fresh' — a worst-cell rollup.
//   - "Passing" EXCLUDES stale evidence: canvas §2.3 says "stale evidence
//     drives a drift signal", so a control whose evidence decayed out of its
//     window is drifting even though nothing flipped to fail. This is what
//     makes drift a LEADING indicator.
//   - delta = passing(today) - passing(yesterday), signed.
//
// The snapshot ledger is append-only and DB-backed — anti-criterion P0-3
// (the drift signal persists across restarts) holds by construction: the
// signal lives in Postgres, never in process memory.
package drift

import (
	"context"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/mgoodric/security-atlas/internal/db/dbx"
	"github.com/mgoodric/security-atlas/internal/tenancy"
)

// Trigger values mirror the control_drift_snapshots.trigger CHECK vocabulary.
const (
	TriggerScheduled = "scheduled"
	TriggerIngest    = "ingest"
	TriggerManual    = "manual"
)

// Store is the drift read model's database access layer. Every method opens a
// transaction, applies the tenant GUC via internal/tenancy, and runs queries
// inside that transaction so RLS policies see the tenant id.
type Store struct {
	pool *pgxpool.Pool
	// now is the wall-clock source. Overridable in tests so the snapshot date
	// and the "as-of" horizon are deterministic.
	now func() time.Time
}

// NewStore wires a Store over the application pgx pool. The pool must be
// connected as the application role (NOSUPERUSER NOBYPASSRLS) so RLS is
// actually enforced.
func NewStore(pool *pgxpool.Pool) *Store {
	return &Store{pool: pool, now: func() time.Time { return time.Now().UTC() }}
}

// Snapshot is one day's captured drift state.
type Snapshot struct {
	SnapshotDate      time.Time // UTC calendar day (date-only semantics)
	ControlsPassing   int
	PassingControlIDs []uuid.UUID
	CapturedAt        time.Time
	Trigger           string
}

// DriftRow is one control that flipped OUT of passing between two
// consecutive daily snapshots — the AC-3 response shape.
type DriftRow struct {
	ControlID     uuid.UUID
	LastPassing   time.Time // the snapshot_date this control was last seen passing
	CurrentResult string    // "fail" | "stale" | "not_passing" — why it is no longer passing
}

// DriftReport is the AC-3 endpoint payload: the signed delta over the window
// plus the controls that flipped out of passing.
type DriftReport struct {
	SinceDate    time.Time
	ThroughDate  time.Time
	Delta        int // controls_passing(latest) - controls_passing(earliest in window)
	FlippedToOut []DriftRow
	Snapshots    []Snapshot
}

// CaptureSnapshot computes and APPENDS one drift snapshot for the tenant in
// ctx, for the calendar day of `s.now()`. The passing-control set is the
// worst-cell rollup over control_evaluations as of now. Returns the captured
// snapshot. Pure read of the evaluation ledger + append of the snapshot
// ledger — invariant #2.
func (s *Store) CaptureSnapshot(ctx context.Context, trigger string) (Snapshot, error) {
	now := s.now()
	var snap Snapshot
	err := s.inTx(ctx, func(ctx context.Context, q *dbx.Queries, tenantID uuid.UUID) error {
		passing, err := q.ListPassingControlsForDay(ctx, dbx.ListPassingControlsForDayParams{
			TenantID:    pgUUID(tenantID),
			EvaluatedAt: pgTimestamptz(now),
		})
		if err != nil {
			return fmt.Errorf("list passing controls: %w", err)
		}
		ids := make([]uuid.UUID, 0, len(passing))
		pgIDs := make([]pgtype.UUID, 0, len(passing))
		for _, p := range passing {
			ids = append(ids, uuid.UUID(p.Bytes))
			pgIDs = append(pgIDs, p)
		}
		row, err := q.InsertDriftSnapshot(ctx, dbx.InsertDriftSnapshotParams{
			ID:                pgUUID(uuid.New()),
			TenantID:          pgUUID(tenantID),
			SnapshotDate:      pgDate(now),
			ControlsPassing:   int32(len(ids)),
			PassingControlIds: pgIDs,
			Trigger:           trigger,
		})
		if err != nil {
			return fmt.Errorf("insert drift snapshot: %w", err)
		}
		snap = snapshotFromRow(row)
		return nil
	})
	if err != nil {
		return Snapshot{}, err
	}
	return snap, nil
}

// Report builds the AC-3 drift report for the tenant in ctx over the window
// [now-since, now]. It reads the latest snapshot per calendar day, computes
// the signed delta between the earliest and latest snapshot in the window,
// and diffs consecutive days to find controls that flipped OUT of passing.
//
// `since` is a positive duration (e.g. 7 days). The window's lower bound is
// computed in Go (today - sinceDays) and passed to the query explicitly so
// pgx never has to infer the type of a bare placeholder (SQLSTATE 42P08).
func (s *Store) Report(ctx context.Context, since time.Duration) (DriftReport, error) {
	now := s.now()
	sinceDays := int(since.Hours() / 24)
	if sinceDays < 1 {
		sinceDays = 1
	}
	sinceDate := now.AddDate(0, 0, -sinceDays)

	var report DriftReport
	report.SinceDate = truncDate(sinceDate)
	report.ThroughDate = truncDate(now)

	err := s.inTx(ctx, func(ctx context.Context, q *dbx.Queries, tenantID uuid.UUID) error {
		rows, err := q.ListLatestDriftSnapshotsSince(ctx, dbx.ListLatestDriftSnapshotsSinceParams{
			TenantID:     pgUUID(tenantID),
			SnapshotDate: pgDate(sinceDate),
		})
		if err != nil {
			return fmt.Errorf("list drift snapshots: %w", err)
		}
		// rows are snapshot_date DESC. Build the chronological slice.
		snaps := make([]Snapshot, 0, len(rows))
		for i := len(rows) - 1; i >= 0; i-- {
			snaps = append(snaps, snapshotFromRow(rows[i]))
		}
		report.Snapshots = snaps
		report.Delta = computeDelta(snaps)
		report.FlippedToOut = computeFlips(snaps)
		return nil
	})
	if err != nil {
		return DriftReport{}, err
	}
	return report, nil
}

// computeDelta is the canvas §7.1 signed drift count over the window:
// controls_passing(latest snapshot) - controls_passing(earliest snapshot).
// A negative delta means controls drifted OUT of passing — the leading
// warning sign. Zero snapshots -> zero delta.
func computeDelta(snaps []Snapshot) int {
	if len(snaps) == 0 {
		return 0
	}
	earliest := snaps[0]
	latest := snaps[len(snaps)-1]
	return latest.ControlsPassing - earliest.ControlsPassing
}

// computeFlips diffs consecutive daily snapshots and returns every control
// that was passing on some day in the window and is NOT passing on the latest
// snapshot — the controls that drifted out. LastPassing is the most recent
// snapshot_date the control appeared in the passing set. CurrentResult is
// "not_passing": the snapshot ledger records only set membership, so it knows
// the control left the passing set but not WHY (fail vs stale). A future
// slice can enrich this from the live evaluation state if the dashboard needs
// the distinction.
func computeFlips(snaps []Snapshot) []DriftRow {
	if len(snaps) < 2 {
		return nil
	}
	latest := snaps[len(snaps)-1]
	latestPassing := make(map[uuid.UUID]bool, len(latest.PassingControlIDs))
	for _, id := range latest.PassingControlIDs {
		latestPassing[id] = true
	}
	// For every control ever-passing in the window but not passing latest,
	// record the most recent day it WAS passing.
	lastPassingDay := make(map[uuid.UUID]time.Time)
	for _, snap := range snaps {
		for _, id := range snap.PassingControlIDs {
			if !latestPassing[id] {
				if snap.SnapshotDate.After(lastPassingDay[id]) {
					lastPassingDay[id] = snap.SnapshotDate
				}
			}
		}
	}
	out := make([]DriftRow, 0, len(lastPassingDay))
	for id, day := range lastPassingDay {
		out = append(out, DriftRow{
			ControlID:     id,
			LastPassing:   day,
			CurrentResult: "not_passing",
		})
	}
	return out
}

func snapshotFromRow(r dbx.ControlDriftSnapshot) Snapshot {
	snap := Snapshot{
		ControlsPassing: int(r.ControlsPassing),
		Trigger:         r.Trigger,
	}
	if r.SnapshotDate.Valid {
		snap.SnapshotDate = r.SnapshotDate.Time
	}
	if r.CapturedAt.Valid {
		snap.CapturedAt = r.CapturedAt.Time
	}
	snap.PassingControlIDs = make([]uuid.UUID, 0, len(r.PassingControlIds))
	for _, id := range r.PassingControlIds {
		snap.PassingControlIDs = append(snap.PassingControlIDs, uuid.UUID(id.Bytes))
	}
	return snap
}

// inTx opens a transaction, applies the tenant GUC, runs fn, commits on
// success. Mirrors eval.Store.inTx / freshness.Store.inTx.
func (s *Store) inTx(ctx context.Context, fn func(context.Context, *dbx.Queries, uuid.UUID) error) error {
	tenantStr, err := tenancy.TenantFromContext(ctx)
	if err != nil {
		return err
	}
	tenantID, err := uuid.Parse(tenantStr)
	if err != nil {
		return fmt.Errorf("drift: parse tenant id: %w", err)
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("drift: begin tx: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	if err := tenancy.ApplyTenant(ctx, tx); err != nil {
		return err
	}
	q := dbx.New(tx)
	if err := fn(ctx, q, tenantID); err != nil {
		return err
	}
	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("drift: commit: %w", err)
	}
	return nil
}

func truncDate(t time.Time) time.Time {
	return time.Date(t.Year(), t.Month(), t.Day(), 0, 0, 0, 0, time.UTC)
}

func pgUUID(u uuid.UUID) pgtype.UUID {
	return pgtype.UUID{Bytes: u, Valid: true}
}

func pgTimestamptz(t time.Time) pgtype.Timestamptz {
	if t.IsZero() {
		return pgtype.Timestamptz{}
	}
	return pgtype.Timestamptz{Time: t.UTC(), Valid: true}
}

func pgDate(t time.Time) pgtype.Date {
	if t.IsZero() {
		return pgtype.Date{}
	}
	return pgtype.Date{Time: truncDate(t), Valid: true}
}
