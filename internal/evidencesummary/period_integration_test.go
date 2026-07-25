//go:build integration

// Slice 749 — integration tests for the period-scoped AI evidence-summary
// surface. Real Postgres + the real evidencesummary.PeriodStore (RLS-backed
// FROZEN-population evidence-set assembly + horizon-bound citation resolution) +
// the real internal/audit/period.Store (to create + freeze a period) + the
// llm.StubClient (NO live Ollama in CI). The stub lets us craft a VALID draft
// (cites a real frozen-population id) or a VIOLATING draft (cites a post-freeze /
// cross-tenant id) and assert the deterministic frozen-population integrity +
// citation-validation + suppression behavior.
//
// Run with:
//
//	go test -tags=integration -p 1 ./internal/evidencesummary/...
//
// Requires DATABASE_URL_APP (atlas_app role) and DATABASE_URL (admin role).
//
// Coverage:
//
//	AC-1/AC-5  frozen-population integrity — a post-freeze (observed_at > frozen_at)
//	           record NEVER enters the summary OR its citable-id set (P0-749-1,
//	           invariant #10). THE headline test.
//	AC-2       a summary citing an unresolvable / non-frozen id is suppressed ->
//	           deterministic frozen list shown.
//	AC-3       cross-tenant isolation — a Tenant-B period summary cannot cite a
//	           Tenant-A record.
package evidencesummary_test

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	auditperiod "github.com/mgoodric/security-atlas/internal/audit/period"
	"github.com/mgoodric/security-atlas/internal/dbtest"
	"github.com/mgoodric/security-atlas/internal/evidencesummary"
	"github.com/mgoodric/security-atlas/internal/llm"
)

// ----- harness (period-scoped) -----

// freshPeriodTenant returns a fresh tenant + registers cleanup of the audit-period
// + ledger tables this slice's period tests touch, in FK order.
func freshPeriodTenant(t *testing.T, admin *pgxpool.Pool) string {
	t.Helper()
	tenant := uuid.NewString()
	t.Cleanup(func() {
		ctx := context.Background()
		for _, stmt := range []string{
			`DELETE FROM audit_period_audit_log WHERE tenant_id = $1`,
			`UPDATE populations SET audit_period_id = NULL, frozen_at = NULL WHERE tenant_id = $1`,
			`DELETE FROM populations WHERE tenant_id = $1`,
			`DELETE FROM audit_periods WHERE tenant_id = $1`,
			`DELETE FROM evidence_records WHERE tenant_id = $1`,
			`DELETE FROM controls WHERE tenant_id = $1`,
		} {
			if _, err := admin.Exec(ctx, stmt, tenant); err != nil {
				t.Logf("cleanup %s: %v", stmt, err)
			}
		}
	})
	return tenant
}

// seedFrameworkVersion seeds a global-catalog framework + framework_version so an
// audit_periods row can FK to it. Mirrors the slice-028 suite helper.
func seedFrameworkVersion(t *testing.T, admin *pgxpool.Pool) uuid.UUID {
	t.Helper()
	fwID := uuid.New()
	versionID := uuid.New()
	slug := fmt.Sprintf("slice749-%s", uuid.NewString()[:8])
	if _, err := admin.Exec(context.Background(), `
		INSERT INTO frameworks (id, tenant_id, name, slug, issuer)
		VALUES ($1, NULL, 'Slice 749 test framework', $2, 'test')
	`, fwID, slug); err != nil {
		t.Fatalf("seed framework: %v", err)
	}
	if _, err := admin.Exec(context.Background(), `
		INSERT INTO framework_versions (id, tenant_id, framework_id, version)
		VALUES ($1, NULL, $2, '1.0')
	`, versionID, fwID); err != nil {
		t.Fatalf("seed framework_version: %v", err)
	}
	return versionID
}

// seedFrozenPeriod creates an OPEN period via the real period.Store, then freezes
// it at frozenAt — exercising the real freeze path so the frozen_at horizon is
// stamped exactly as production does.
func seedFrozenPeriod(t *testing.T, app *pgxpool.Pool, tenant string, fwID uuid.UUID, frozenAt time.Time) uuid.UUID {
	t.Helper()
	store := auditperiod.NewStore(app)
	ctx := dbtest.WithTenantCtx(t, tenant)
	p, err := store.Create(ctx, auditperiod.CreateInput{
		Name:               "slice 749 period " + uuid.NewString()[:8],
		FrameworkVersionID: fwID,
		PeriodStart:        frozenAt.Add(-90 * 24 * time.Hour),
		PeriodEnd:          frozenAt.Add(24 * time.Hour),
		CreatedBy:          "key_test_749",
	})
	if err != nil {
		t.Fatalf("create period: %v", err)
	}
	if _, err := store.Freeze(ctx, p.ID, "key_test_749", frozenAt); err != nil {
		t.Fatalf("freeze period: %v", err)
	}
	return p.ID
}

