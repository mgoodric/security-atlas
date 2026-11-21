//go:build integration

// #2924 fold-in: an audit period frozen, or a population created, before the
// routing cutover (evidence_routing_cutover, set by migration 20261290000000)
// keeps reading exactly what it read before routing existed. A control-free
// record of a routed kind (a pre-change connector row stored under
// control_ref 'scf:TDA-06') is invisible to it; a record bound to the
// control by id is not. A period frozen after the cutover, and a population
// created after it, see routed records.

package ingest_test

import (
	"context"
	"encoding/json"
	"slices"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/mgoodric/security-atlas/internal/audit"
	"github.com/mgoodric/security-atlas/internal/audit/period"
	"github.com/mgoodric/security-atlas/internal/dbtest"
	"github.com/mgoodric/security-atlas/internal/evidencesummary"
	"github.com/mgoodric/security-atlas/internal/tenancy"
)

func routingCutover(t *testing.T, admin *pgxpool.Pool) time.Time {
	t.Helper()
	var cut time.Time
	if err := admin.QueryRow(context.Background(),
		`SELECT cut_at FROM evidence_routing_cutover WHERE singleton`).Scan(&cut); err != nil {
		t.Fatalf("read routing cutover: %v", err)
	}
	return cut
}

// insertRepoProtection writes one github.repo_protection.v1 ledger row as the
// owner role, the way a row stored before this change looks: bound to ctrl
// when ctrl is valid, else control-free under control_ref 'scf:TDA-06'.
func insertRepoProtection(t *testing.T, admin *pgxpool.Pool, tenant string, ctrl *uuid.UUID, observed time.Time) uuid.UUID {
	t.Helper()
	body, _ := json.Marshal(map[string]any{
		"repo_full_name": "acme/api", "default_branch": "main", "required_reviews": 1,
		"require_code_owner_reviews": false, "require_signed_commits": false,
		"require_linear_history": false, "enforce_admins": true,
	})
	id := uuid.New()
	var controlID any
	ref := "scf:TDA-06"
	if ctrl != nil {
		controlID, ref = *ctrl, ctrl.String()
	}
	if _, err := admin.Exec(context.Background(), `
		INSERT INTO evidence_records (
			id, tenant_id, control_id, evidence_kind, observed_at, ingested_at,
			provenance, result, payload, hash, control_ref
		)
		VALUES ($1, $2, $3, 'github.repo_protection.v1', $4, now(), '{}'::jsonb, 'pass', $5::jsonb, $6, $7)
	`, id, tenant, controlID, observed, string(body), "hash-pin-"+id.String()[:8], ref); err != nil {
		t.Fatalf("insert repo protection row: %v", err)
	}
	return id
}

func pinFrameworkVersion(t *testing.T, admin *pgxpool.Pool) uuid.UUID {
	t.Helper()
	fwID, versionID := uuid.New(), uuid.New()
	if _, err := admin.Exec(context.Background(), `
		INSERT INTO frameworks (id, tenant_id, name, slug, issuer)
		VALUES ($1, NULL, 'Issue 2924 pin framework', $2, 'test')
	`, fwID, "issue2924-"+fwID.String()[:8]); err != nil {
		t.Fatalf("seed framework: %v", err)
	}
	if _, err := admin.Exec(context.Background(), `
		INSERT INTO framework_versions (id, tenant_id, framework_id, version)
		VALUES ($1, NULL, $2, '1.0')
	`, versionID, fwID); err != nil {
		t.Fatalf("seed framework_version: %v", err)
	}
	t.Cleanup(func() {
		ctx := context.Background()
		_, _ = admin.Exec(ctx, `DELETE FROM framework_versions WHERE id = $1`, versionID)
		_, _ = admin.Exec(ctx, `DELETE FROM frameworks WHERE id = $1`, fwID)
	})
	return versionID
}

func observationIDs(obs []period.ControlStateObservation) []uuid.UUID {
	out := make([]uuid.UUID, len(obs))
	for i, o := range obs {
		out[i] = o.EvidenceRecordID
	}
	slices.SortFunc(out, func(a, b uuid.UUID) int { return slices.Compare(a[:], b[:]) })
	return out
}

