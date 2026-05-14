//go:build integration

// Slice 054 — integration tests for the declarative aggregation rules
// engine. Real Postgres only — RLS, the append-only audit/eval ledgers, the
// idempotency key lookup, and the candidate-risk query cannot be exercised
// against a fake DB.
//
// Run with:
//
//	go test -tags=integration -race ./internal/risk/aggrule/...
//
// Requires DATABASE_URL_APP (atlas_app role) and DATABASE_URL (postgres /
// admin role for fixture seeding).
//
// Coverage:
//
//	ISC-17  Evaluate fires only on active rules
//	ISC-18  one meta-risk per (rule_id, window_start); re-run updates
//	ISC-19  candidate read excludes rule_generated meta-risks (cycle prevention)
//	ISC-20  exactly one aggregation_rule_evaluations row per active rule per cycle
//	ISC-21  closing a child does not close the parent; severity recomputes lower
//	ISC-22  re-activation does not re-fire on pre-activation risks
//	ISC-23  engine writer is a narrow interface (compile-time + behavioural)
//	ISC-30  AC-7 E2E flow

package aggrule_test

import (
	"context"
	"encoding/json"
	"os"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/mgoodric/security-atlas/internal/risk/aggrule"
	"github.com/mgoodric/security-atlas/internal/tenancy"
)

// ----- harness -----

func appDSN(t *testing.T) string {
	t.Helper()
	v := os.Getenv("DATABASE_URL_APP")
	if v == "" {
		t.Skip("DATABASE_URL_APP not set; skipping integration test")
	}
	return v
}

