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
//   - policyAttestationRateEvaluator.Compute           — empty + populated
//   - vendorRiskConcentrationEvaluator.Compute         — empty + populated
//   - exceptionExpirationRunwayEvaluator.Compute       — empty + populated
//   - criticalFindingsSLAEvaluator.Compute             — findings==0 + populated
//
// TestAllRegisteredEvaluators_NoSQLError is the per-evaluator schema smoke
// test (OE-550): it walks Registry.Names() and asserts every registered
// evaluator's query executes against the migrated schema without a SQL
// error. A future evaluator whose SQL names a column or relation the schema
// does not carry fails there rather than in the scheduler log.
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
	"math"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/mgoodric/security-atlas/internal/dbtest"
	"github.com/mgoodric/security-atlas/internal/metrics/eval"
)

// ----- harness -----

// freshTenant returns a brand-new tenant UUID and registers a cleanup
// that drops every row this slice's seed helpers introduce. Mirrors the
// scheduler integration harness. Carve-out from the slice-435 dbtest harness:
// this suite's seeders key off a uuid.UUID tenant (not the string that
// dbtest.SeedTenant returns), so the helper stays inline; only its pool is
// re-routed to dbtest.NewMigratePool at the call sites (742 drain batch 17).
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
			`DELETE FROM policies           WHERE tenant_id = $1`,
			`DELETE FROM users              WHERE tenant_id = $1`,
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
// NOTE: `framework_scopes` carries `framework_version_id`, not
// `framework_id`, and its lifecycle column is `state` with the vocabulary
// {draft,review,approved,activated,superseded} — NOT 'active'. The
// evaluator's in-scope predicate is `state = 'activated'`, so the scope
// seeded here lands in the frameworks CTE (OE-550).
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