// seedOpenPeriod creates an OPEN (not frozen) period.
func seedOpenPeriod(t *testing.T, app *pgxpool.Pool, tenant string, fwID uuid.UUID) uuid.UUID {
	t.Helper()
	store := auditperiod.NewStore(app)
	ctx := dbtest.WithTenantCtx(t, tenant)
	p, err := store.Create(ctx, auditperiod.CreateInput{
		Name:               "slice 749 open period " + uuid.NewString()[:8],
		FrameworkVersionID: fwID,
		PeriodStart:        time.Now().Add(-90 * 24 * time.Hour),
		PeriodEnd:          time.Now().Add(24 * time.Hour),
		CreatedBy:          "key_test_749",
	})
	if err != nil {
		t.Fatalf("create open period: %v", err)
	}
	return p.ID
}

func periodStubWith(store *evidencesummary.PeriodStore, draft string) *evidencesummary.PeriodService {
	client := &llm.StubClient{Result: llm.GenerateResult{
		Text:          draft,
		ModelName:     "stub-model",
		ModelVersion:  "1",
		ModelProvider: "stub",
	}}
	return evidencesummary.NewPeriodService(store, client, store)
}

// ===== AC-1 / AC-5: frozen-population integrity (THE headline, P0-749-1) =====
//
// A control has BOTH pre-freeze and post-freeze evidence. The period-scoped
// summary must draw ONLY from the pre-freeze (observed_at <= frozen_at)
// population: the post-freeze record must NEVER appear in the frozen evidence set
// AND must NEVER be citable (invariant #10). We prove both legs:
//   - the assembled set contains only pre-freeze ids;
//   - a draft citing the post-freeze id is SUPPRESSED (it is not in the citable
//     set, nor resolvable under the freeze horizon).
func TestPeriodSummarize_PostFreezeRecordNeverEnters(t *testing.T) {
	app := dbtest.NewAppPool(t)
	admin := dbtest.NewMigratePool(t)
	tenant := freshPeriodTenant(t, admin)
	fwID := seedFrameworkVersion(t, admin)

	frozenAt := time.Now().UTC().Add(-24 * time.Hour)
	ctrlID := seedControl(t, admin, tenant)
	// Two pre-freeze records and one post-freeze (LIVE) record.
	preA := seedEvidence(t, admin, tenant, ctrlID, frozenAt.Add(-48*time.Hour))
	preB := seedEvidence(t, admin, tenant, ctrlID, frozenAt.Add(-12*time.Hour))
	postFreeze := seedEvidence(t, admin, tenant, ctrlID, frozenAt.Add(12*time.Hour))

	periodID := seedFrozenPeriod(t, app, tenant, fwID, frozenAt)
	store := evidencesummary.NewPeriodStore(app)

	// Leg 1: the assembled frozen set excludes the post-freeze record.
	set, err := store.PeriodEvidenceSet(tenantCtx(t, tenant), ctrlID, periodID)
	if err != nil {
		t.Fatalf("PeriodEvidenceSet: %v", err)
	}
	if set.TotalCount != 2 {
		t.Fatalf("AC-5: frozen population should be 2 (pre-freeze only), got %d", set.TotalCount)
	}
	got := map[uuid.UUID]bool{}
	for _, r := range set.Records {
		got[r.EvidenceID] = true
		if r.ObservedAt.After(frozenAt) {
			t.Fatalf("FROZEN-POPULATION LEAK: record %s observed at %v exceeds frozen_at %v",
				r.EvidenceID, r.ObservedAt, frozenAt)
		}
	}
	if !got[preA] || !got[preB] {
		t.Errorf("frozen set missing a pre-freeze record: have %v", got)
	}
	if got[postFreeze] {
		t.Fatal("FROZEN-POPULATION LEAK: post-freeze record entered the frozen evidence set (P0-749-1)")
	}

	// Leg 2: a summary citing the post-freeze id is suppressed (it is not citable).
	draft := fmt.Sprintf("Control (%s) cites post-freeze record (%s).", ctrlID, postFreeze)
	sum, err := periodStubWith(store, draft).PeriodSummarize(tenantCtx(t, tenant), ctrlID, periodID)
	if err != nil {
		t.Fatalf("PeriodSummarize: %v", err)
	}
	if !sum.Suppressed {
		t.Fatal("FROZEN-POPULATION LEAK: a post-freeze id was citable in the period summary (P0-749-1)")
	}
	if sum.Reason != evidencesummary.ReasonUnresolvedCitation {
		t.Errorf("reason = %q, want %q", sum.Reason, evidencesummary.ReasonUnresolvedCitation)
	}
	if sum.Text != "" {
		t.Error("post-freeze-citation summary must not surface text")
	}
	// Defense-in-depth: the resolver itself refuses the post-freeze id under the
	// freeze horizon, but resolves a pre-freeze id.
	if _, ok, rerr := store.ResolveBeforeHorizon(tenantCtx(t, tenant), postFreeze, ctrlID, frozenAt); rerr != nil {
		t.Fatalf("ResolveBeforeHorizon(postFreeze): %v", rerr)
	} else if ok {
		t.Fatal("FROZEN-POPULATION LEAK: resolver resolved a post-freeze evidence id under the freeze horizon")
	}
	if _, ok, rerr := store.ResolveBeforeHorizon(tenantCtx(t, tenant), preA, ctrlID, frozenAt); rerr != nil {
		t.Fatalf("ResolveBeforeHorizon(preA): %v", rerr)
	} else if !ok {
		t.Fatal("resolver could not resolve an in-population pre-freeze id — resolver is broken")
	}
}

