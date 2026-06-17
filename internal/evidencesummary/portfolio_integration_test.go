//go:build integration

// Slice 750 — integration tests for the PORTFOLIO (multi-control) AI
// evidence-summarization surface. Real Postgres + the real
// evidencesummary.PortfolioStore (RLS-backed two-level bounded cross-control
// corpus assembly + the embedded slice-502 citation resolver) + the
// llm.StubClient (no live Ollama in CI — the slice-498 CI seam). The stub lets us
// craft a VALID draft (cites real tenant-owned ids across controls, states only
// rollup-allowed numbers), a FABRICATED-COUNT draft, an UNGROUNDED-citation draft,
// and a CROSS-TENANT draft, and assert the deterministic
// two-level-bound + citation-gate + numeric-gate + cross-tenant behavior.
//
// Run with:
//
//	go test -tags=integration -p 1 ./internal/evidencesummary/...
//
// Requires DATABASE_URL_APP (atlas_app role) and DATABASE_URL (admin role).
//
// Coverage:
//
//	AC-1   the two-level bound holds (cap controls AND cap records/control)
//	AC-2   an ungrounded/non-existent citation suppresses -> rollup only
//	AC-3   a fabricated portfolio count suppresses; a correct count renders
//	AC-4   a Tenant-B portfolio summary cannot cite ANY Tenant-A control/record
package evidencesummary_test

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/mgoodric/security-atlas/internal/dbtest"
	"github.com/mgoodric/security-atlas/internal/evidencesummary"
	"github.com/mgoodric/security-atlas/internal/llm"
)

// seedFamilyControl inserts a control in a given control_family (the portfolio
// filter dimension exercised here).
func seedFamilyControl(t *testing.T, admin *pgxpool.Pool, tenant, family string) uuid.UUID {
	t.Helper()
	ctrlID := uuid.New()
	if _, err := admin.Exec(context.Background(), `
		INSERT INTO controls (
			id, tenant_id, title, control_family, implementation_type,
			bundle_id, evidence_queries, applicability_expr, freshness_class
		)
		VALUES ($1, $2, 'slice 750 portfolio test control', $3, 'automated',
		        $4, '[]'::jsonb, 'true', 'quarterly')
	`, ctrlID, tenant, family, "test-bundle-750-"+ctrlID.String()); err != nil {
		t.Fatalf("seed family control: %v", err)
	}
	return ctrlID
}

// seedFrameworkWithAnchorControl seeds frameworks -> framework_version -> one SCF
// anchor -> one control anchored on it (+ one evidence record), for the framework
// filter path. Returns the framework_version id + the control id + evidence id.
func seedFrameworkWithAnchorControl(t *testing.T, admin *pgxpool.Pool, tenant string) (uuid.UUID, uuid.UUID, uuid.UUID) {
	t.Helper()
	ctx := context.Background()
	fwID := uuid.New()
	slug := "fw750-" + fwID.String()[:8]
	if _, err := admin.Exec(ctx, `
		INSERT INTO frameworks (id, name, slug, issuer) VALUES ($1, $2, $3, 'test-issuer')
	`, fwID, "Slice 750 Framework", slug); err != nil {
		t.Fatalf("seed framework: %v", err)
	}
	fvID := uuid.New()
	if _, err := admin.Exec(ctx, `
		INSERT INTO framework_versions (id, framework_id, version, status) VALUES ($1, $2, '2026', 'current')
	`, fvID, fwID); err != nil {
		t.Fatalf("seed framework_version: %v", err)
	}
	anchorID := uuid.New()
	if _, err := admin.Exec(ctx, `
		INSERT INTO scf_anchors (id, framework_version_id, scf_id, family, title)
		VALUES ($1, $2, $3, 'IAC', 'anchor 750')
	`, anchorID, fvID, "SCF-750-"+anchorID.String()[:8]); err != nil {
		t.Fatalf("seed scf_anchor: %v", err)
	}
	ctrlID := uuid.New()
	if _, err := admin.Exec(ctx, `
		INSERT INTO controls (
			id, tenant_id, title, control_family, implementation_type,
			bundle_id, evidence_queries, applicability_expr, freshness_class, scf_anchor_id
		)
		VALUES ($1, $2, 'slice 750 framework control', 'IAC', 'automated',
		        $3, '[]'::jsonb, 'true', 'quarterly', $4)
	`, ctrlID, tenant, "test-bundle-750-fw-"+ctrlID.String(), anchorID); err != nil {
		t.Fatalf("seed framework control: %v", err)
	}
	evID := seedEvidence(t, admin, tenant, ctrlID, time.Now().UTC().Add(-4*24*time.Hour))
	return fvID, ctrlID, evID
}