func sortedIDs(ids ...uuid.UUID) []uuid.UUID {
	out := slices.Clone(ids)
	slices.SortFunc(out, func(a, b uuid.UUID) int { return slices.Compare(a[:], b[:]) })
	return out
}

func TestRouted2924_PeriodsAndPopulationsPinnedBeforeCutover(t *testing.T) {
	_, app, _ := boot(t)
	admin := dbtest.NewMigratePool(t)
	ctx, err := tenancy.WithTenant(context.Background(), tenantA)
	if err != nil {
		t.Fatalf("WithTenant: %v", err)
	}
	cut := routingCutover(t, admin)
	ctrl := seedRoutedStockControl(t, admin, tenantA, cc81Bundles[0])
	// Registered after the control's cleanup, so it runs first: the period
	// rows and the bound ledger row reference the control. The ledger is
	// append-only, so it is truncated the way boot() resets it.
	t.Cleanup(func() {
		for _, stmt := range []string{
			`DELETE FROM sample_audit_log WHERE tenant_id = $1`,
			`DELETE FROM sample_evidence WHERE tenant_id = $1`,
			`DELETE FROM samples WHERE tenant_id = $1`,
			`DELETE FROM populations WHERE tenant_id = $1`,
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
	audits := audit.NewStore(app)
	periods := period.NewStore(app)
	summaries := evidencesummary.NewPeriodStore(app)

	// A record bound to the control by id, observed before the cutover.
	bound := insertRepoProtection(t, admin, tenantA, &ctrl, cut.Add(-2*time.Hour))

	newPeriod := func(name string) period.Period {
		t.Helper()
		p, err := periods.Create(ctx, period.CreateInput{
			Name:               name,
			FrameworkVersionID: fw,
			PeriodStart:        cut.Add(-24 * time.Hour),
			PeriodEnd:          time.Now().Add(24 * time.Hour),
			CreatedBy:          "key_test_2924",
		})
		if err != nil {
			t.Fatalf("create period %s: %v", name, err)
		}
		return p
	}
	newPopulation := func() audit.Population {
		t.Helper()
		pop, err := audits.CreatePopulation(ctx, audit.CreatePopulationInput{
			ControlID:       ctrl,
			ScopePredicate:  []byte(`{}`),
			TimeWindowStart: cut.Add(-3 * time.Hour),
			TimeWindowEnd:   time.Now().Add(time.Hour),
			CreatedBy:       "key_test_2924",
		})
		if err != nil {
			t.Fatalf("create population: %v", err)
		}
		return pop
	}
	draw := func(pop audit.Population, seed string) []uuid.UUID {
		t.Helper()
		smp, err := audits.DrawSample(ctx, audit.DrawSampleInput{
			PopulationID: pop.ID, N: 5, Seed: seed, CreatedBy: "key_test_2924",
		})
		if err != nil {
			t.Fatalf("draw sample: %v", err)
		}
		return smp.EvidenceRecordIDs
	}

	// The old world: a population created, and a period frozen, before the
	// cutover. Created now, then backdated, since no code older than the
	// cutover can run here.
	oldPop := newPopulation()
	if oldPop.RowCount != 1 {
		t.Fatalf("old population row_count = %d, want 1 (the bound record)", oldPop.RowCount)
	}
	if _, err := admin.Exec(context.Background(),
		`UPDATE populations SET created_at = $2 WHERE id = $1`, oldPop.ID, cut.Add(-time.Hour)); err != nil {
		t.Fatalf("backdate population: %v", err)
	}
	oldSample := draw(oldPop, "seed-2924")
	oldPeriod := newPeriod("FY25 frozen before routing")
	if err := periods.AttachPopulation(ctx, oldPeriod.ID, oldPop.ID, "key_test_2924"); err != nil {
		t.Fatalf("attach old population: %v", err)
	}
	if _, err := periods.Freeze(ctx, oldPeriod.ID, "key_test_2924", cut.Add(-30*time.Minute)); err != nil {
		t.Fatalf("freeze old period: %v", err)
	}
	oldState, err := periods.ControlState(ctx, oldPeriod.ID, ctrl)
	if err != nil {
		t.Fatalf("old period control state: %v", err)
	}
	if got := observationIDs(oldState); !slices.Equal(got, []uuid.UUID{bound}) {
		t.Fatalf("old period state before routed row = %v, want [%s]", got, bound)
	}

	// A control-free connector row stored before this change, inside every
	// window and horizon above: only the pin keeps it out of the old reads.
	routedRow := insertRepoProtection(t, admin, tenantA, nil, cut.Add(-90*time.Minute))

	// The period frozen before the cutover is unchanged.
	state, err := periods.ControlState(ctx, oldPeriod.ID, ctrl)
	if err != nil {
		t.Fatalf("old period control state: %v", err)
	}
	if got := observationIDs(state); !slices.Equal(got, observationIDs(oldState)) {
		t.Errorf("old period state = %v, want unchanged %v", got, observationIDs(oldState))
	}
	oldSet, err := summaries.PeriodEvidenceSet(ctx, ctrl, oldPeriod.ID)
	if err != nil {
		t.Fatalf("old period evidence set: %v", err)
	}
	if oldSet.TotalCount != 1 {
		t.Errorf("old period evidence-set total = %d, want 1", oldSet.TotalCount)
	}
	if _, ok, err := summaries.ResolveBeforeHorizon(ctx, routedRow, ctrl, cut.Add(-30*time.Minute)); err != nil || ok {
		t.Errorf("old period resolves routed row as citable: ok=%v err=%v, want false", ok, err)
	}

	// The old population's re-draw: same row count, same same-seed sample.
	if got := draw(oldPop, "seed-2924"); !slices.Equal(got, oldSample) {
		t.Errorf("old population re-draw = %v, want the original %v", got, oldSample)
	}
	if got := draw(oldPop, "seed-2924"); len(got) != int(oldPop.RowCount) {
		t.Errorf("old population re-draw size = %d, want row_count %d", len(got), oldPop.RowCount)
	}

	// The new world: a population created and a period frozen after the
	// cutover see the routed row.
	newPop := newPopulation()
	if newPop.RowCount != 2 {
		t.Errorf("new population row_count = %d, want 2 (bound + routed)", newPop.RowCount)
	}
	if got := sortedIDs(draw(newPop, "seed-2924")...); !slices.Equal(got, sortedIDs(bound, routedRow)) {
		t.Errorf("new population draw = %v, want both records", got)
	}
	nowPeriod := newPeriod("FY26 frozen after routing")
	if err := periods.AttachPopulation(ctx, nowPeriod.ID, newPop.ID, "key_test_2924"); err != nil {
		t.Fatalf("attach new population: %v", err)
	}
	if _, err := periods.Freeze(ctx, nowPeriod.ID, "key_test_2924", time.Now().UTC()); err != nil {
		t.Fatalf("freeze new period: %v", err)
	}
	newState, err := periods.ControlState(ctx, nowPeriod.ID, ctrl)
	if err != nil {
		t.Fatalf("new period control state: %v", err)
	}
	if got := observationIDs(newState); !slices.Equal(got, sortedIDs(bound, routedRow)) {
		t.Errorf("new period state = %v, want both records", got)
	}
	newSet, err := summaries.PeriodEvidenceSet(ctx, ctrl, nowPeriod.ID)
	if err != nil {
		t.Fatalf("new period evidence set: %v", err)
	}
	if newSet.TotalCount != 2 {
		t.Errorf("new period evidence-set total = %d, want 2", newSet.TotalCount)
	}

	// An auditor scoped to a period opens the routed row only under the
	// period whose population holds it.
	scopeOf, ok, err := audits.EvidencePeriods(ctx, routedRow)
	if err != nil || !ok {
		t.Fatalf("evidence periods for routed row: ok=%v err=%v", ok, err)
	}
	if !slices.Equal(scopeOf.Periods, []uuid.UUID{nowPeriod.ID}) {
		t.Errorf("routed row's periods = %v, want only [%s] (not the pinned %s)", scopeOf.Periods, nowPeriod.ID, oldPeriod.ID)
	}
	boundScope, _, err := audits.EvidencePeriods(ctx, bound)
	if err != nil {
		t.Fatalf("evidence periods for bound row: %v", err)
	}
	if got := sortedIDs(boundScope.Periods...); !slices.Equal(got, sortedIDs(oldPeriod.ID, nowPeriod.ID)) {
		t.Errorf("bound row's periods = %v, want both periods", got)
	}
}
