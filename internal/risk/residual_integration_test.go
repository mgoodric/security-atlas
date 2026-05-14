//go:build integration

// Integration tests for slice 020: risk-control linkage + residual-risk
// derivation. Real Postgres only — RLS, the risk_control_links weight columns
// (migration `_029`), and the residual derivation reading slice 012's
// evaluation ledger are only meaningful against a real database. The DB is
// never mocked.
//
// Run with: go test -tags=integration -race ./internal/risk/...
//
// Required env:
//   DATABASE_URL      - migration role DSN (BYPASSRLS); seeds controls +
//                       evidence + scope cells outside the GUC.
//   DATABASE_URL_APP  - application role DSN (NOSUPERUSER NOBYPASSRLS); the
//                       risk.Store + eval.Engine run against this so RLS is
//                       enforced.
//
// These tests share the harness helpers (appDSN/adminDSN/openPool/
// freshTenant/seedControl/ctxFor) declared in integration_test.go.

package risk_test

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/mgoodric/security-atlas/internal/eval"
	"github.com/mgoodric/security-atlas/internal/risk"
	"github.com/mgoodric/security-atlas/internal/scope"
)

// ----- slice-020 seed helpers -----

// seedEvalControl inserts a control configured for evidence-driven evaluation
// (automated, monthly freshness). The plain seedControl in integration_test.go
// is sufficient for link-existence tests; this one carries the columns
// eval.Engine.EvaluateControl needs.
func seedEvalControl(t *testing.T, admin *pgxpool.Pool, tenant string) uuid.UUID {
	t.Helper()
	ctrlID := uuid.New()
	bundleID := "slice-020-test-bundle-" + ctrlID.String()
	if _, err := admin.Exec(context.Background(), `
		INSERT INTO controls (
			id, tenant_id, title, control_family, implementation_type,
			bundle_id, freshness_class, evidence_queries, applicability_expr
		)
		VALUES ($1, $2, 'slice 020 test control', 'AAA', 'automated',
		        $3, 'monthly', '[]'::jsonb, 'true')
	`, ctrlID, tenant, bundleID); err != nil {
		t.Fatalf("seed eval control: %v", err)
	}
	return ctrlID
}

// seedEvidence inserts one evidence_records row. The eval engine reads these;
// the residual deriver never touches them (invariant 2).
func seedEvidence(t *testing.T, admin *pgxpool.Pool, tenant string, ctrlID uuid.UUID, result string, observedAt time.Time) {
	t.Helper()
	id := uuid.New()
	controlRef := ctrlID.String()
	if _, err := admin.Exec(context.Background(), `
		INSERT INTO evidence_records (
			id, tenant_id, control_id, observed_at, ingested_at,
			provenance, result, payload, hash, control_ref
		)
		VALUES ($1, $2, $3, $4, now(), '{}'::jsonb, $5, '{}'::jsonb, $6, $7)
	`, id, tenant, ctrlID, observedAt, result, "hash-020-"+id.String()[:8], controlRef); err != nil {
		t.Fatalf("seed evidence: %v", err)
	}
}

// seedNistRisk inserts a nist_800_30 risk with the given (likelihood, impact).
// inherent scalar = likelihood * impact.
func seedNistRisk(t *testing.T, admin *pgxpool.Pool, tenant string, likelihood, impact int) uuid.UUID {
	t.Helper()
	riskID := uuid.New()
	inherent, _ := json.Marshal(map[string]int{"likelihood": likelihood, "impact": impact})
	if _, err := admin.Exec(context.Background(), `
		INSERT INTO risks (
			id, tenant_id, title, description, category, methodology,
			inherent_score, treatment, treatment_owner, residual_score
		)
		VALUES ($1, $2, 'slice 020 test risk', '', 'operational', 'nist_800_30',
		        $3::jsonb, 'avoid', 'owner', '{}'::jsonb)
	`, riskID, tenant, string(inherent)); err != nil {
		t.Fatalf("seed nist risk: %v", err)
	}
	return riskID
}