func portfolioStubSvc(store *evidencesummary.PortfolioStore, draft string) *evidencesummary.PortfolioService {
	client := &llm.StubClient{Result: llm.GenerateResult{
		Text:          draft,
		ModelName:     "stub-model",
		ModelVersion:  "1",
		ModelProvider: "stub",
	}}
	return evidencesummary.NewPortfolioService(store, client, store.Resolver())
}

// ----- AC-3: a correct-count cross-control summary renders -----

func TestPortfolio_ValidRenders(t *testing.T) {
	app := dbtest.NewAppPool(t)
	admin := dbtest.NewMigratePool(t)
	tenant := freshTenant(t, admin)

	fam := "IAC"
	observed := time.Now().UTC().Add(-10 * 24 * time.Hour)
	c1 := seedFamilyControl(t, admin, tenant, fam)
	c2 := seedFamilyControl(t, admin, tenant, fam)
	e1 := seedEvidence(t, admin, tenant, c1, observed)
	e2 := seedEvidence(t, admin, tenant, c2, observed)

	store := evidencesummary.NewPortfolioStore(app)
	// 2 controls in summary, 2 with evidence, 0 gaps, 2 records — all allowed.
	draft := fmt.Sprintf(
		"Across the 2 controls in this family, 2 have current live evidence and 2 records are on record. Control (%s) cites (%s); control (%s) cites (%s).",
		c1, e1, c2, e2)
	svc := portfolioStubSvc(store, draft)

	sum, err := svc.PortfolioSummarize(tenantCtx(t, tenant), evidencesummary.PortfolioFilter{Family: fam})
	if err != nil {
		t.Fatalf("PortfolioSummarize: %v", err)
	}
	if sum.Suppressed {
		t.Fatalf("expected rendered, got suppressed (reason %q)", sum.Reason)
	}
	if sum.Rollup.ControlsInSummary != 2 || sum.Rollup.ControlsWithEvidence != 2 || sum.Rollup.TotalRecords != 2 {
		t.Errorf("rollup mismatch: %+v", sum.Rollup)
	}
	if len(sum.Citations) != 4 {
		t.Errorf("expected 4 resolved citations (2 controls + 2 records), got %d", len(sum.Citations))
	}
}

// ----- AC-3: a fabricated count suppresses -----

func TestPortfolio_FabricatedCountSuppressed(t *testing.T) {
	app := dbtest.NewAppPool(t)
	admin := dbtest.NewMigratePool(t)
	tenant := freshTenant(t, admin)

	fam := "BCD"
	c1 := seedFamilyControl(t, admin, tenant, fam)
	seedEvidence(t, admin, tenant, c1, time.Now().UTC().Add(-5*24*time.Hour))

	store := evidencesummary.NewPortfolioStore(app)
	// Rollup: 1 control, 1 record. The draft fabricates "40 of 40".
	draft := fmt.Sprintf("All 40 of 40 controls are covered. Control (%s).", c1)
	svc := portfolioStubSvc(store, draft)

	sum, err := svc.PortfolioSummarize(tenantCtx(t, tenant), evidencesummary.PortfolioFilter{Family: fam})
	if err != nil {
		t.Fatalf("PortfolioSummarize: %v", err)
	}
	if !sum.Suppressed || sum.Reason != evidencesummary.ReasonNumericMismatch {
		t.Fatalf("expected numeric-mismatch suppression, got suppressed=%v reason=%q", sum.Suppressed, sum.Reason)
	}
	// The deterministic rollup is still returned (AC-7 graceful degradation).
	if len(sum.PortfolioSet.Controls) != 1 {
		t.Error("rollup must still be returned on suppression")
	}
}

// ----- AC-2: a non-existent citation suppresses -----

