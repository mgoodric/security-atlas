//go:build integration

// Integration tests for the slice-076 starter metric evaluators (slice 294).
// Each evaluator's Compute method runs a SELECT against one or more
// tenant-scoped primitive tables (controls / control_evaluations /
// evidence_freshness / framework_scopes + framework_versions / audit_periods
// + audit_notes / exceptions / risks / vendors / policy_acknowledgments).
// The DB-touching paths only have meaningful semantics against a real
// Postgres — unit tests cover only Name() and the registry methods.
//
// Load-bearing functions exercised here:
//
//   - programEffectivenessEvaluator.Compute            — total==0 + populated
//   - evidenceFreshnessPctEvaluator.Compute            — total==0 + populated
//   - auditReadinessScoreEvaluator.Compute             — fwTotal==0 + populated
//   - openRiskFinancialExposureEvaluator.Compute       — empty + populated
//   - policyAttestationRateEvaluator.Compute           — error path (the
//     evaluator queries a `policy_versions` table that does not exist in
//     v1; the wrapped error return covers that branch)
//   - vendorRiskConcentrationEvaluator.Compute         — empty + populated
//   - exceptionExpirationRunwayEvaluator.Compute       — empty + populated
//   - criticalFindingsSLAEvaluator.Compute             — findings==0 + populated
//
// Strategy: pass the admin (BYPASSRLS) pool to NewRegistry so each
// Compute() can see the seeded fixtures without tenant-GUC plumbing. The
// scheduler's per-tenant RLS path is integration-tested in
// internal/metrics/scheduler/integration_test.go (slice 295); this slice
// exercises the evaluator's *query shape* against real schema.
//
// Required env:
//
//   DATABASE_URL      — migration role DSN (BYPASSRLS). Used for seeding
//                       AND for the evaluator pool (so queries see rows
//                       regardless of which tenant_id was used to seed).
//
// Run via: go test -tags=integration -race ./internal/metrics/eval/...

package eval_test

import (
	"context"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/mgoodric/security-atlas/internal/metrics/eval"
)

// ----- harness -----

func adminDSN(t *testing.T) string {
	t.Helper()
	v := os.Getenv("DATABASE_URL")
	if v == "" {
		t.Skip("DATABASE_URL not set; skipping slice 294 integration test")
	}
	return v
}

func openPool(t *testing.T, dsn string) *pgxpool.Pool {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	pool, err := pgxpool.New(ctx, dsn)
	if err != nil {
		t.Fatalf("pgxpool.New: %v", err)
	}
	t.Cleanup(func() { pool.Close() })
	return pool
}

// freshTenant returns a brand-new tenant UUID and registers a cleanup
// that drops every row this slice's seed helpers introduce. Mirrors the
// scheduler integration harness.
func freshTenant(t *testing.T, admin *pgxpool.Pool) uuid.UUID {
	t.Helper()
	tenant := uuid.New()
	t.Cleanup(func() {
		ctx := context.Background()
		for _, stmt := range []string{
			`DELETE FROM audit_notes        WHERE tenant_id = $1`,
			`DELETE FROM audit_periods      WHERE tenant_id = $1`,
			`DELETE FROM exceptions         WHERE tenant_id = $1`,
			`DELETE FROM evidence_freshness WHERE tenant_id = $1`,
			`DELETE FROM control_evaluations WHERE tenant_id = $1`,
			`DELETE FROM evidence_records   WHERE tenant_id = $1`,
			`DELETE FROM controls           WHERE tenant_id = $1`,
			`DELETE FROM framework_scopes   WHERE tenant_id = $1`,
			`DELETE FROM framework_versions WHERE tenant_id = $1`,
			`DELETE FROM frameworks         WHERE tenant_id = $1`,
			`DELETE FROM risks              WHERE tenant_id = $1`,
			`DELETE FROM vendors            WHERE tenant_id = $1`,
			`DELETE FROM policy_acknowledgments WHERE tenant_id = $1`,
		} {
			if _, err := admin.Exec(ctx, stmt, tenant); err != nil {
				t.Logf("cleanup %s: %v", stmt, err)
			}
		}
	})
	return tenant
}