func newDeriver(app *pgxpool.Pool) *risk.ResidualDeriver {
	engine := eval.NewEngine(eval.NewStore(app), scope.NewStore(app))
	return risk.NewResidualDeriver(risk.NewStore(app), engine)
}

// ===== AC-7 / ISC-39, ISC-40: risk with no linked controls =====

func TestResidual_NoLinkedControlsWarnsAndEqualsInherent(t *testing.T) {
	admin := openPool(t, adminDSN(t))
	defer admin.Close()
	app := openPool(t, appDSN(t))
	defer app.Close()
	tenant := freshTenant(t, admin)
	ctx := ctxFor(t, tenant)

	riskID := seedNistRisk(t, admin, tenant, 4, 4) // inherent 16
	deriver := newDeriver(app)

	res, err := deriver.Derive(ctx, riskID, false)
	if err != nil {
		t.Fatalf("Derive: %v", err)
	}
	if res.Warning != risk.WarningNoControlsLinked {
		t.Fatalf("ISC-40: expected warning %q, got %q", risk.WarningNoControlsLinked, res.Warning)
	}
	if res.ResidualScore != res.InherentScore {
		t.Fatalf("ISC-39: residual %v != inherent %v", res.ResidualScore, res.InherentScore)
	}
	if res.InherentScore != 16 {
		t.Fatalf("inherent scalar = %v, want 16", res.InherentScore)
	}
	if len(res.Breakdown) != 0 {
		t.Fatalf("expected empty breakdown, got %d entries", len(res.Breakdown))
	}
}

// ===== ISC-41: risk with >=1 linked control has no warning =====

func TestResidual_LinkedControlClearsWarning(t *testing.T) {
	admin := openPool(t, adminDSN(t))
	defer admin.Close()
	app := openPool(t, appDSN(t))
	defer app.Close()
	tenant := freshTenant(t, admin)
	ctx := ctxFor(t, tenant)

	riskID := seedNistRisk(t, admin, tenant, 3, 3)
	ctrlID := seedEvalControl(t, admin, tenant)
	store := risk.NewStore(app)
	if err := store.LinkControl(ctx, risk.LinkControlInput{RiskID: riskID, ControlID: ctrlID}); err != nil {
		t.Fatalf("LinkControl: %v", err)
	}

	res, err := newDeriver(app).Derive(ctx, riskID, false)
	if err != nil {
		t.Fatalf("Derive: %v", err)
	}
	if res.Warning != "" {
		t.Fatalf("ISC-41: expected no warning, got %q", res.Warning)
	}
	if len(res.Breakdown) != 1 {
		t.Fatalf("ISC-41: expected 1 breakdown entry, got %d", len(res.Breakdown))
	}
}

// ===== AC-3 / AC-4 / ISC-24..28: effectiveness breakdown components =====