func TestPortfolio_NonExistentCitationSuppressed(t *testing.T) {
	app := dbtest.NewAppPool(t)
	admin := dbtest.NewMigratePool(t)
	tenant := freshTenant(t, admin)

	fam := "CDE"
	c1 := seedFamilyControl(t, admin, tenant, fam)
	seedEvidence(t, admin, tenant, c1, time.Now().UTC().Add(-3*24*time.Hour))

	store := evidencesummary.NewPortfolioStore(app)
	// Cite a fabricated id that is neither a control nor an evidence record.
	draft := fmt.Sprintf("1 control, 1 record. See (%s) and (%s).", c1, uuid.New())
	svc := portfolioStubSvc(store, draft)

	sum, err := svc.PortfolioSummarize(tenantCtx(t, tenant), evidencesummary.PortfolioFilter{Family: fam})
	if err != nil {
		t.Fatalf("PortfolioSummarize: %v", err)
	}
	if !sum.Suppressed || sum.Reason != evidencesummary.ReasonUnresolvedCitation {
		t.Fatalf("expected unresolved-citation suppression, got suppressed=%v reason=%q", sum.Suppressed, sum.Reason)
	}
}

// ----- AC-1: the two-level bound holds across many controls/records -----

func TestPortfolio_TwoLevelBound(t *testing.T) {
	app := dbtest.NewAppPool(t)
	admin := dbtest.NewMigratePool(t)
	tenant := freshTenant(t, admin)

	fam := "DEF"
	// Seed MORE controls than the per-summary cap, each with MORE records than
	// the per-control cap.
	totalControls := evidencesummary.MaxControlsPerSummary + 4
	recsPerControl := evidencesummary.MaxRecordsPerControl + 3
	for i := 0; i < totalControls; i++ {
		c := seedFamilyControl(t, admin, tenant, fam)
		for j := 0; j < recsPerControl; j++ {
			seedEvidence(t, admin, tenant, c, time.Now().UTC().Add(-time.Duration(j)*time.Hour))
		}
	}

	store := evidencesummary.NewPortfolioStore(app)
	set, err := store.PortfolioSet(tenantCtx(t, tenant), evidencesummary.PortfolioFilter{Family: fam})
	if err != nil {
		t.Fatalf("PortfolioSet: %v", err)
	}
	// FIRST level: cap controls-per-summary.
	if len(set.Controls) != evidencesummary.MaxControlsPerSummary {
		t.Fatalf("controls in corpus = %d, want %d (controls-per-summary bound)",
			len(set.Controls), evidencesummary.MaxControlsPerSummary)
	}
	// TotalControls reflects the full matched count (for the honest K-of-N label).
	if set.TotalControls < evidencesummary.MaxControlsPerSummary+1 {
		t.Errorf("TotalControls = %d, want > cap (the N in K-of-N)", set.TotalControls)
	}
	// SECOND level: cap records-per-control.
	for _, c := range set.Controls {
		if len(c.Records) != evidencesummary.MaxRecordsPerControl {
			t.Errorf("control %s has %d records in corpus, want %d (records-per-control bound)",
				c.ControlID, len(c.Records), evidencesummary.MaxRecordsPerControl)
		}
		if c.TotalCount != recsPerControl {
			t.Errorf("control %s TotalCount = %d, want %d (full count for honesty)",
				c.ControlID, c.TotalCount, recsPerControl)
		}
	}
}

// ----- AC-1: the FRAMEWORK filter path resolves controls via SCF anchors -----

func TestPortfolio_FrameworkFilterResolvesViaAnchors(t *testing.T) {
	app := dbtest.NewAppPool(t)
	admin := dbtest.NewMigratePool(t)
	tenant := freshTenant(t, admin)

	fvID, ctrlID, evID := seedFrameworkWithAnchorControl(t, admin, tenant)
	// Also seed a control NOT on this framework's anchors — it must NOT appear.
	other := seedFamilyControl(t, admin, tenant, "ZZZ")
	seedEvidence(t, admin, tenant, other, time.Now().UTC().Add(-1*24*time.Hour))

	store := evidencesummary.NewPortfolioStore(app)
	set, err := store.PortfolioSet(tenantCtx(t, tenant),
		evidencesummary.PortfolioFilter{FrameworkVersionID: fvID, FrameworkLabel: "Slice 750 Framework (2026)"})
	if err != nil {
		t.Fatalf("PortfolioSet(framework): %v", err)
	}
	if len(set.Controls) != 1 {
		t.Fatalf("framework filter matched %d controls, want exactly 1 (anchored)", len(set.Controls))
	}
	if set.Controls[0].ControlID != ctrlID {
		t.Errorf("framework filter returned the wrong control")
	}
	if len(set.Controls[0].Records) != 1 || set.Controls[0].Records[0].EvidenceID != evID {
		t.Errorf("framework control's evidence not assembled")
	}
	if set.Filter.Mode() != "framework" {
		t.Errorf("filter mode = %q, want framework", set.Filter.Mode())
	}
}