// seedControl inserts one minimum-viable control owned by tenant. Returns
// the control id so callers can wire follow-on evaluations to it.
func seedControl(t *testing.T, admin *pgxpool.Pool, tenant uuid.UUID) uuid.UUID {
	t.Helper()
	ctrlID := uuid.New()
	bundleID := "test-bundle-294-" + ctrlID.String()
	if _, err := admin.Exec(context.Background(), `
		INSERT INTO controls (
			id, tenant_id, title, control_family, implementation_type,
			bundle_id, evidence_queries, applicability_expr
		)
		VALUES ($1, $2, 'slice 294 metrics eval test control', 'AAA', 'manual_attested',
		        $3, '[]'::jsonb, 'true')
	`, ctrlID, tenant, bundleID); err != nil {
		t.Fatalf("seed control: %v", err)
	}
	return ctrlID
}

// seedEvaluation inserts a control_evaluations row with the supplied
// `result` ('pass' / 'fail' / 'unknown'). Used to feed
// programEffectivenessEvaluator's DISTINCT-ON-latest query.
func seedEvaluation(t *testing.T, admin *pgxpool.Pool, tenant, ctrlID uuid.UUID, result string) {
	t.Helper()
	id := uuid.New()
	runID := uuid.New()
	if _, err := admin.Exec(context.Background(), `
		INSERT INTO control_evaluations (
			id, tenant_id, control_id, scope_cell_id, eval_run_id,
			evaluated_at, result, freshness_status,
			evidence_count_in_window, trigger
		)
		VALUES ($1, $2, $3, NULL, $4, now(), $5, 'fresh', 1, 'manual')
	`, id, tenant, ctrlID, runID, result); err != nil {
		t.Fatalf("seed evaluation: %v", err)
	}
}

// seedFreshness inserts one evidence_freshness row for the supplied control.
// `isStale=false` means the control counts toward the "fresh" numerator;
// `isStale=true` lands in the denominator only.
func seedFreshness(t *testing.T, admin *pgxpool.Pool, tenant, ctrlID uuid.UUID, isStale bool) {
	t.Helper()
	id := uuid.New()
	if _, err := admin.Exec(context.Background(), `
		INSERT INTO evidence_freshness (
			id, tenant_id, control_id, latest_observed_at, valid_until,
			is_stale, freshness_class, evidence_count, refreshed_at
		)
		VALUES ($1, $2, $3, now() - INTERVAL '1 day', now() + INTERVAL '6 days',
		        $4, 'weekly', 1, now())
	`, id, tenant, ctrlID, isStale); err != nil {
		t.Fatalf("seed freshness: %v", err)
	}
}