// ===== AC-1/AC-2: valid frozen-population citation renders =====

func TestPeriodSummarize_ValidFrozenCitationRenders(t *testing.T) {
	app := dbtest.NewAppPool(t)
	admin := dbtest.NewMigratePool(t)
	tenant := freshPeriodTenant(t, admin)
	fwID := seedFrameworkVersion(t, admin)

	frozenAt := time.Now().UTC().Add(-24 * time.Hour)
	ctrlID := seedControl(t, admin, tenant)
	evID := seedEvidence(t, admin, tenant, ctrlID, frozenAt.Add(-48*time.Hour))
	periodID := seedFrozenPeriod(t, app, tenant, fwID, frozenAt)

	store := evidencesummary.NewPeriodStore(app)
	draft := fmt.Sprintf("Control (%s) has a frozen-period access review (%s) that passed.", ctrlID, evID)
	sum, err := periodStubWith(store, draft).PeriodSummarize(tenantCtx(t, tenant), ctrlID, periodID)
	if err != nil {
		t.Fatalf("PeriodSummarize: %v", err)
	}
	if sum.Suppressed {
		t.Fatalf("expected rendered summary, got suppressed (%q)", sum.Reason)
	}
	if len(sum.Citations) != 2 {
		t.Fatalf("want 2 resolved citations, got %d: %+v", len(sum.Citations), sum.Citations)
	}
	if sum.AuditPeriodID != periodID {
		t.Errorf("audit period id mismatch: %s vs %s", sum.AuditPeriodID, periodID)
	}
	if !sum.FrozenAt.Equal(frozenAt.Truncate(time.Microsecond)) && sum.FrozenAt.Sub(frozenAt).Abs() > time.Millisecond {
		t.Errorf("frozen_at label = %v, want ~%v", sum.FrozenAt, frozenAt)
	}
}

// ===== AC-2: a non-frozen / non-existent citation is suppressed =====

func TestPeriodSummarize_NonExistentCitationSuppressed(t *testing.T) {
	app := dbtest.NewAppPool(t)
	admin := dbtest.NewMigratePool(t)
	tenant := freshPeriodTenant(t, admin)
	fwID := seedFrameworkVersion(t, admin)

	frozenAt := time.Now().UTC().Add(-24 * time.Hour)
	ctrlID := seedControl(t, admin, tenant)
	seedEvidence(t, admin, tenant, ctrlID, frozenAt.Add(-48*time.Hour))
	periodID := seedFrozenPeriod(t, app, tenant, fwID, frozenAt)

	store := evidencesummary.NewPeriodStore(app)
	fabricated := uuid.New()
	draft := fmt.Sprintf("Control (%s) shows record (%s).", ctrlID, fabricated)
	sum, err := periodStubWith(store, draft).PeriodSummarize(tenantCtx(t, tenant), ctrlID, periodID)
	if err != nil {
		t.Fatalf("PeriodSummarize: %v", err)
	}
	if !sum.Suppressed || sum.Reason != evidencesummary.ReasonUnresolvedCitation {
		t.Fatalf("want suppressed/unresolved_citation, got suppressed=%v reason=%q", sum.Suppressed, sum.Reason)
	}
	// AC-7: the deterministic frozen evidence list STILL renders.
	if len(sum.EvidenceSet.Records) != 1 {
		t.Error("frozen evidence list must still render on suppression")
	}
}