// ----- AC-1: a framework version with no anchors yields an empty set -----

func TestPortfolio_FrameworkNoAnchorsEmpty(t *testing.T) {
	app := dbtest.NewAppPool(t)
	admin := dbtest.NewMigratePool(t)
	tenant := freshTenant(t, admin)

	// A framework version with NO anchors.
	ctx := context.Background()
	fwID := uuid.New()
	if _, err := admin.Exec(ctx, `INSERT INTO frameworks (id, name, slug, issuer) VALUES ($1,'empty fw',$2,'x')`,
		fwID, "fw750empty-"+fwID.String()[:8]); err != nil {
		t.Fatalf("seed fw: %v", err)
	}
	fvID := uuid.New()
	if _, err := admin.Exec(ctx, `INSERT INTO framework_versions (id, framework_id, version, status) VALUES ($1,$2,'2026','current')`,
		fvID, fwID); err != nil {
		t.Fatalf("seed fv: %v", err)
	}

	store := evidencesummary.NewPortfolioStore(app)
	set, err := store.PortfolioSet(tenantCtx(t, tenant), evidencesummary.PortfolioFilter{FrameworkVersionID: fvID})
	if err != nil {
		t.Fatalf("PortfolioSet: %v", err)
	}
	if len(set.Controls) != 0 || set.TotalControls != 0 {
		t.Errorf("expected empty set for a no-anchor framework, got %d controls", len(set.Controls))
	}
}

// ----- AC-4 (LOAD-BEARING): cross-tenant isolation across the whole set -----

func TestPortfolio_CrossTenantIsolation(t *testing.T) {
	app := dbtest.NewAppPool(t)
	admin := dbtest.NewMigratePool(t)
	tenantA := freshTenant(t, admin)
	tenantB := freshTenant(t, admin)

	fam := "EFG"
	// Tenant A has controls + evidence in this family.
	aCtrl := seedFamilyControl(t, admin, tenantA, fam)
	aEv := seedEvidence(t, admin, tenantA, aCtrl, time.Now().UTC().Add(-2*24*time.Hour))
	// Tenant B has its own controls + evidence in the SAME family (overlapping
	// structure, distinct rows).
	bCtrl := seedFamilyControl(t, admin, tenantB, fam)
	bEv := seedEvidence(t, admin, tenantB, bCtrl, time.Now().UTC().Add(-2*24*time.Hour))

	store := evidencesummary.NewPortfolioStore(app)

	// 1) Tenant B's portfolio corpus must contain ONLY Tenant B's control/records.
	setB, err := store.PortfolioSet(tenantCtx(t, tenantB), evidencesummary.PortfolioFilter{Family: fam})
	if err != nil {
		t.Fatalf("PortfolioSet(B): %v", err)
	}
	for _, c := range setB.Controls {
		if c.ControlID == aCtrl {
			t.Fatal("AC-4 BREACH: Tenant B corpus contains Tenant A control")
		}
		for _, e := range c.Records {
			if e.EvidenceID == aEv {
				t.Fatal("AC-4 BREACH: Tenant B corpus contains Tenant A evidence")
			}
		}
	}

	// 2) A Tenant-B summary that TRIES to cite Tenant A's ids must suppress: the
	// grounding gate rejects them (not in B's corpus) AND the resolver cannot see
	// them under B's RLS.
	draft := fmt.Sprintf(
		"1 control, 1 record. Tenant B cites (%s) and (%s); also (%s) and (%s).",
		bCtrl, bEv, aCtrl, aEv)
	svc := portfolioStubSvc(store, draft)
	sumB, err := svc.PortfolioSummarize(tenantCtx(t, tenantB), evidencesummary.PortfolioFilter{Family: fam})
	if err != nil {
		t.Fatalf("PortfolioSummarize(B): %v", err)
	}
	if !sumB.Suppressed {
		t.Fatal("AC-4 BREACH: a Tenant-B summary citing Tenant-A ids was NOT suppressed")
	}
	if sumB.Reason != evidencesummary.ReasonUnresolvedCitation {
		t.Errorf("expected unresolved-citation suppression for cross-tenant cite, got %q", sumB.Reason)
	}
	// The suppressed text must not have leaked.
	if strings.Contains(sumB.Text, aCtrl.String()) || strings.Contains(sumB.Text, aEv.String()) {
		t.Fatal("AC-4 BREACH: suppressed text leaked Tenant A ids")
	}
}