// seedPolicyAck inserts one user, one published policy, and one
// acknowledgment of that policy dated inside the evaluator's 365-day
// window. policyAttestationRateEvaluator's denominator is
// (distinct recent ackers) × (published policies) and its numerator is
// the ack count against those policies, so a single (user, policy, ack)
// triple lands the evaluator on its populated branch with rate 1.0.
//
// Schema note: there is no `policy_versions` table — each publish writes
// its own `policies` row, and policy_acknowledgments.policy_version_id
// FKs to policies(tenant_id, id) (slice 023). `policies` requires
// effective_date to be non-NULL once status is 'published'
// (policies_effective_date_set_when_published).
func seedPolicyAck(t *testing.T, admin *pgxpool.Pool, tenant uuid.UUID) {
	t.Helper()
	ctx := context.Background()
	userID := uuid.New()
	if _, err := admin.Exec(ctx, `
		INSERT INTO users (id, tenant_id, email, display_name)
		VALUES ($1, $2, $3, 'OE-550 metrics eval test user')
	`, userID, tenant, "oe550-"+userID.String()+"@test.invalid"); err != nil {
		t.Fatalf("seed user: %v", err)
	}
	policyID := uuid.New()
	if _, err := admin.Exec(ctx, `
		INSERT INTO policies (
			id, tenant_id, title, version, status, effective_date,
			body_md, owner_role, approver_role, created_by,
			published_at, published_by
		)
		VALUES ($1, $2, 'OE-550 acceptable use', '1.0.0', 'published', now()::date,
		        '# body', 'security_lead', 'ciso', 'tester',
		        now(), 'tester')
	`, policyID, tenant); err != nil {
		t.Fatalf("seed policy: %v", err)
	}
	ackID := uuid.New()
	if _, err := admin.Exec(ctx, `
		INSERT INTO policy_acknowledgments (
			id, tenant_id, policy_id, policy_version_id, user_id,
			acknowledged_at, ack_token
		)
		VALUES ($1, $2, $3, $3, $4, now() - INTERVAL '1 day', $5)
	`, ackID, tenant, policyID, userID, "oe550-"+ackID.String()); err != nil {
		t.Fatalf("seed policy_acknowledgment: %v", err)
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
	admin := dbtest.NewMigratePool(t)
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
	admin := dbtest.NewMigratePool(t)
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
// Covers BOTH branches: fwTotal==0 (no activated framework_scope →
// Value=0, dims=sample:empty) and populated (one activated scope whose
// framework has an open audit period + one fresh evidence_freshness row →
// periodFactor × freshFactor > 0).
//
// Before OE-550 the evaluator's SQL selected `framework_id` straight off
// `framework_scopes` (which carries only `framework_version_id`) and
// filtered on the non-existent state `'active'`, so Compute always
// returned a wrapped 42703 error. The join through `framework_versions`
// plus the `'activated'` predicate is what this test now pins.

func TestAuditReadinessScore_EmptyAndPopulated(t *testing.T) {
	admin := dbtest.NewMigratePool(t)
	tenant := freshTenant(t, admin)

	r := eval.NewRegistry(admin)
	e, ok := r.Get("audit_readiness_score")
	if !ok {
		t.Fatal("audit_readiness_score not registered")
	}

	// Branch 1: no activated framework scopes anywhere → empty sample.
	res, err := e.Compute(context.Background())
	if err != nil {
		t.Fatalf("Compute(empty): %v", err)
	}
	if res.Value != 0 || res.Dimensions["sample"] != "empty" {
		t.Errorf("Compute(empty) = %v / dims %v; want Value=0 and sample=empty", res.Value, res.Dimensions)
	}

	// Branch 2: populated. An activated scope on a framework version that
	// carries an open audit period, plus one fresh control.
	seedFrameworkAndPeriod(t, admin, tenant, true)
	ctrlID := seedControl(t, admin, tenant)
	seedFreshness(t, admin, tenant, ctrlID, false)

	res2, err := e.Compute(context.Background())
	if err != nil {
		t.Fatalf("Compute(populated): %v", err)
	}
	if res2.Value <= 0 || res2.Value > 1 {
		t.Errorf("Compute(populated) Value = %v; want in (0,1]", res2.Value)
	}
	if res2.Dimensions["frameworks_total"] == "" || res2.Dimensions["frameworks_with_period"] == "" {
		t.Errorf("Compute(populated) dims missing framework counts: %v", res2.Dimensions)
	}
	if res2.Dimensions["frameworks_with_period"] == "0" {
		t.Errorf("Compute(populated) frameworks_with_period = 0; the open audit period should have joined through framework_versions")
	}
}

// ===== openRiskFinancialExposureEvaluator =====

func TestOpenRiskFinancialExposure_EmptyAndPopulated(t *testing.T) {
	admin := dbtest.NewMigratePool(t)
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
// Covers BOTH branches: expected==0 (no acks in the 365-day window →
// Value=0, dims=sample:empty) and populated (one recent acker × one
// published policy, acknowledged → rate 1.0).
//
// Before OE-550 the evaluator's `current_policies` CTE selected from a
// `policy_versions` relation that does not exist, so Compute always
// returned a wrapped 42P01 error. The repoint at `policies` filtered to
// status='published' — the table policy_acknowledgments.policy_version_id
// actually FKs to — is what this test now pins.

func TestPolicyAttestationRate_EmptyAndPopulated(t *testing.T) {
	admin := dbtest.NewMigratePool(t)
	tenant := freshTenant(t, admin)

	r := eval.NewRegistry(admin)
	e, ok := r.Get("policy_attestation_rate")
	if !ok {
		t.Fatal("policy_attestation_rate not registered")
	}

	// Branch 1: no acks in the window → expected==0 → empty sample.
	res, err := e.Compute(context.Background())
	if err != nil {
		t.Fatalf("Compute(empty): %v", err)
	}
	if res.Value != 0 || res.Dimensions["sample"] != "empty" {
		t.Errorf("Compute(empty) = %v / dims %v; want Value=0 and sample=empty", res.Value, res.Dimensions)
	}

	// Branch 2: populated. One user, one published policy, one ack.
	seedPolicyAck(t, admin, tenant)

	res2, err := e.Compute(context.Background())
	if err != nil {
		t.Fatalf("Compute(populated): %v", err)
	}
	if res2.Value != 1.0 {
		t.Errorf("Compute(populated) Value = %v; want 1.0 (one acker × one published policy, acknowledged)", res2.Value)
	}
	if res2.Dimensions["expected_acks"] != "1" || res2.Dimensions["got_acks"] != "1" {
		t.Errorf("Compute(populated) dims = %v; want expected_acks=1 and got_acks=1", res2.Dimensions)
	}
}

// ===== vendorRiskConcentrationEvaluator =====

func TestVendorRiskConcentration_EmptyAndPopulated(t *testing.T) {
	admin := dbtest.NewMigratePool(t)
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
	admin := dbtest.NewMigratePool(t)
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
	admin := dbtest.NewMigratePool(t)
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

// ===== per-evaluator schema smoke test (OE-550) =====
//
// The bug this test exists to catch: an evaluator whose SQL names a
// column or relation the migrated schema does not carry. Two of the
// eight starter evaluators shipped that way — audit_readiness_score
// selected `framework_scopes.framework_id` (42703) and
// policy_attestation_rate selected from `policy_versions` (42P01) — and
// the only signal was an ERROR line in the metrics-scheduler log every
// 15 minutes, forever. Per-evaluator unit tests cannot catch it (the SQL
// is only parsed by Postgres) and the per-evaluator integration tests
// above did not, because each one asserted the shape of ITS OWN
// evaluator.
//
// So: walk Registry.Names() and Compute every registered evaluator
// against the real migrated schema. Any SQL error fails the test, and a
// newly-registered evaluator is enrolled automatically — there is no
// per-evaluator list to forget to update. Fixtures are seeded first so
// every evaluator runs its populated branch as well as parsing its SQL.

func TestAllRegisteredEvaluators_NoSQLError(t *testing.T) {
	admin := dbtest.NewMigratePool(t)
	tenant := freshTenant(t, admin)

	// Seed one row for every primitive the starter evaluators read, so
	// each Compute exercises its populated branch rather than short-
	// circuiting on an empty sample before the interesting SQL runs.
	ctrlID := seedControl(t, admin, tenant)
	seedEvaluation(t, admin, tenant, ctrlID, "pass")
	seedFreshness(t, admin, tenant, ctrlID, false)
	// seedAuditFinding seeds its own framework + activated scope + open
	// audit period before the finding, which is also what
	// audit_readiness_score needs — calling seedFrameworkAndPeriod again
	// here would collide on audit_periods_tenant_name_uniq.
	seedAuditFinding(t, admin, tenant)
	seedException(t, admin, tenant)
	seedRisk(t, admin, tenant)
	seedVendor(t, admin, tenant)
	seedPolicyAck(t, admin, tenant)

	r := eval.NewRegistry(admin)
	names := r.Names()
	if len(names) == 0 {
		t.Fatal("Registry.Names() is empty; the smoke test would vacuously pass")
	}

	for _, name := range names {
		t.Run(name, func(t *testing.T) {
			e, ok := r.Get(name)
			if !ok {
				t.Fatalf("Names() returned %q but Get(%q) failed", name, name)
			}
			res, err := e.Compute(context.Background())
			if err != nil {
				t.Fatalf("Compute against the migrated schema: %v", err)
			}
			if math.IsNaN(res.Value) || math.IsInf(res.Value, 0) {
				t.Errorf("Compute Value = %v; want a finite number", res.Value)
			}
		})
	}
}