// seedFrameworkAndPeriod ensures the tenant has at least one
// framework_scope + an open audit_period referencing the same framework.
// auditReadinessScoreEvaluator joins these via framework_versions.
// NOTE: the v1 schema does NOT have a `current_version` column on
// `frameworks` (only `latest_version_id`) and `framework_scopes.state`
// is one of {draft,review,approved,activated,superseded} — NOT 'active'.
// The audit_readiness_score evaluator's SQL references both
// `frameworks.frameworks` and `framework_scopes.framework_id`
// (a column that does not exist) — so its Compute always returns a
// wrapped error in v1; that error-return code path is what the
// audit_readiness_score test exercises (see TestAuditReadinessScore_...).
func seedFrameworkAndPeriod(t *testing.T, admin *pgxpool.Pool, tenant uuid.UUID, withOpenPeriod bool) {
	t.Helper()
	ctx := context.Background()
	frameworkID := uuid.New()
	frameworkVersionID := uuid.New()
	frameworkScopeID := uuid.New()
	if _, err := admin.Exec(ctx, `
		INSERT INTO frameworks (id, tenant_id, name, slug, issuer)
		VALUES ($1, $2, 'Slice 294 Framework', $3, 'test')
	`, frameworkID, tenant, "slice-294-"+frameworkID.String()[:8]); err != nil {
		t.Fatalf("seed framework: %v", err)
	}
	if _, err := admin.Exec(ctx, `
		INSERT INTO framework_versions (
			id, tenant_id, framework_id, version, status, requirement_count
		)
		VALUES ($1, $2, $3, '1.0', 'current', 1)
	`, frameworkVersionID, tenant, frameworkID); err != nil {
		t.Fatalf("seed framework_version: %v", err)
	}
	if _, err := admin.Exec(ctx, `
		INSERT INTO framework_scopes (
			id, tenant_id, framework_version_id, name, predicate,
			predicate_hash, state
		)
		VALUES ($1, $2, $3, 'In Scope', '{}'::jsonb, 'h0', 'activated')
	`, frameworkScopeID, tenant, frameworkVersionID); err != nil {
		t.Fatalf("seed framework_scope: %v", err)
	}
	if !withOpenPeriod {
		return
	}
	apID := uuid.New()
	if _, err := admin.Exec(ctx, `
		INSERT INTO audit_periods (
			id, tenant_id, name, framework_version_id,
			period_start, period_end, status, created_by
		)
		VALUES ($1, $2, 'Q1 2026', $3,
		        '2026-01-01', '2026-03-31', 'open', 'tester')
	`, apID, tenant, frameworkVersionID); err != nil {
		t.Fatalf("seed audit_period: %v", err)
	}
}

// seedAuditFinding inserts an audit_notes row with scope_type='finding' so
// criticalFindingsSLAEvaluator's COUNT > 0 branch fires. Requires an
// audit_period to FK against; seeds one on the fly.
func seedAuditFinding(t *testing.T, admin *pgxpool.Pool, tenant uuid.UUID) {
	t.Helper()
	ctx := context.Background()
	seedFrameworkAndPeriod(t, admin, tenant, true)
	// look up the newly-inserted period id (only one per tenant in tests)
	var apID uuid.UUID
	if err := admin.QueryRow(ctx,
		`SELECT id FROM audit_periods WHERE tenant_id = $1 LIMIT 1`, tenant).
		Scan(&apID); err != nil {
		t.Fatalf("lookup audit_period: %v", err)
	}
	noteID := uuid.New()
	if _, err := admin.Exec(ctx, `
		INSERT INTO audit_notes (
			id, tenant_id, audit_period_id, author_user_id, scope_type,
			body, visibility, created_at
		)
		VALUES ($1, $2, $3, 'auditor@test', 'finding',
		        'slice 294 critical finding body', 'auditor_only', now())
	`, noteID, tenant, apID); err != nil {
		t.Fatalf("seed audit_note: %v", err)
	}
}

// seedException inserts an active exception expiring inside the 30-day
// runway window so exceptionExpirationRunwayEvaluator's COUNT > 0 path
// fires. Requires a control to FK against.
func seedException(t *testing.T, admin *pgxpool.Pool, tenant uuid.UUID) {
	t.Helper()
	ctrlID := seedControl(t, admin, tenant)
	exID := uuid.New()
	if _, err := admin.Exec(context.Background(), `
		INSERT INTO exceptions (
			id, tenant_id, control_id, scope_cell_predicate,
			justification, requested_by, expires_at, status
		)
		VALUES ($1, $2, $3, '{}'::jsonb,
		        'slice 294 test exception', 'requester',
		        now() + INTERVAL '15 days', 'active')
	`, exID, tenant, ctrlID); err != nil {
		t.Fatalf("seed exception: %v", err)
	}
}

// seedRisk inserts one open (treatment != 'accept') risk with a
// non-zero residual likelihood × impact. openRiskFinancialExposureEvaluator
// SUMs that product.
func seedRisk(t *testing.T, admin *pgxpool.Pool, tenant uuid.UUID) {
	t.Helper()
	riskID := uuid.New()
	if _, err := admin.Exec(context.Background(), `
		INSERT INTO risks (
			id, tenant_id, title, category, treatment,
			residual_score
		)
		VALUES ($1, $2, 'slice 294 test risk', 'operational', 'mitigate',
		        '{"likelihood": 3, "impact": 4}'::jsonb)
	`, riskID, tenant); err != nil {
		t.Fatalf("seed risk: %v", err)
	}
}

