//go:build integration

// #2931: a routed-kind record pushed with an explicit control UUID is stored
// with control_id NULL and control_ref = <uuid>. Every by-control reader
// must ignore that control_ref, so the record reaches a control only when the
// control's evidence queries declare its kind. Before the fix, the readers
// with a control_ref leg (evaluation, summaries, control detail, checklist,
// portfolio, freshness) read it for the named control regardless of kind.

package ingest_test

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/mgoodric/security-atlas/internal/db/dbx"
	"github.com/mgoodric/security-atlas/internal/dbtest"
	"github.com/mgoodric/security-atlas/internal/evidence/routed"
	"github.com/mgoodric/security-atlas/internal/tenancy"
)

// seedQuerylessControl inserts a control whose evidence queries declare no
// kind, so the only records any reader can resolve to it are ones naming it
// by control_id or control_ref.
func seedQuerylessControl(t *testing.T, admin *pgxpool.Pool, tenant string) uuid.UUID {
	t.Helper()
	id := uuid.New()
	if _, err := admin.Exec(context.Background(), `
		INSERT INTO controls (
			id, tenant_id, title, control_family, implementation_type,
			bundle_id, freshness_class, evidence_queries, applicability_expr
		)
		VALUES ($1, $2, 'queryless 2931', 'test', 'automated', $3, 'daily', '[]'::jsonb, 'true')
	`, id, tenant, "test_2931_"+id.String()[:8]); err != nil {
		t.Fatalf("seed queryless control: %v", err)
	}
	t.Cleanup(func() {
		_, _ = admin.Exec(context.Background(), `DELETE FROM control_evaluations WHERE control_id = $1`, id)
		_, _ = admin.Exec(context.Background(), `DELETE FROM controls WHERE id = $1`, id)
	})
	return id
}