func TestResidual_BreakdownReflectsOperationalPassRate(t *testing.T) {
	admin := openPool(t, adminDSN(t))
	defer admin.Close()
	app := openPool(t, appDSN(t))
	defer app.Close()
	tenant := freshTenant(t, admin)
	ctx := ctxFor(t, tenant)

	riskID := seedNistRisk(t, admin, tenant, 5, 5) // inherent 25
	ctrlID := seedEvalControl(t, admin, tenant)
	store := risk.NewStore(app)
	// Link with explicit weights: design 1.0, weights 0.0/1.0/0.0 so the
	// composite effectiveness is purely the operational pass rate. That
	// isolates AC-4 — the breakdown's control_effectiveness must equal the
	// slice-012 rolling pass rate.
	if err := store.LinkControl(ctx, risk.LinkControlInput{
		RiskID:          riskID,
		ControlID:       ctrlID,
		DesignScore:     1.0,
		DesignScoreSet:  true,
		WeightDesign:    0.0,
		WeightOperation: 1.0,
		WeightCoverage:  0.0,
		WeightsSet:      true,
	}); err != nil {
		t.Fatalf("LinkControl: %v", err)
	}

	engine := eval.NewEngine(eval.NewStore(app), scope.NewStore(app))
	// Two evaluation runs: pass, then fail. Rolling pass rate = 1/2 = 0.5.
	seedEvidence(t, admin, tenant, ctrlID, "pass", time.Now().UTC().Add(-2*24*time.Hour))
	if _, err := engine.EvaluateControl(ctx, ctrlID, eval.TriggerManual, eval.FarFuture); err != nil {
		t.Fatalf("EvaluateControl run 1: %v", err)
	}
	seedEvidence(t, admin, tenant, ctrlID, "fail", time.Now().UTC().Add(-1*24*time.Hour))
	if _, err := engine.EvaluateControl(ctx, ctrlID, eval.TriggerManual, eval.FarFuture); err != nil {
		t.Fatalf("EvaluateControl run 2: %v", err)
	}

	res, err := risk.NewResidualDeriver(store, engine).Derive(ctx, riskID, false)
	if err != nil {
		t.Fatalf("Derive: %v", err)
	}
	if len(res.Breakdown) != 1 {
		t.Fatalf("expected 1 breakdown entry, got %d", len(res.Breakdown))
	}
	bd := res.Breakdown[0]
	// AC-4 / ISC-27: operational_score equals the slice-012 rolling pass rate.
	if bd.OperationalScore < 0.49 || bd.OperationalScore > 0.51 {
		t.Fatalf("AC-4: operational_score = %v, want ~0.5", bd.OperationalScore)
	}
	// With weights 0/1/0, control_effectiveness == operational_score.
	if bd.ControlEffectiveness < 0.49 || bd.ControlEffectiveness > 0.51 {
		t.Fatalf("AC-3: control_effectiveness = %v, want ~0.5", bd.ControlEffectiveness)
	}
	// ISC-25 / ISC-26: every component + the composite are present.
	if bd.ControlID != ctrlID {
		t.Fatalf("breakdown control id mismatch")
	}
}

// ===== ISC-28: control with no evaluations -> operational no_data =====

func TestResidual_NoEvaluationsFlagsNoData(t *testing.T) {
	admin := openPool(t, adminDSN(t))
	defer admin.Close()
	app := openPool(t, appDSN(t))
	defer app.Close()
	tenant := freshTenant(t, admin)
	ctx := ctxFor(t, tenant)

	riskID := seedNistRisk(t, admin, tenant, 2, 2)
	ctrlID := seedEvalControl(t, admin, tenant)
	store := risk.NewStore(app)
	if err := store.LinkControl(ctx, risk.LinkControlInput{RiskID: riskID, ControlID: ctrlID}); err != nil {
		t.Fatalf("LinkControl: %v", err)
	}

	res, err := newDeriver(app).Derive(ctx, riskID, false)
	if err != nil {
		t.Fatalf("Derive: %v", err)
	}
	if len(res.Breakdown) != 1 {
		t.Fatalf("expected 1 breakdown entry, got %d", len(res.Breakdown))
	}
	if !res.Breakdown[0].OperationalNoData {
		t.Fatalf("ISC-28: expected operational_no_data=true for a control with no evaluations")
	}
	if res.Breakdown[0].OperationalScore != 0 {
		t.Fatalf("ISC-28: operational_score should be 0 when no data, got %v", res.Breakdown[0].OperationalScore)
	}
}

// ===== AC-6 / ISC-37, ISC-38: control flip moves residual =====