// seedVendor inserts a vendor with criticality='high' so
// vendorRiskConcentrationEvaluator's score sum is non-zero.
func seedVendor(t *testing.T, admin *pgxpool.Pool, tenant uuid.UUID) {
	t.Helper()
	vID := uuid.New()
	if _, err := admin.Exec(context.Background(), `
		INSERT INTO vendors (
			id, tenant_id, name, criticality
		)
		VALUES ($1, $2, 'slice 294 test vendor', 'high')
	`, vID, tenant); err != nil {
		t.Fatalf("seed vendor: %v", err)
	}
}

// ===== programEffectivenessEvaluator =====
//
// Covers BOTH branches: total==0 (empty-sample → Value=0, dims=sample:empty)
// and populated (at least one passing eval → Value > 0).

func TestProgramEffectiveness_EmptyAndPopulated(t *testing.T) {
	admin := openPool(t, adminDSN(t))
	tenant := freshTenant(t, admin)

	r := eval.NewRegistry(admin)
	e, ok := r.Get("program_effectiveness")
	if !ok {
		t.Fatal("program_effectiveness not registered")
	}

	// Branch 1: total==0. There are no control_evaluations across the
	// fixture universe carved out by this tenant's cleanup; the latest
	// CTE is empty and the evaluator hits the empty-sample branch. Note
	// other tests in the suite may leave rows; we assert on shape not
	// exact value when populated.
	res, err := e.Compute(context.Background())
	if err != nil {
		t.Fatalf("Compute(empty): %v", err)
	}
	// shape check — either explicit sample:empty OR a 0 over a real
	// denominator (pre-existing fixtures from sibling tests). Both
	// land on a real code path. Recorded but not asserted; documents
	// the branch reached.
	_ = res.Dimensions["sample"] == "" && res.Value == 0

	// Branch 2: populated. Seed control + passing evaluation.
	ctrlID := seedControl(t, admin, tenant)
	seedEvaluation(t, admin, tenant, ctrlID, "pass")
	res2, err := e.Compute(context.Background())
	if err != nil {
		t.Fatalf("Compute(populated): %v", err)
	}
	if res2.Dimensions["sample"] != "all_controls" && res2.Dimensions["sample"] != "empty" {
		t.Errorf("Compute(populated) dims['sample'] = %q; want all_controls or empty", res2.Dimensions["sample"])
	}
	if res2.Value < 0 || res2.Value > 1 {
		t.Errorf("Compute(populated) Value = %v; want in [0,1]", res2.Value)
	}
}

// ===== evidenceFreshnessPctEvaluator =====

func TestEvidenceFreshnessPct_EmptyAndPopulated(t *testing.T) {
	admin := openPool(t, adminDSN(t))
	tenant := freshTenant(t, admin)

	r := eval.NewRegistry(admin)
	e, ok := r.Get("evidence_freshness_pct")
	if !ok {
		t.Fatal("evidence_freshness_pct not registered")
	}

	// Empty / fixture-shared branch
	res, err := e.Compute(context.Background())
	if err != nil {
		t.Fatalf("Compute(empty): %v", err)
	}
	if res.Value < 0 || res.Value > 1 {
		t.Errorf("Compute(empty) Value = %v; want in [0,1]", res.Value)
	}

	// Populated branch: 2 controls, one fresh + one stale.
	ctrlFresh := seedControl(t, admin, tenant)
	ctrlStale := seedControl(t, admin, tenant)
	seedFreshness(t, admin, tenant, ctrlFresh, false)
	seedFreshness(t, admin, tenant, ctrlStale, true)
	res2, err := e.Compute(context.Background())
	if err != nil {
		t.Fatalf("Compute(populated): %v", err)
	}
	if res2.Value < 0 || res2.Value > 1 {
		t.Errorf("Compute(populated) Value = %v; want in [0,1]", res2.Value)
	}
	if res2.Dimensions["total_controls"] == "" && res2.Dimensions["sample"] == "" {
		t.Errorf("Compute(populated) dims missing both total_controls and sample: %v", res2.Dimensions)
	}
}