// readerCounts runs every by-control ledger reader that has a control_ref
// leg, as tenant through the RLS-bound app role, and returns how many of the
// records in want each one resolves to ctrl. The aggregate readers
// (checklist, portfolio, freshness) report the control's whole evidence, so
// their count is only meaningful for a control with no other evidence.
func readerCounts(t *testing.T, app *pgxpool.Pool, tenant string, ctrl uuid.UUID, want map[uuid.UUID]bool) map[string]int64 {
	t.Helper()
	ctx, err := tenancy.WithTenant(context.Background(), tenant)
	if err != nil {
		t.Fatalf("WithTenant: %v", err)
	}
	tx, err := app.Begin(ctx)
	if err != nil {
		t.Fatalf("Begin: %v", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if err := tenancy.ApplyTenant(ctx, tx); err != nil {
		t.Fatalf("ApplyTenant: %v", err)
	}
	q := dbx.New(tx)

	tid := pgtype.UUID{Bytes: uuid.MustParse(tenant), Valid: true}
	cid := pgtype.UUID{Bytes: ctrl, Valid: true}
	ref := ctrl.String()
	free, rk := routed.ControlFreeKinds(), routed.Kinds()
	far := pgtype.Timestamptz{Time: time.Now().Add(24 * time.Hour), Valid: true}
	past := pgtype.Timestamptz{Time: time.Now().Add(-365 * 24 * time.Hour), Valid: true}
	maxID := pgtype.UUID{Bytes: uuid.Max, Valid: true}
	inf := pgtype.Timestamptz{InfinityModifier: pgtype.Infinity, Valid: true}

	got := map[string]int64{}
	match := func(name string, ids []pgtype.UUID) {
		for _, id := range ids {
			if want[uuid.UUID(id.Bytes)] {
				got[name]++
			}
		}
	}
	fail := func(name string, err error) {
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
	}

	asOf, err := q.ListEvidenceForControlAsOf(ctx, dbx.ListEvidenceForControlAsOfParams{
		TenantID: tid, ControlID: cid, ControlRef: ref,
		RoutedKinds: rk, UnboundKinds: free, ObservedAt: far,
	})
	fail("ListEvidenceForControlAsOf", err)
	ids := make([]pgtype.UUID, 0, len(asOf))
	for _, r := range asOf {
		ids = append(ids, r.ID)
	}
	match("eval.ListEvidenceForControlAsOf", ids)

	byCtrl, err := q.ListEvidenceRecordsByControl(ctx, dbx.ListEvidenceRecordsByControlParams{
		TenantID: tid, ControlID: cid, ControlRef: ref, Limit: 1000,
		RoutedKinds: rk, UnboundKinds: free,
	})
	fail("ListEvidenceRecordsByControl", err)
	ids = ids[:0]
	for _, r := range byCtrl {
		ids = append(ids, r.ID)
	}
	match("ListEvidenceRecordsByControl", ids)

	horizon, err := q.ListEvidenceRecordsByControlBeforeHorizon(ctx, dbx.ListEvidenceRecordsByControlBeforeHorizonParams{
		TenantID: tid, ControlID: cid, ControlRef: ref, Limit: 1000,
		RoutedKinds: rk, UnboundKinds: free, FrozenAt: far,
	})
	fail("ListEvidenceRecordsByControlBeforeHorizon", err)
	ids = ids[:0]
	for _, r := range horizon {
		ids = append(ids, r.ID)
	}
	match("ListEvidenceRecordsByControlBeforeHorizon", ids)

	paged, err := q.ListEvidenceForControlPaged(ctx, dbx.ListEvidenceForControlPagedParams{
		TenantID: tid, ControlID: cid, ControlRef: ref,
		ObservedAt: past, ObservedAt_2: far, IDOnly: false,
		RoutedKinds: rk, UnboundKinds: free,
		CursorTs: inf, CursorID: maxID, RowLimit: 1000,
	})
	fail("ListEvidenceForControlPaged", err)
	ids = ids[:0]
	for _, r := range paged {
		ids = append(ids, r.ID)
	}
	match("controldetail.ListEvidenceForControlPaged", ids)

	// The counts: compared against the matching list's total so a stray
	// record of another test cannot pass for one of ours.
	n, err := q.CountEvidenceRecordsByControl(ctx, dbx.CountEvidenceRecordsByControlParams{
		TenantID: tid, ControlID: cid, ControlRef: ref, RoutedKinds: rk, UnboundKinds: free,
	})
	fail("CountEvidenceRecordsByControl", err)
	got["CountEvidenceRecordsByControl"] = n
	n, err = q.CountEvidenceRecordsByControlBeforeHorizon(ctx, dbx.CountEvidenceRecordsByControlBeforeHorizonParams{
		TenantID: tid, ControlID: cid, ControlRef: ref, RoutedKinds: rk, UnboundKinds: free, FrozenAt: far,
	})
	fail("CountEvidenceRecordsByControlBeforeHorizon", err)
	got["CountEvidenceRecordsByControlBeforeHorizon"] = n
	n, err = q.CountEvidenceForControlWindow(ctx, dbx.CountEvidenceForControlWindowParams{
		TenantID: tid, ControlID: cid, ControlRef: ref,
		ObservedAt: past, ObservedAt_2: far, IDOnly: false,
		RoutedKinds: rk, UnboundKinds: free,
	})
	fail("CountEvidenceForControlWindow", err)
	got["controldetail.CountEvidenceForControlWindow"] = n

	checklist, err := q.ListInScopeControlsForChecklist(ctx, dbx.ListInScopeControlsForChecklistParams{
		TenantID: tid, RoutedKinds: rk, UnboundKinds: free,
	})
	fail("ListInScopeControlsForChecklist", err)
	got["checklist.ListInScopeControlsForChecklist"] = 0
	for _, r := range checklist {
		if r.ID == cid && r.HasEvidence {
			got["checklist.ListInScopeControlsForChecklist"] = 1
		}
	}

	portfolio, err := q.ListActiveControlsForPortfolio(ctx, dbx.ListActiveControlsForPortfolioParams{
		TenantID: tid, Limit: 100000, RoutedKinds: rk, UnboundKinds: free,
	})
	fail("ListActiveControlsForPortfolio", err)
	got["portfolio.ListActiveControlsForPortfolio"] = -1
	for _, r := range portfolio {
		if r.ID == cid {
			got["portfolio.ListActiveControlsForPortfolio"] = r.EvidenceCount
		}
	}

	fresh, err := q.ListControlsWithLatestEvidence(ctx, dbx.ListControlsWithLatestEvidenceParams{
		TenantID: tid, RoutedKinds: rk, UnboundKinds: free,
	})
	fail("ListControlsWithLatestEvidence", err)
	got["freshness.ListControlsWithLatestEvidence"] = -1
	for _, r := range fresh {
		if r.ControlID == cid {
			got["freshness.ListControlsWithLatestEvidence"] = r.EvidenceCount
		}
	}
	return got
}

// insertLegacyRefRow copies the ledger row src as a new row of a bound
// (non-routed) kind with control_id NULL and control_ref = ref: the shape a
// control_ref-only record has. It is inserted as the owner; readers then see
// it through RLS as the app role.
func insertLegacyRefRow(t *testing.T, admin *pgxpool.Pool, src uuid.UUID, ref string) uuid.UUID {
	t.Helper()
	ctx := context.Background()
	id := uuid.New()
	tx, err := admin.Begin(ctx)
	if err != nil {
		t.Fatalf("Begin: %v", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	for _, s := range []struct {
		sql  string
		args []any
	}{
		{`CREATE TEMP TABLE legacy_2931 ON COMMIT DROP AS SELECT * FROM evidence_records WHERE id = $1`,
			[]any{src}},
		{`UPDATE legacy_2931 SET id = $1, control_id = NULL, control_ref = $2,
		        evidence_kind = 'test.bound_kind_2931.v1', idempotency_key = $3`,
			[]any{id, ref, "legacy-2931-" + id.String()}},
		{`INSERT INTO evidence_records SELECT * FROM legacy_2931`, nil},
	} {
		if _, err := tx.Exec(ctx, s.sql, s.args...); err != nil {
			t.Fatalf("legacy row %q: %v", s.sql[:20], err)
		}
	}
	if err := tx.Commit(ctx); err != nil {
		t.Fatalf("Commit: %v", err)
	}
	return id
}

func TestRouted2931_ExplicitUUIDOnUndeclaredControlIsReadByNoReader(t *testing.T) {
	svc, app, _ := boot(t)
	admin := dbtest.NewMigratePool(t)

	undeclared := seedQuerylessControl(t, admin, tenantA)
	declares := seedRoutedStockControl(t, admin, tenantA, "soc2_cc8_1_change_management")

	receipt, _, err := svc.Process(context.Background(), repoProtectionRecord(t, undeclared.String()), mkCred(tenantA, nil, ""))
	if err != nil {
		t.Fatalf("Process: %v", err)
	}
	gotID, gotRef := ledgerControl(t, admin, receipt.RecordID)
	if gotID.Valid || gotRef != undeclared.String() {
		t.Fatalf("stored (control_id=%v, control_ref=%q), want (NULL, %q)", gotID, gotRef, undeclared)
	}
	rec := uuid.MustParse(receipt.RecordID)
	want := map[uuid.UUID]bool{rec: true}

	// The named control does not declare the kind: no reader resolves it.
	for name, n := range readerCounts(t, app, tenantA, undeclared, want) {
		if n != 0 {
			t.Errorf("undeclared control: %s = %d, want 0 (control_ref must not route a routed kind)", name, n)
		}
	}

	// A control that declares the kind reads it in every list reader.
	got := readerCounts(t, app, tenantA, declares, want)
	for _, name := range []string{
		"eval.ListEvidenceForControlAsOf",
		"ListEvidenceRecordsByControl",
		"ListEvidenceRecordsByControlBeforeHorizon",
		"controldetail.ListEvidenceForControlPaged",
	} {
		if got[name] != 1 {
			t.Errorf("declaring control: %s = %d, want 1", name, got[name])
		}
	}

	// A bound kind's control_ref-only row still reaches the control it
	// names: the control_ref leg is closed only for routed kinds.
	legacy := insertLegacyRefRow(t, admin, rec, undeclared.String())
	for name, n := range readerCounts(t, app, tenantA, undeclared, map[uuid.UUID]bool{legacy: true}) {
		if n != 1 {
			t.Errorf("bound-kind control_ref row: %s = %d, want 1", name, n)
		}
	}
}