func TestResidual_ControlFlipPassToFailRaisesResidual(t *testing.T) {
	admin := openPool(t, adminDSN(t))
	defer admin.Close()
	app := openPool(t, appDSN(t))
	defer app.Close()
	tenant := freshTenant(t, admin)
	ctx := ctxFor(t, tenant)

	riskID := seedNistRisk(t, admin, tenant, 5, 5) // inherent 25
	ctrlID := seedEvalControl(t, admin, tenant)
	store := risk.NewStore(app)
	// Weights isolate operational so the flip is fully visible.
	if err := store.LinkControl(ctx, risk.LinkControlInput{
		RiskID:          riskID,
		ControlID:       ctrlID,
		DesignScore:     0.0,
		DesignScoreSet:  true,
		WeightDesign:    0.0,
		WeightOperation: 1.0,
		WeightCoverage:  0.0,
		WeightsSet:      true,
	}); err != nil {
		t.Fatalf("LinkControl: %v", err)
	}
	engine := eval.NewEngine(eval.NewStore(app), scope.NewStore(app))
	deriver := risk.NewResidualDeriver(store, engine)

	// All-passing evidence -> operational 1.0 -> residual 0.
	seedEvidence(t, admin, tenant, ctrlID, "pass", time.Now().UTC().Add(-1*24*time.Hour))
	if _, err := engine.EvaluateControl(ctx, ctrlID, eval.TriggerManual, eval.FarFuture); err != nil {
		t.Fatalf("EvaluateControl pass: %v", err)
	}
	before, err := deriver.Derive(ctx, riskID, false)
	if err != nil {
		t.Fatalf("Derive before: %v", err)
	}

	// Flip: add a fail record, re-evaluate -> operational 0.5 -> residual rises.
	seedEvidence(t, admin, tenant, ctrlID, "fail", time.Now().UTC().Add(-1*time.Hour))
	if _, err := engine.EvaluateControl(ctx, ctrlID, eval.TriggerManual, eval.FarFuture); err != nil {
		t.Fatalf("EvaluateControl fail: %v", err)
	}
	after, err := deriver.Derive(ctx, riskID, false)
	if err != nil {
		t.Fatalf("Derive after: %v", err)
	}

	if !(after.ResidualScore > before.ResidualScore) {
		t.Fatalf("AC-6 / ISC-37: residual did not rise after pass->fail flip: before=%v after=%v",
			before.ResidualScore, after.ResidualScore)
	}
}

// ===== ISC-11 / ISC-23: DeriveAndPersist writes residual_score JSONB =====

func TestResidual_DeriveAndPersistWritesResidualScore(t *testing.T) {
	admin := openPool(t, adminDSN(t))
	defer admin.Close()
	app := openPool(t, appDSN(t))
	defer app.Close()
	tenant := freshTenant(t, admin)
	ctx := ctxFor(t, tenant)

	riskID := seedNistRisk(t, admin, tenant, 4, 4) // inherent 16
	deriver := newDeriver(app)

	if _, err := deriver.DeriveAndPersist(ctx, riskID, false); err != nil {
		t.Fatalf("DeriveAndPersist: %v", err)
	}
	// Read residual_score straight from the DB to confirm the write landed.
	var blob []byte
	if err := admin.QueryRow(context.Background(),
		`SELECT residual_score FROM risks WHERE tenant_id = $1 AND id = $2`,
		tenant, riskID).Scan(&blob); err != nil {
		t.Fatalf("read residual_score: %v", err)
	}
	var persisted struct {
		Score   float64 `json:"score"`
		Warning string  `json:"warning"`
	}
	if err := json.Unmarshal(blob, &persisted); err != nil {
		t.Fatalf("unmarshal residual_score: %v", err)
	}
	// No linked controls -> residual equals inherent (16), warning set.
	if persisted.Score != 16 {
		t.Fatalf("ISC-23: persisted residual score = %v, want 16", persisted.Score)
	}
	if persisted.Warning != risk.WarningNoControlsLinked {
		t.Fatalf("ISC-23: persisted warning = %q, want %q", persisted.Warning, risk.WarningNoControlsLinked)
	}
}

// ===== AC-1 / ISC-19, ISC-20: link errors =====