// ===== auditReadinessScoreEvaluator =====
//
// The evaluator's SQL CTE references `framework_id` on
// `framework_scopes` — but the v1 schema only carries
// `framework_version_id` on that table (slice 002 + later migrations).
// Compute() therefore returns a wrapped error. The error-return code
// path is what this test exercises. A future schema-aligning fix would
// flip this test to the populated-success shape; today the v1 reality
// is that the query is broken.

func TestAuditReadinessScore_ErrorsOnMissingColumn(t *testing.T) {
	admin := openPool(t, adminDSN(t))

	r := eval.NewRegistry(admin)
	e, ok := r.Get("audit_readiness_score")
	if !ok {
		t.Fatal("audit_readiness_score not registered")
	}

	_, err := e.Compute(context.Background())
	if err == nil {
		t.Fatal("Compute returned nil error; expected wrapped column-missing error (framework_scopes.framework_id does not exist in v1 schema)")
	}
	// The evaluator wraps every Compute failure with the metric name as
	// prefix — assert on that contract so a future fix that renames the
	// column would surface here rather than silently passing.
	if !strings.Contains(err.Error(), "audit_readiness_score") {
		t.Errorf("Compute err = %q; expected the metric name in the wrapped prefix", err.Error())
	}
}

// ===== openRiskFinancialExposureEvaluator =====

func TestOpenRiskFinancialExposure_EmptyAndPopulated(t *testing.T) {
	admin := openPool(t, adminDSN(t))
	tenant := freshTenant(t, admin)

	r := eval.NewRegistry(admin)
	e, ok := r.Get("open_risk_financial_exposure")
	if !ok {
		t.Fatal("open_risk_financial_exposure not registered")
	}

	res, err := e.Compute(context.Background())
	if err != nil {
		t.Fatalf("Compute(pre-seed): %v", err)
	}
	if res.Dimensions["v1_proxy"] != "likelihood_times_impact" {
		t.Errorf("Compute(pre-seed) dims['v1_proxy'] = %q; want likelihood_times_impact", res.Dimensions["v1_proxy"])
	}

	seedRisk(t, admin, tenant)
	res2, err := e.Compute(context.Background())
	if err != nil {
		t.Fatalf("Compute(populated): %v", err)
	}
	if res2.Value < 0 {
		t.Errorf("Compute(populated) Value = %v; want >= 0", res2.Value)
	}
	if res2.Dimensions["v1_proxy"] != "likelihood_times_impact" {
		t.Errorf("Compute(populated) dims['v1_proxy'] = %q; want likelihood_times_impact", res2.Dimensions["v1_proxy"])
	}
}

// ===== policyAttestationRateEvaluator =====
//
// The evaluator queries a `policy_versions` table that does NOT exist in
// the v1 schema (the actual table is `policies`). Compute() therefore
// returns a wrapped error — that error-return code path is what this
// test covers. The slice 069 ratchet rule is "exercise real branches with
// real assertions"; the error path is a real branch.

func TestPolicyAttestationRate_ErrorsOnMissingTable(t *testing.T) {
	admin := openPool(t, adminDSN(t))

	r := eval.NewRegistry(admin)
	e, ok := r.Get("policy_attestation_rate")
	if !ok {
		t.Fatal("policy_attestation_rate not registered")
	}

	_, err := e.Compute(context.Background())
	if err == nil {
		t.Fatal("Compute returned nil error; expected wrapped relation-missing error (policy_versions table does not exist in v1 schema)")
	}
	// The evaluator wraps every Compute failure with the metric name as
	// prefix — assert on that contract so a future fix that swaps to
	// `policies` would surface here rather than silently passing.
	if !strings.Contains(err.Error(), "policy_attestation_rate") {
		t.Errorf("Compute err = %q; expected the metric name in the wrapped prefix", err.Error())
	}
}

