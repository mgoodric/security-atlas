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

	"github.com/mgoodric/security-atlas/internal/audit/period"
	"github.com/mgoodric/security-atlas/internal/db/dbx"
	"github.com/mgoodric/security-atlas/internal/dbtest"
	"github.com/mgoodric/security-atlas/internal/evidence/routed"
	"github.com/mgoodric/security-atlas/internal/evidencesummary"
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

// insertLegacyRefRow copies the ledger row src as a new row of kind (a bound,
// non-routed kind, or NULL: evidence_kind is nullable) with control_id NULL
// and control_ref = ref: the shape a control_ref-only record has. It is
// inserted as the owner; readers then see it through RLS as the app role.
func insertLegacyRefRow(t *testing.T, admin *pgxpool.Pool, src uuid.UUID, ref string, kind *string) uuid.UUID {
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
		        evidence_kind = $3, idempotency_key = $4`,
			[]any{id, ref, kind, "legacy-2931-" + id.String()}},
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

	// A control_ref-only row of a bound kind, or of no kind, still reaches
	// the control it names: the control_ref leg is closed only for routed
	// kinds. The NULL kind is the case `evidence_kind <> ALL(...)` alone gets
	// wrong (NULL, so the leg would close).
	boundKind := "test.bound_kind_2931.v1"
	legacy := map[uuid.UUID]bool{}
	for _, kind := range []*string{&boundKind, nil} {
		legacy[insertLegacyRefRow(t, admin, rec, undeclared.String(), kind)] = true
	}
	for name, n := range readerCounts(t, app, tenantA, undeclared, legacy) {
		want := int64(2)
		if name == "checklist.ListInScopeControlsForChecklist" {
			want = 1 // has_evidence is a boolean
		}
		if n != want {
			t.Errorf("bound-kind and NULL-kind control_ref rows: %s = %d, want %d", name, n, want)
		}
	}
}

// #2931 review: a period frozen before the routing cutover keeps reading
// what it read then. Before routing shipped, a control-free row of a
// now-routed kind whose control_ref names the control reached it through the
// control_ref leg. The pinned read binds no routed kinds, so that leg stays
// open: the frozen evidence set still lists, counts and cites the row. The
// pinned read still keeps out the control-free row it never had (scf: ref).
func TestRouted2931_PeriodFrozenBeforeCutoverKeepsRefOnlyRoutedRow(t *testing.T) {
	_, app, _ := boot(t)
	admin := dbtest.NewMigratePool(t)
	ctx, err := tenancy.WithTenant(context.Background(), tenantA)
	if err != nil {
		t.Fatalf("WithTenant: %v", err)
	}
	cut := routingCutover(t, admin)
	ctrl := seedRoutedStockControl(t, admin, tenantA, cc81Bundles[0])
	// Registered after the control's cleanup, so it runs first; the ledger is
	// append-only, so it is truncated the way boot() resets it.
	t.Cleanup(func() {
		for _, stmt := range []string{
			`DELETE FROM audit_period_audit_log WHERE tenant_id = $1`,
			`DELETE FROM audit_periods WHERE tenant_id = $1`,
		} {
			if _, err := admin.Exec(context.Background(), stmt, tenantA); err != nil {
				t.Logf("cleanup %s: %v", stmt, err)
			}
		}
		if _, err := admin.Exec(context.Background(), `TRUNCATE evidence_records CASCADE`); err != nil {
			t.Logf("cleanup truncate evidence_records: %v", err)
		}
	})
	fw := pinFrameworkVersion(t, admin)
	periods := period.NewStore(app)
	summaries := evidencesummary.NewPeriodStore(app)

	frozenAt := cut.Add(-30 * time.Minute)
	p, err := periods.Create(ctx, period.CreateInput{
		Name:               "FY25 frozen before routing (#2931)",
		FrameworkVersionID: fw,
		PeriodStart:        cut.Add(-24 * time.Hour),
		PeriodEnd:          time.Now().Add(24 * time.Hour),
		CreatedBy:          "key_test_2931",
	})
	if err != nil {
		t.Fatalf("create period: %v", err)
	}
	if _, err := periods.Freeze(ctx, p.ID, "key_test_2931", frozenAt); err != nil {
		t.Fatalf("freeze period: %v", err)
	}

	// Both rows observed before the horizon. scfRow is control-free under
	// 'scf:TDA-06'; refRow is the same kind under the control's UUID.
	scfRow := insertRepoProtection(t, admin, tenantA, nil, cut.Add(-90*time.Minute))
	routedKind := "github.repo_protection.v2"
	refRow := insertLegacyRefRow(t, admin, scfRow, ctrl.String(), &routedKind)

	set, err := summaries.PeriodEvidenceSet(ctx, ctrl, p.ID)
	if err != nil {
		t.Fatalf("period evidence set: %v", err)
	}
	var listed []uuid.UUID
	for _, r := range set.Records {
		listed = append(listed, r.EvidenceID)
	}
	if set.TotalCount != 1 || len(listed) != 1 || listed[0] != refRow {
		t.Errorf("pinned period evidence set = %v (total %d), want only the ref-only row %s", listed, set.TotalCount, refRow)
	}
	if _, ok, err := summaries.ResolveBeforeHorizon(ctx, refRow, ctrl, frozenAt); err != nil || !ok {
		t.Errorf("pinned period resolves the ref-only routed row: ok=%v err=%v, want true", ok, err)
	}
	if _, ok, err := summaries.ResolveBeforeHorizon(ctx, scfRow, ctrl, frozenAt); err != nil || ok {
		t.Errorf("pinned period resolves the scf: routed row: ok=%v err=%v, want false", ok, err)
	}
}