func adminDSN(t *testing.T) string {
	t.Helper()
	v := os.Getenv("DATABASE_URL")
	if v == "" {
		t.Skip("DATABASE_URL not set; skipping integration test")
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

func freshTenant(t *testing.T, admin *pgxpool.Pool) string {
	t.Helper()
	tenant := uuid.NewString()
	t.Cleanup(func() {
		ctx := context.Background()
		for _, stmt := range []string{
			`DELETE FROM aggregation_rule_evaluations WHERE tenant_id = $1`,
			`DELETE FROM aggregation_rule_audit_log WHERE tenant_id = $1`,
			`DELETE FROM aggregation_rules WHERE tenant_id = $1`,
			`DELETE FROM risk_aggregations WHERE tenant_id = $1`,
			`DELETE FROM risks WHERE tenant_id = $1`,
			`DELETE FROM org_units WHERE tenant_id = $1`,
		} {
			if _, err := admin.Exec(ctx, stmt, tenant); err != nil {
				t.Logf("cleanup %s: %v", stmt, err)
			}
		}
	})
	return tenant
}

func ctxFor(t *testing.T, tenant string) context.Context {
	t.Helper()
	ctx, err := tenancy.WithTenant(context.Background(), tenant)
	if err != nil {
		t.Fatalf("WithTenant: %v", err)
	}
	return ctx
}

// seedOrgUnit inserts a team-level org unit and returns its id.
func seedOrgUnit(t *testing.T, admin *pgxpool.Pool, tenant, name string) uuid.UUID {
	t.Helper()
	id := uuid.New()
	_, err := admin.Exec(context.Background(), `
		INSERT INTO org_units (id, tenant_id, name, parent_id, level, acceptance_authorities)
		VALUES ($1, $2, $3, NULL, 'team', '[]'::jsonb)`, id, tenant, name)
	if err != nil {
		t.Fatalf("seed org unit: %v", err)
	}
	return id
}

// seedRisk inserts an aggregation-eligible risk (nist_800_30, integer
// likelihood+impact) carrying the given themes and optional org_unit.
// createdAt lets a test place a risk before/after a rule's activation.
func seedRisk(t *testing.T, admin *pgxpool.Pool, tenant, title string, likelihood, impact int, themes []string, orgUnit *uuid.UUID, createdAt time.Time) uuid.UUID {
	t.Helper()
	id := uuid.New()
	if themes == nil {
		themes = []string{}
	}
	score, _ := json.Marshal(map[string]int{"likelihood": likelihood, "impact": impact})
	_, err := admin.Exec(context.Background(), `
		INSERT INTO risks (
			id, tenant_id, title, category, methodology, inherent_score,
			treatment, treatment_owner, residual_score, accepter,
			instrument_reference, level, org_unit_id, themes, created_at
		) VALUES (
			$1, $2, $3, 'operational', 'nist_800_30', $4,
			'avoid', '', '{}', '', '', 'team', $5, $6, $7
		)`, id, tenant, title, score, orgUnit, themes, createdAt)
	if err != nil {
		t.Fatalf("seed risk: %v", err)
	}
	return id
}

// deleteRisk removes a risk (simulating a "close"/resolve — the
// risk_aggregations row cascades).
func deleteRisk(t *testing.T, admin *pgxpool.Pool, tenant string, id uuid.UUID) {
	t.Helper()
	if _, err := admin.Exec(context.Background(),
		`DELETE FROM risks WHERE tenant_id = $1 AND id = $2`, tenant, id); err != nil {
		t.Fatalf("delete risk: %v", err)
	}
}

// makeRule builds a valid Rule for the tests below.
func makeRule(ruleID, theme string, minRisks, minTeams, windowDays int, fn string) aggrule.Rule {
	return aggrule.Rule{
		RuleID:           ruleID,
		TargetTheme:      theme,
		MinRisks:         minRisks,
		MinTeams:         minTeams,
		WindowDays:       windowDays,
		ParentLevel:      "org",
		SeverityFunction: fn,
		TitleTemplate:    "Cross-team {theme} pattern",
	}
}

// createAndActivate persists a rule and flips it to active, returning the
// stored rule.
func createAndActivate(t *testing.T, store *aggrule.Store, ctx context.Context, r aggrule.Rule) aggrule.StoredRule {
	t.Helper()
	created, err := store.Create(ctx, r, "tester")
	if err != nil {
		t.Fatalf("Create rule %s: %v", r.RuleID, err)
	}
	if created.Status != "staged" {
		t.Fatalf("new rule status: got %q, want staged", created.Status)
	}
	active, err := store.Activate(ctx, created.ID, "reviewer")
	if err != nil {
		t.Fatalf("Activate rule %s: %v", r.RuleID, err)
	}
	if active.Status != "active" {
		t.Fatalf("activated rule status: got %q, want active", active.Status)
	}
	return active
}

// metaRiskCount counts rule-generated meta-risks for the tenant.
func metaRiskCount(t *testing.T, admin *pgxpool.Pool, tenant string) int {
	t.Helper()
	var n int
	if err := admin.QueryRow(context.Background(),
		`SELECT count(*) FROM risks WHERE tenant_id = $1
		   AND (inherent_score->>'rule_generated')::bool = true`, tenant).Scan(&n); err != nil {
		t.Fatalf("count meta-risks: %v", err)
	}
	return n
}

// ===== ISC-17: Evaluate fires only on active rules =====

func TestEngine_OnlyActiveRulesFire_ISC17(t *testing.T) {
	app := openPool(t, appDSN(t))
	admin := openPool(t, adminDSN(t))
	tenant := freshTenant(t, admin)
	ctx := ctxFor(t, tenant)
	store := aggrule.NewStore(app)

	team1 := seedOrgUnit(t, admin, tenant, "team-1")
	team2 := seedOrgUnit(t, admin, tenant, "team-2")
	now := time.Now().UTC().Add(5 * time.Second)
	seedRisk(t, admin, tenant, "r1", 3, 3, []string{"ownership"}, &team1, now)
	seedRisk(t, admin, tenant, "r2", 4, 3, []string{"ownership"}, &team1, now)
	seedRisk(t, admin, tenant, "r3", 3, 3, []string{"ownership"}, &team2, now)

	// A STAGED rule must not fire even though the data would satisfy it.
	staged, err := store.Create(ctx, makeRule("staged-rule", "ownership", 3, 2, 90, "max"), "tester")
	if err != nil {
		t.Fatalf("Create staged rule: %v", err)
	}
	evals, err := store.Evaluate(ctx, []string{"ownership"})
	if err != nil {
		t.Fatalf("Evaluate (staged only): %v", err)
	}
	if len(evals) != 0 {
		t.Fatalf("staged rule produced %d evaluations, want 0 (staged rules never run)", len(evals))
	}
	if n := metaRiskCount(t, admin, tenant); n != 0 {
		t.Fatalf("staged rule created %d meta-risks, want 0", n)
	}

	// Activate it — now it fires.
	if _, err := store.Activate(ctx, staged.ID, "reviewer"); err != nil {
		t.Fatalf("Activate: %v", err)
	}
	evals, err = store.Evaluate(ctx, []string{"ownership"})
	if err != nil {
		t.Fatalf("Evaluate (active): %v", err)
	}
	if len(evals) != 1 || evals[0].Outcome != "fired" {
		t.Fatalf("active rule: got %d evals, outcome %v; want 1 fired", len(evals), evalOutcomes(evals))
	}

	// Deactivate — stops firing again.
	if _, err := store.Deactivate(ctx, staged.ID, "reviewer"); err != nil {
		t.Fatalf("Deactivate: %v", err)
	}
	evals, err = store.Evaluate(ctx, []string{"ownership"})
	if err != nil {
		t.Fatalf("Evaluate (inactive): %v", err)
	}
	if len(evals) != 0 {
		t.Fatalf("inactive rule produced %d evaluations, want 0", len(evals))
	}
}

// ===== ISC-18: one meta-risk per (rule_id, window_start); re-run updates =====

func TestEngine_WindowIdempotency_ISC18(t *testing.T) {
	app := openPool(t, appDSN(t))
	admin := openPool(t, adminDSN(t))
	tenant := freshTenant(t, admin)
	ctx := ctxFor(t, tenant)
	store := aggrule.NewStore(app)

	team1 := seedOrgUnit(t, admin, tenant, "team-1")
	team2 := seedOrgUnit(t, admin, tenant, "team-2")
	now := time.Now().UTC().Add(5 * time.Second)
	seedRisk(t, admin, tenant, "r1", 3, 3, []string{"ownership"}, &team1, now)
	seedRisk(t, admin, tenant, "r2", 4, 3, []string{"ownership"}, &team1, now)
	seedRisk(t, admin, tenant, "r3", 3, 3, []string{"ownership"}, &team2, now)

	rule := createAndActivate(t, store, ctx, makeRule("ownership-cross-team", "ownership", 3, 2, 90, "max"))

	// First evaluation fires and creates one meta-risk.
	evals, err := store.Evaluate(ctx, []string{"ownership"})
	if err != nil {
		t.Fatalf("Evaluate #1: %v", err)
	}
	if len(evals) != 1 || evals[0].Outcome != "fired" {
		t.Fatalf("eval #1: want 1 fired, got %v", evalOutcomes(evals))
	}
	firstMeta := evals[0].MetaRiskID
	if firstMeta == nil {
		t.Fatal("eval #1: fired evaluation has nil MetaRiskID")
	}
	if n := metaRiskCount(t, admin, tenant); n != 1 {
		t.Fatalf("after eval #1: %d meta-risks, want 1", n)
	}

	// Add a fourth risk in the SAME window, re-evaluate. Must UPDATE the
	// existing meta-risk, not create a second.
	seedRisk(t, admin, tenant, "r4", 5, 5, []string{"ownership"}, &team2, now)
	evals, err = store.Evaluate(ctx, []string{"ownership"})
	if err != nil {
		t.Fatalf("Evaluate #2: %v", err)
	}
	if len(evals) != 1 || evals[0].Outcome != "fired" {
		t.Fatalf("eval #2: want 1 fired, got %v", evalOutcomes(evals))
	}
	if evals[0].MetaRiskID == nil || *evals[0].MetaRiskID != *firstMeta {
		t.Fatalf("eval #2: meta-risk id changed; idempotency broken (was %v, now %v)",
			firstMeta, evals[0].MetaRiskID)
	}
	if n := metaRiskCount(t, admin, tenant); n != 1 {
		t.Fatalf("after eval #2: %d meta-risks, want 1 (window idempotency)", n)
	}
	if evals[0].RiskCount != 4 {
		t.Fatalf("eval #2: RiskCount %d, want 4 (new child joined the window)", evals[0].RiskCount)
	}
	_ = rule
}

// ===== ISC-19: candidate read excludes rule_generated meta-risks =====

func TestEngine_CycleExclusion_ISC19(t *testing.T) {
	app := openPool(t, appDSN(t))
	admin := openPool(t, adminDSN(t))
	tenant := freshTenant(t, admin)
	ctx := ctxFor(t, tenant)
	store := aggrule.NewStore(app)

	team1 := seedOrgUnit(t, admin, tenant, "team-1")
	team2 := seedOrgUnit(t, admin, tenant, "team-2")
	now := time.Now().UTC().Add(5 * time.Second)
	seedRisk(t, admin, tenant, "r1", 3, 3, []string{"ownership"}, &team1, now)
	seedRisk(t, admin, tenant, "r2", 4, 3, []string{"ownership"}, &team1, now)
	seedRisk(t, admin, tenant, "r3", 3, 3, []string{"ownership"}, &team2, now)

	createAndActivate(t, store, ctx, makeRule("ownership-cross-team", "ownership", 3, 2, 90, "max"))

	// Fire once — produces a meta-risk that carries the union of child
	// themes (which includes "ownership").
	if _, err := store.Evaluate(ctx, []string{"ownership"}); err != nil {
		t.Fatalf("Evaluate #1: %v", err)
	}

	// Confirm the meta-risk carries "ownership" — i.e. it WOULD be a
	// candidate were it not for the rule_generated exclusion.
	var metaThemes []string
	if err := admin.QueryRow(context.Background(),
		`SELECT themes FROM risks WHERE tenant_id = $1
		   AND (inherent_score->>'rule_generated')::bool = true`, tenant).Scan(&metaThemes); err != nil {
		t.Fatalf("read meta-risk themes: %v", err)
	}
	if !contains(metaThemes, "ownership") {
		t.Fatalf("meta-risk themes %v do not include target_theme — test premise invalid", metaThemes)
	}

	// Re-evaluate repeatedly. If cycle exclusion were broken, the
	// meta-risk would become a candidate and the engine would create a
	// SECOND meta-risk (and then a third...). With exclusion, the count
	// stays at exactly 1.
	for i := 0; i < 3; i++ {
		if _, err := store.Evaluate(ctx, []string{"ownership"}); err != nil {
			t.Fatalf("Evaluate re-run %d: %v", i, err)
		}
	}
	if n := metaRiskCount(t, admin, tenant); n != 1 {
		t.Fatalf("after repeated evaluation: %d meta-risks, want 1 (cycle exclusion failed)", n)
	}

	// And the RiskCount on the latest evaluation must still be 3 — the
	// meta-risk itself was never counted as a child.
	evals, err := store.Evaluate(ctx, []string{"ownership"})
	if err != nil {
		t.Fatalf("Evaluate final: %v", err)
	}
	if len(evals) != 1 || evals[0].RiskCount != 3 {
		t.Fatalf("final eval RiskCount: got %v, want 3 (meta-risk excluded)", evalCounts(evals))
	}
}

// ===== ISC-20: exactly one aggregation_rule_evaluations row per active rule per cycle =====

func TestEngine_OneEvaluationRowPerRulePerCycle_ISC20(t *testing.T) {
	app := openPool(t, appDSN(t))
	admin := openPool(t, adminDSN(t))
	tenant := freshTenant(t, admin)
	ctx := ctxFor(t, tenant)
	store := aggrule.NewStore(app)

	team1 := seedOrgUnit(t, admin, tenant, "team-1")
	now := time.Now().UTC().Add(5 * time.Second)
	// Only TWO risks on ONE team — below both thresholds of the rule
	// below, so the outcome is no_match. AC-8: a no_match STILL writes a
	// ledger row.
	seedRisk(t, admin, tenant, "r1", 3, 3, []string{"ownership"}, &team1, now)
	seedRisk(t, admin, tenant, "r2", 4, 3, []string{"ownership"}, &team1, now)

	rule := createAndActivate(t, store, ctx, makeRule("ownership-cross-team", "ownership", 3, 2, 90, "max"))

	evals, err := store.Evaluate(ctx, []string{"ownership"})
	if err != nil {
		t.Fatalf("Evaluate: %v", err)
	}
	if len(evals) != 1 {
		t.Fatalf("got %d evaluations, want exactly 1", len(evals))
	}
	if evals[0].Outcome == "fired" {
		t.Fatalf("outcome fired, want no_match/near_miss (below threshold)")
	}

	// The ledger has exactly one row for this rule after one cycle.
	ledger, err := store.Evaluations(ctx, rule.ID)
	if err != nil {
		t.Fatalf("Evaluations: %v", err)
	}
	if len(ledger) != 1 {
		t.Fatalf("ledger has %d rows after 1 cycle, want 1 (even for no_match — AC-8)", len(ledger))
	}

	// A second cycle appends exactly one more.
	if _, err := store.Evaluate(ctx, []string{"ownership"}); err != nil {
		t.Fatalf("Evaluate #2: %v", err)
	}
	ledger, err = store.Evaluations(ctx, rule.ID)
	if err != nil {
		t.Fatalf("Evaluations #2: %v", err)
	}
	if len(ledger) != 2 {
		t.Fatalf("ledger has %d rows after 2 cycles, want 2", len(ledger))
	}
}

// ===== ISC-21: closing a child does not close the parent; severity recomputes lower =====

func TestEngine_ChildCloseDoesNotCloseParent_ISC21(t *testing.T) {
	app := openPool(t, appDSN(t))
	admin := openPool(t, adminDSN(t))
	tenant := freshTenant(t, admin)
	ctx := ctxFor(t, tenant)
	store := aggrule.NewStore(app)

	team1 := seedOrgUnit(t, admin, tenant, "team-1")
	team2 := seedOrgUnit(t, admin, tenant, "team-2")
	now := time.Now().UTC().Add(5 * time.Second)
	seedRisk(t, admin, tenant, "r1", 2, 2, []string{"ownership"}, &team1, now)       // sev 4
	seedRisk(t, admin, tenant, "r2", 2, 2, []string{"ownership"}, &team1, now)       // sev 4
	r3 := seedRisk(t, admin, tenant, "r3", 5, 5, []string{"ownership"}, &team2, now) // sev 25

	createAndActivate(t, store, ctx, makeRule("ownership-cross-team", "ownership", 3, 2, 90, "sum"))

	evals, err := store.Evaluate(ctx, []string{"ownership"})
	if err != nil {
		t.Fatalf("Evaluate #1: %v", err)
	}
	if len(evals) != 1 || evals[0].Outcome != "fired" {
		t.Fatalf("eval #1: want 1 fired, got %v", evalOutcomes(evals))
	}
	metaID := *evals[0].MetaRiskID
	sevBefore := metaSeverity(t, admin, tenant, metaID)
	// sum(4,4,25)=33, capped at 25.
	if sevBefore != 25 {
		t.Fatalf("meta-risk severity before close: got %d, want 25", sevBefore)
	}

	// Close the highest-severity child.
	deleteRisk(t, admin, tenant, r3)

	evals, err = store.Evaluate(ctx, []string{"ownership"})
	if err != nil {
		t.Fatalf("Evaluate #2: %v", err)
	}
	// Now only 2 risks on 1 team — below threshold, so this cycle is
	// no_match (or near_miss), never fired.
	if len(evals) != 1 || evals[0].Outcome == "fired" {
		t.Fatalf("eval #2 after child close: want 1 non-fired, got %v", evalOutcomes(evals))
	}
	// The meta-risk row, however, MUST still exist (no auto-close) — the
	// engine never deletes or closes a parent.
	if n := metaRiskCount(t, admin, tenant); n != 1 {
		t.Fatalf("after closing a child: %d meta-risks, want 1 (parent must survive)", n)
	}

	// The parent's lifecycle is independent — it is still present and was
	// never mutated to a closed state (risks have no status column in v1;
	// "survives" == row still exists, which we just asserted). The stored
	// severity is historical (frozen at last fire); a live recompute would
	// show the drop. Re-fire by adding a third risk back on a 2nd team to
	// prove the severity recomputes LOWER than the original 25.
	team3 := seedOrgUnit(t, admin, tenant, "team-3")
	seedRisk(t, admin, tenant, "r4", 1, 2, []string{"ownership"}, &team3, now) // sev 2
	evals, err = store.Evaluate(ctx, []string{"ownership"})
	if err != nil {
		t.Fatalf("Evaluate #3: %v", err)
	}
	if len(evals) != 1 || evals[0].Outcome != "fired" {
		t.Fatalf("eval #3: want 1 fired, got %v", evalOutcomes(evals))
	}
	sevAfter := metaSeverity(t, admin, tenant, metaID)
	// sum(4,4,2)=10 — strictly lower than the original 25.
	if sevAfter >= sevBefore {
		t.Fatalf("severity did not recompute lower: before %d, after %d", sevBefore, sevAfter)
	}
	if sevAfter != 10 {
		t.Fatalf("recomputed severity: got %d, want 10 (sum of 4,4,2)", sevAfter)
	}
}

// ===== ISC-22: re-activation does not re-fire on pre-activation risks =====

func TestEngine_ReactivationNoHistoricalRefire_ISC22(t *testing.T) {
	app := openPool(t, appDSN(t))
	admin := openPool(t, adminDSN(t))
	tenant := freshTenant(t, admin)
	ctx := ctxFor(t, tenant)
	store := aggrule.NewStore(app)

	team1 := seedOrgUnit(t, admin, tenant, "team-1")
	team2 := seedOrgUnit(t, admin, tenant, "team-2")

	// Three "historical" risks created well in the past.
	old := time.Now().UTC().AddDate(0, 0, -10)
	seedRisk(t, admin, tenant, "old1", 3, 3, []string{"ownership"}, &team1, old)
	seedRisk(t, admin, tenant, "old2", 4, 3, []string{"ownership"}, &team1, old)
	seedRisk(t, admin, tenant, "old3", 3, 3, []string{"ownership"}, &team2, old)

	// Create + activate + immediately deactivate, then re-activate. The
	// re-activation's activated_at is "now", which is AFTER the historical
	// risks — so the engine's window cut-off excludes them.
	created, err := store.Create(ctx, makeRule("ownership-cross-team", "ownership", 3, 2, 90, "max"), "tester")
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if _, err := store.Activate(ctx, created.ID, "reviewer"); err != nil {
		t.Fatalf("Activate: %v", err)
	}
	if _, err := store.Deactivate(ctx, created.ID, "reviewer"); err != nil {
		t.Fatalf("Deactivate: %v", err)
	}
	// Small sleep so the re-activation timestamp is strictly after the
	// historical risks' created_at (which is days old anyway, but also
	// after any first-activation timestamp).
	time.Sleep(10 * time.Millisecond)
	if _, err := store.Activate(ctx, created.ID, "reviewer"); err != nil {
		t.Fatalf("Re-activate: %v", err)
	}

	// Evaluate. The historical risks are BEFORE activated_at, so the rule
	// must NOT fire on them.
	evals, err := store.Evaluate(ctx, []string{"ownership"})
	if err != nil {
		t.Fatalf("Evaluate after re-activation: %v", err)
	}
	if len(evals) != 1 {
		t.Fatalf("got %d evals, want 1", len(evals))
	}
	if evals[0].Outcome == "fired" {
		t.Fatalf("rule fired on historical (pre-activation) data — P0 anti-criterion violated")
	}
	if n := metaRiskCount(t, admin, tenant); n != 0 {
		t.Fatalf("re-activation created %d meta-risks from historical data, want 0", n)
	}

	// A risk written AFTER re-activation, together with the historical
	// ones, still must not bring the historical ones into scope — but on
	// its own it is below threshold. Add 3 fresh risks across 2 teams:
	// those DO fire.
	fresh := time.Now().UTC().Add(5 * time.Second)
	seedRisk(t, admin, tenant, "new1", 3, 3, []string{"ownership"}, &team1, fresh)
	seedRisk(t, admin, tenant, "new2", 4, 3, []string{"ownership"}, &team1, fresh)
	seedRisk(t, admin, tenant, "new3", 3, 3, []string{"ownership"}, &team2, fresh)
	evals, err = store.Evaluate(ctx, []string{"ownership"})
	if err != nil {
		t.Fatalf("Evaluate with fresh risks: %v", err)
	}
	if len(evals) != 1 || evals[0].Outcome != "fired" {
		t.Fatalf("fresh risks: want 1 fired, got %v", evalOutcomes(evals))
	}
	// Exactly 3 — only the post-activation risks. If the historical ones
	// had been counted it would be 6.
	if evals[0].RiskCount != 3 {
		t.Fatalf("RiskCount %d, want 3 (historical risks must stay excluded)", evals[0].RiskCount)
	}
}

// ===== ISC-23: engine writer is a narrow interface (behavioural) =====

// The compile-time guarantee lives in engine.go (`var _ metaRiskWriter =
// (*dbx.Queries)(nil)` plus the unexported metaRiskWriter interface — the
// Engine struct can only be constructed with that narrow interface, and the
// interface deliberately omits CreateRisk / DeleteRisk / any control or
// policy or evidence writer).
//
// This behavioural test confirms the bound holds in practice: a full
// evaluation cycle touches ONLY risks that are rule-generated meta-risks
// (plus the ledger) — it never inserts or deletes a non-meta risk, and never
// touches controls.
func TestEngine_NarrowWriter_ISC23(t *testing.T) {
	app := openPool(t, appDSN(t))
	admin := openPool(t, adminDSN(t))
	tenant := freshTenant(t, admin)
	ctx := ctxFor(t, tenant)
	store := aggrule.NewStore(app)

	team1 := seedOrgUnit(t, admin, tenant, "team-1")
	team2 := seedOrgUnit(t, admin, tenant, "team-2")
	now := time.Now().UTC().Add(5 * time.Second)
	child1 := seedRisk(t, admin, tenant, "r1", 3, 3, []string{"ownership"}, &team1, now)
	child2 := seedRisk(t, admin, tenant, "r2", 4, 3, []string{"ownership"}, &team1, now)
	child3 := seedRisk(t, admin, tenant, "r3", 3, 3, []string{"ownership"}, &team2, now)
	childIDs := map[uuid.UUID]bool{child1: true, child2: true, child3: true}

	controlCountBefore := tableCount(t, admin, tenant, "controls")

	createAndActivate(t, store, ctx, makeRule("ownership-cross-team", "ownership", 3, 2, 90, "max"))
	for i := 0; i < 3; i++ {
		if _, err := store.Evaluate(ctx, []string{"ownership"}); err != nil {
			t.Fatalf("Evaluate %d: %v", i, err)
		}
	}

	// The three original child risks are untouched — still present, still
	// non-rule-generated.
	for id := range childIDs {
		var ruleGen *bool
		if err := admin.QueryRow(context.Background(),
			`SELECT (inherent_score->>'rule_generated')::bool FROM risks WHERE tenant_id=$1 AND id=$2`,
			tenant, id).Scan(&ruleGen); err != nil {
			t.Fatalf("child %s missing after engine run — engine deleted a non-meta risk: %v", id, err)
		}
		if ruleGen != nil && *ruleGen {
			t.Fatalf("child %s was marked rule_generated — engine mutated a non-meta risk", id)
		}
	}

	// The ONLY risks the engine created carry rule_generated=true.
	nonMetaCreated := tableCountWhere(t, admin, tenant,
		`risks`, `(inherent_score->>'rule_generated') IS DISTINCT FROM 'true' AND id NOT IN ($2,$3,$4)`,
		child1, child2, child3)
	if nonMetaCreated != 0 {
		t.Fatalf("engine created %d non-meta-risk rows — narrow-writer bound violated", nonMetaCreated)
	}

	// controls table is completely untouched.
	if after := tableCount(t, admin, tenant, "controls"); after != controlCountBefore {
		t.Fatalf("controls row count changed (%d -> %d) — engine wrote outside its bound",
			controlCountBefore, after)
	}
}

// ===== ISC-30: AC-7 E2E flow =====

func TestEngine_E2E_OwnershipCrossTeam_ISC30(t *testing.T) {
	app := openPool(t, appDSN(t))
	admin := openPool(t, adminDSN(t))
	tenant := freshTenant(t, admin)
	ctx := ctxFor(t, tenant)
	store := aggrule.NewStore(app)

	// Rule: ownership-cross-team, threshold 3 risks / 2 teams / 90 days,
	// parent at org level.
	rule := createAndActivate(t, store, ctx,
		makeRule("ownership-cross-team", "ownership", 3, 2, 90, "max"))

	team1 := seedOrgUnit(t, admin, tenant, "team-1")
	team2 := seedOrgUnit(t, admin, tenant, "team-2")
	now := time.Now().UTC().Add(5 * time.Second)

	// Step 1: create 2 ownership risks across 1 team — NO meta-risk.
	seedRisk(t, admin, tenant, "ownership risk 1", 3, 3, []string{"ownership"}, &team1, now)
	seedRisk(t, admin, tenant, "ownership risk 2", 4, 3, []string{"ownership"}, &team1, now)
	evals, err := store.Evaluate(ctx, []string{"ownership"})
	if err != nil {
		t.Fatalf("Evaluate after 2 risks/1 team: %v", err)
	}
	if len(evals) != 1 || evals[0].Outcome == "fired" {
		t.Fatalf("2 risks/1 team: must NOT fire, got %v", evalOutcomes(evals))
	}
	if n := metaRiskCount(t, admin, tenant); n != 0 {
		t.Fatalf("2 risks/1 team: %d meta-risks, want 0", n)
	}

	// Step 2: create a 3rd ownership risk on a 2nd team — meta-risk
	// auto-appears at org level with all 3 as children.
	r3 := seedRisk(t, admin, tenant, "ownership risk 3", 5, 4, []string{"ownership"}, &team2, now)
	evals, err = store.Evaluate(ctx, []string{"ownership"})
	if err != nil {
		t.Fatalf("Evaluate after 3rd risk/2nd team: %v", err)
	}
	if len(evals) != 1 || evals[0].Outcome != "fired" {
		t.Fatalf("3 risks/2 teams: must fire, got %v", evalOutcomes(evals))
	}
	if n := metaRiskCount(t, admin, tenant); n != 1 {
		t.Fatalf("3 risks/2 teams: %d meta-risks, want 1", n)
	}
	metaID := *evals[0].MetaRiskID

	// The meta-risk is at org level with exactly 3 children.
	var level string
	if err := admin.QueryRow(context.Background(),
		`SELECT level FROM risks WHERE tenant_id=$1 AND id=$2`, tenant, metaID).Scan(&level); err != nil {
		t.Fatalf("read meta-risk level: %v", err)
	}
	if level != "org" {
		t.Fatalf("meta-risk level: got %q, want org", level)
	}
	if c := childLinkCount(t, admin, tenant, metaID); c != 3 {
		t.Fatalf("meta-risk has %d child links, want 3", c)
	}
	if evals[0].RiskCount != 3 || evals[0].TeamCount != 2 {
		t.Fatalf("fired eval counts: got (risks=%d,teams=%d), want (3,2)",
			evals[0].RiskCount, evals[0].TeamCount)
	}

	// Step 3: resolve the 3rd risk — parent severity drops, parent stays
	// open (the row survives; no auto-close).
	deleteRisk(t, admin, tenant, r3)
	if n := metaRiskCount(t, admin, tenant); n != 1 {
		t.Fatalf("after resolving 3rd risk: %d meta-risks, want 1 (parent must survive)", n)
	}
	// Child links cascade on child delete — the parent now has 2 children.
	if c := childLinkCount(t, admin, tenant, metaID); c != 2 {
		t.Fatalf("after resolving 3rd risk: %d child links, want 2", c)
	}

	// The auditor ledger reflects the full history for the rule.
	ledger, err := store.Evaluations(ctx, rule.ID)
	if err != nil {
		t.Fatalf("Evaluations: %v", err)
	}
	if len(ledger) < 2 {
		t.Fatalf("evaluation ledger has %d rows, want >= 2 (the no-fire + the fire)", len(ledger))
	}
	// The audit log records the create + activate.
	auditLog, err := store.AuditLog(ctx, rule.ID)
	if err != nil {
		t.Fatalf("AuditLog: %v", err)
	}
	if len(auditLog) != 2 {
		t.Fatalf("audit log has %d rows, want 2 (created + activated)", len(auditLog))
	}
}

// ----- small helpers -----

func contains(s []string, v string) bool {
	for _, x := range s {
		if x == v {
			return true
		}
	}
	return false
}

func evalOutcomes(evals []aggrule.Evaluation) []string {
	out := make([]string, len(evals))
	for i, e := range evals {
		out[i] = e.Outcome
	}
	return out
}

func evalCounts(evals []aggrule.Evaluation) []int {
	out := make([]int, len(evals))
	for i, e := range evals {
		out[i] = e.RiskCount
	}
	return out
}

func metaSeverity(t *testing.T, admin *pgxpool.Pool, tenant string, metaID uuid.UUID) int {
	t.Helper()
	var sev int
	if err := admin.QueryRow(context.Background(),
		`SELECT (inherent_score->>'severity')::int FROM risks WHERE tenant_id=$1 AND id=$2`,
		tenant, metaID).Scan(&sev); err != nil {
		t.Fatalf("read meta-risk severity: %v", err)
	}
	return sev
}

func childLinkCount(t *testing.T, admin *pgxpool.Pool, tenant string, parentID uuid.UUID) int {
	t.Helper()
	var n int
	if err := admin.QueryRow(context.Background(),
		`SELECT count(*) FROM risk_aggregations WHERE tenant_id=$1 AND parent_risk_id=$2`,
		tenant, parentID).Scan(&n); err != nil {
		t.Fatalf("count child links: %v", err)
	}
	return n
}

func tableCount(t *testing.T, admin *pgxpool.Pool, tenant, table string) int {
	t.Helper()
	var n int
	// table is a fixed literal from the test, not user input.
	if err := admin.QueryRow(context.Background(),
		`SELECT count(*) FROM `+table+` WHERE tenant_id=$1`, tenant).Scan(&n); err != nil {
		t.Fatalf("count %s: %v", table, err)
	}
	return n
}

func tableCountWhere(t *testing.T, admin *pgxpool.Pool, tenant, table, cond string, args ...interface{}) int {
	t.Helper()
	var n int
	params := append([]interface{}{tenant}, args...)
	if err := admin.QueryRow(context.Background(),
		`SELECT count(*) FROM `+table+` WHERE tenant_id=$1 AND `+cond, params...).Scan(&n); err != nil {
		t.Fatalf("count %s where %s: %v", table, cond, err)
	}
	return n
}