func TestLinkControl_UnknownControlIsNotFound(t *testing.T) {
	admin := openPool(t, adminDSN(t))
	defer admin.Close()
	app := openPool(t, appDSN(t))
	defer app.Close()
	tenant := freshTenant(t, admin)
	ctx := ctxFor(t, tenant)

	riskID := seedNistRisk(t, admin, tenant, 1, 1)
	store := risk.NewStore(app)
	err := store.LinkControl(ctx, risk.LinkControlInput{
		RiskID:    riskID,
		ControlID: uuid.New(), // does not exist
	})
	if err != risk.ErrControlNotFound {
		t.Fatalf("ISC-19: expected ErrControlNotFound, got %v", err)
	}
}

func TestLinkControl_UnknownRiskIsNotFound(t *testing.T) {
	admin := openPool(t, adminDSN(t))
	defer admin.Close()
	app := openPool(t, appDSN(t))
	defer app.Close()
	tenant := freshTenant(t, admin)
	ctx := ctxFor(t, tenant)

	ctrlID := seedEvalControl(t, admin, tenant)
	store := risk.NewStore(app)
	err := store.LinkControl(ctx, risk.LinkControlInput{
		RiskID:    uuid.New(), // does not exist
		ControlID: ctrlID,
	})
	if err != risk.ErrNotFound {
		t.Fatalf("ISC-20: expected ErrNotFound, got %v", err)
	}
}

// ===== ISC-18: linking is idempotent =====

func TestLinkControl_IdempotentRelink(t *testing.T) {
	admin := openPool(t, adminDSN(t))
	defer admin.Close()
	app := openPool(t, appDSN(t))
	defer app.Close()
	tenant := freshTenant(t, admin)
	ctx := ctxFor(t, tenant)

	riskID := seedNistRisk(t, admin, tenant, 3, 3)
	ctrlID := seedEvalControl(t, admin, tenant)
	store := risk.NewStore(app)

	in := risk.LinkControlInput{RiskID: riskID, ControlID: ctrlID}
	if err := store.LinkControl(ctx, in); err != nil {
		t.Fatalf("LinkControl first: %v", err)
	}
	if err := store.LinkControl(ctx, in); err != nil {
		t.Fatalf("ISC-18: re-link should be idempotent, got %v", err)
	}
	rk, err := store.Get(ctx, riskID)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if len(rk.LinkedControlIDs) != 1 {
		t.Fatalf("ISC-18: expected exactly 1 link after re-link, got %d", len(rk.LinkedControlIDs))
	}
}

// ===== ISC-42: risk_control_links weight rows are tenant-isolated =====

func TestLinkControl_WeightsAreTenantIsolated(t *testing.T) {
	admin := openPool(t, adminDSN(t))
	defer admin.Close()
	app := openPool(t, appDSN(t))
	defer app.Close()
	tenantA := freshTenant(t, admin)
	tenantB := freshTenant(t, admin)

	riskA := seedNistRisk(t, admin, tenantA, 4, 4)
	ctrlA := seedEvalControl(t, admin, tenantA)
	store := risk.NewStore(app)
	if err := store.LinkControl(ctxFor(t, tenantA), risk.LinkControlInput{
		RiskID: riskA, ControlID: ctrlA,
		DesignScore: 0.9, DesignScoreSet: true,
	}); err != nil {
		t.Fatalf("LinkControl tenant A: %v", err)
	}
	// Tenant B cannot see tenant A's risk at all (RLS) — Get returns NotFound.
	if _, err := store.Get(ctxFor(t, tenantB), riskA); err != risk.ErrNotFound {
		t.Fatalf("ISC-42: tenant B should not see tenant A's risk, got %v", err)
	}
	// And the deriver for tenant B treats riskA as not found.
	if _, err := newDeriver(app).Derive(ctxFor(t, tenantB), riskA, false); err != risk.ErrNotFound {
		t.Fatalf("ISC-42: tenant B deriver should not see tenant A's risk, got %v", err)
	}
}