// ===== AC-3: cross-tenant isolation (P0-749-3 inherits P0-502-2) =====
//
// Tenant A owns a control + frozen-period evidence. The model (for a Tenant-B
// request over B's own frozen period) leaks Tenant A's evidence id. Under Tenant
// B's RLS context the Tenant-A id is invisible, so the summary is SUPPRESSED.
func TestPeriodSummarize_CrossTenantCitationSuppressed(t *testing.T) {
	app := dbtest.NewAppPool(t)
	admin := dbtest.NewMigratePool(t)

	tenantA := freshPeriodTenant(t, admin)
	tenantB := freshPeriodTenant(t, admin)
	fwID := seedFrameworkVersion(t, admin)

	frozenAt := time.Now().UTC().Add(-24 * time.Hour)
	// Tenant A's records + frozen period.
	ctrlA := seedControl(t, admin, tenantA)
	evA := seedEvidence(t, admin, tenantA, ctrlA, frozenAt.Add(-48*time.Hour))

	// Tenant B's own control + evidence + frozen period.
	ctrlB := seedControl(t, admin, tenantB)
	seedEvidence(t, admin, tenantB, ctrlB, frozenAt.Add(-48*time.Hour))
	periodB := seedFrozenPeriod(t, app, tenantB, fwID, frozenAt)

	store := evidencesummary.NewPeriodStore(app)
	draft := fmt.Sprintf("Control (%s) summary referencing cross-tenant record (%s).", ctrlB, evA)
	sum, err := periodStubWith(store, draft).PeriodSummarize(tenantCtx(t, tenantB), ctrlB, periodB)
	if err != nil {
		t.Fatalf("PeriodSummarize: %v", err)
	}
	if !sum.Suppressed {
		t.Fatal("CROSS-TENANT LEAK: a Tenant-B period summary cited a Tenant-A record without suppression (AC-3)")
	}
	if sum.Reason != evidencesummary.ReasonUnresolvedCitation {
		t.Errorf("reason = %q, want %q", sum.Reason, evidencesummary.ReasonUnresolvedCitation)
	}
	// Defense-in-depth: the leaked id is genuinely invisible to Tenant B at the
	// resolver level under the freeze horizon.
	if _, ok, rerr := store.ResolveBeforeHorizon(tenantCtx(t, tenantB), evA, ctrlB, frozenAt); rerr != nil {
		t.Fatalf("ResolveBeforeHorizon(tenantB, evA): %v", rerr)
	} else if ok {
		t.Fatal("CROSS-TENANT LEAK: Tenant B resolved Tenant A's evidence id")
	}
}

// ===== period-state guards: not-found + not-frozen =====

func TestPeriodSummarize_UnknownPeriodNotFound(t *testing.T) {
	app := dbtest.NewAppPool(t)
	admin := dbtest.NewMigratePool(t)
	tenant := freshPeriodTenant(t, admin)
	ctrlID := seedControl(t, admin, tenant)

	store := evidencesummary.NewPeriodStore(app)
	_, err := periodStubWith(store, "ignored").PeriodSummarize(tenantCtx(t, tenant), ctrlID, uuid.New())
	if err == nil {
		t.Fatal("expected ErrNoPeriod for an unknown audit period id")
	}
}

func TestPeriodSummarize_OpenPeriodRefused(t *testing.T) {
	app := dbtest.NewAppPool(t)
	admin := dbtest.NewMigratePool(t)
	tenant := freshPeriodTenant(t, admin)
	fwID := seedFrameworkVersion(t, admin)
	ctrlID := seedControl(t, admin, tenant)
	periodID := seedOpenPeriod(t, app, tenant, fwID)

	store := evidencesummary.NewPeriodStore(app)
	_, err := periodStubWith(store, "ignored").PeriodSummarize(tenantCtx(t, tenant), ctrlID, periodID)
	if err == nil {
		t.Fatal("an OPEN period has no frozen population — expected ErrPeriodNotFrozen (P0-749-1: never mix live + frozen)")
	}
}