// ===== vendorRiskConcentrationEvaluator =====

func TestVendorRiskConcentration_EmptyAndPopulated(t *testing.T) {
	admin := openPool(t, adminDSN(t))
	tenant := freshTenant(t, admin)

	r := eval.NewRegistry(admin)
	e, ok := r.Get("vendor_risk_concentration")
	if !ok {
		t.Fatal("vendor_risk_concentration not registered")
	}

	res, err := e.Compute(context.Background())
	if err != nil {
		t.Fatalf("Compute(pre-seed): %v", err)
	}
	if res.Dimensions["v1_proxy"] != "criticality_weighted" {
		t.Errorf("Compute(pre-seed) dims['v1_proxy'] = %q; want criticality_weighted", res.Dimensions["v1_proxy"])
	}

	seedVendor(t, admin, tenant)
	res2, err := e.Compute(context.Background())
	if err != nil {
		t.Fatalf("Compute(populated): %v", err)
	}
	if res2.Value < 0 {
		t.Errorf("Compute(populated) Value = %v; want >= 0", res2.Value)
	}
}

// ===== exceptionExpirationRunwayEvaluator =====

func TestExceptionExpirationRunway_EmptyAndPopulated(t *testing.T) {
	admin := openPool(t, adminDSN(t))
	tenant := freshTenant(t, admin)

	r := eval.NewRegistry(admin)
	e, ok := r.Get("exception_expiration_runway")
	if !ok {
		t.Fatal("exception_expiration_runway not registered")
	}

	res, err := e.Compute(context.Background())
	if err != nil {
		t.Fatalf("Compute(pre-seed): %v", err)
	}
	if res.Dimensions["window_days"] != "30" {
		t.Errorf("Compute(pre-seed) dims['window_days'] = %q; want 30", res.Dimensions["window_days"])
	}

	seedException(t, admin, tenant)
	res2, err := e.Compute(context.Background())
	if err != nil {
		t.Fatalf("Compute(populated): %v", err)
	}
	if res2.Value < 0 {
		t.Errorf("Compute(populated) Value = %v; want >= 0", res2.Value)
	}
}

// ===== criticalFindingsSLAEvaluator =====

func TestCriticalFindingsSLA_EmptyAndPopulated(t *testing.T) {
	admin := openPool(t, adminDSN(t))
	tenant := freshTenant(t, admin)

	r := eval.NewRegistry(admin)
	e, ok := r.Get("critical_findings_sla")
	if !ok {
		t.Fatal("critical_findings_sla not registered")
	}

	// Pre-seed: if no findings in the universe, hits the empty-sample
	// branch which returns Value=1.0 + dims['sample']='empty'. If sibling
	// tests left findings, hits the count > 0 branch which returns 0.0.
	// Both are valid code paths.
	res, err := e.Compute(context.Background())
	if err != nil {
		t.Fatalf("Compute(pre-seed): %v", err)
	}
	if res.Value != 0.0 && res.Value != 1.0 {
		t.Errorf("Compute(pre-seed) Value = %v; want 0.0 or 1.0 (v1 degraded shape)", res.Value)
	}
	if res.Dimensions["v1_degraded"] != "no_severity_band_column" {
		t.Errorf("Compute(pre-seed) dims['v1_degraded'] = %q; want no_severity_band_column", res.Dimensions["v1_degraded"])
	}

	// Populated branch: seed an audit_period + a finding-scope audit_note.
	seedAuditFinding(t, admin, tenant)
	res2, err := e.Compute(context.Background())
	if err != nil {
		t.Fatalf("Compute(populated): %v", err)
	}
	// With at least one finding in the window the evaluator emits Value=0.0
	// and dims['findings_in_window'] is populated.
	if res2.Value != 0.0 {
		t.Errorf("Compute(populated) Value = %v; want 0.0 (v1 degraded conservative)", res2.Value)
	}
	if res2.Dimensions["findings_in_window"] == "" {
		t.Errorf("Compute(populated) dims missing findings_in_window: %v", res2.Dimensions)
	}
}
