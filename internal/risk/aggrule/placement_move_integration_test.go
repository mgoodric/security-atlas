//go:build integration

// #1954 — moving a risk between org units (#1749) against the aggregation
// engine, which counts distinct org units toward a rule's min_teams:
//
//   - a move that crosses min_teams fires the rule, and moving back drops
//     it below threshold;
//   - a move that waits on a concurrent evaluation's rule lock finishes
//     without a deadlock and re-evaluates with the new placement;
//   - a meta-risk moved to another unit and level keeps that placement
//     when the rule re-evaluates and refreshes it.

package aggrule_test

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/mgoodric/security-atlas/internal/db/dbx"
	"github.com/mgoodric/security-atlas/internal/dbtest"
	"github.com/mgoodric/security-atlas/internal/risk"
	"github.com/mgoodric/security-atlas/internal/risk/aggrule"
)

// freshMoveTenant is freshTenant plus risk_audit_log, which every move
// writes. The log is append-only, so its cleanup DELETE is refused and
// logged.
func freshMoveTenant(t *testing.T, admin *pgxpool.Pool) string {
	t.Helper()
	return dbtest.SeedTenant(t, admin,
		"risk_audit_log",
		"aggregation_rule_evaluations",
		"aggregation_rule_audit_log",
		"aggregation_rules",
		"risk_aggregations",
		"risks",
		"org_units",
	)
}

// seedOrgUnitAt inserts an org unit at the given level and returns its id.
func seedOrgUnitAt(t *testing.T, admin *pgxpool.Pool, tenant, name, level string) uuid.UUID {
	t.Helper()
	id := uuid.New()
	if _, err := admin.Exec(context.Background(), `
		INSERT INTO org_units (id, tenant_id, name, parent_id, level, acceptance_authorities)
		VALUES ($1, $2, $3, NULL, $4, '[]'::jsonb)`, id, tenant, name, level); err != nil {
		t.Fatalf("seed org unit: %v", err)
	}
	return id
}

type lastEval struct {
	outcome   string
	teamCount int
	metaRisk  *uuid.UUID
}

// latestEvaluation reads the tenant's most recent evaluation row.
func latestEvaluation(t *testing.T, admin *pgxpool.Pool, tenant string) lastEval {
	t.Helper()
	var e lastEval
	if err := admin.QueryRow(context.Background(), `
		SELECT outcome, team_count, meta_risk_id FROM aggregation_rule_evaluations
		WHERE tenant_id = $1 ORDER BY evaluated_at DESC LIMIT 1`, tenant).
		Scan(&e.outcome, &e.teamCount, &e.metaRisk); err != nil {
		t.Fatalf("read latest evaluation: %v", err)
	}
	return e
}

// riskPlacement reads a risk's level and org unit.
func riskPlacement(t *testing.T, admin *pgxpool.Pool, tenant string, id uuid.UUID) (string, *uuid.UUID) {
	t.Helper()
	var level string
	var unit *uuid.UUID
	if err := admin.QueryRow(context.Background(),
		`SELECT level::text, org_unit_id FROM risks WHERE tenant_id = $1 AND id = $2`,
		tenant, id).Scan(&level, &unit); err != nil {
		t.Fatalf("read risk placement: %v", err)
	}
	return level, unit
}

func moveTo(unit uuid.UUID) risk.PlacementInput {
	return risk.PlacementInput{OrgUnitSet: true, OrgUnitID: &unit, Actor: "tester"}
}

func TestPlacementMove_CrossesMinTeams_1954(t *testing.T) {
	app := dbtest.NewAppPool(t)
	admin := dbtest.NewMigratePool(t)
	tenant := freshMoveTenant(t, admin)
	ctx := ctxFor(t, tenant)
	rules := aggrule.NewStore(app)
	risks := riskStoreAsWired(app)

	team1 := seedOrgUnit(t, admin, tenant, "team-1")
	team2 := seedOrgUnit(t, admin, tenant, "team-2")
	createAndActivate(t, rules, ctx, makeRule("ownership-two-teams", "ownership", 3, 2, 90, "max"))
	now := time.Now().UTC().Add(5 * time.Second)
	r1 := seedRisk(t, admin, tenant, "r1", 2, 2, []string{"ownership"}, &team1, now)
	r2 := seedRisk(t, admin, tenant, "r2", 2, 2, []string{"ownership"}, &team1, now)
	r3 := seedRisk(t, admin, tenant, "r3", 3, 3, []string{"ownership"}, &team1, now)

	evals, err := rules.Evaluate(ctx, []string{"ownership"})
	if err != nil || len(evals) != 1 || evals[0].Outcome == "fired" || evals[0].MetaRiskID != nil {
		t.Fatalf("one team: want an unfired evaluation and no meta-risk, got %v %v", evalOutcomes(evals), err)
	}

	// Moving r3 to a second team meets min_teams: the move's after-hook fires.
	if _, err := risks.UpdatePlacement(ctx, r3, moveTo(team2)); err != nil {
		t.Fatalf("move r3 to team-2: %v", err)
	}
	fired := latestEvaluation(t, admin, tenant)
	if fired.outcome != "fired" || fired.teamCount != 2 || fired.metaRisk == nil {
		t.Fatalf("after the move: %+v, want fired with 2 teams and a meta-risk", fired)
	}
	meta := *fired.metaRisk
	children := metaChildIDs(t, admin, tenant, meta)
	if len(children) != 3 || !children[r1] || !children[r2] || !children[r3] {
		t.Fatalf("children %v, want r1..r3", children)
	}

	// Moving it back drops below min_teams; the meta-risk is refreshed, not
	// replaced.
	if _, err := risks.UpdatePlacement(ctx, r3, moveTo(team1)); err != nil {
		t.Fatalf("move r3 back to team-1: %v", err)
	}
	below := latestEvaluation(t, admin, tenant)
	if below.outcome == "fired" || below.teamCount != 1 {
		t.Fatalf("after moving back: %+v, want unfired with 1 team", below)
	}
	if below.metaRisk == nil || *below.metaRisk != meta {
		t.Fatalf("below-threshold refresh names %v, want %s", below.metaRisk, meta)
	}

	var audits int
	if err := admin.QueryRow(context.Background(),
		`SELECT count(*) FROM risk_audit_log WHERE tenant_id = $1 AND risk_id = $2 AND action_type = 'placement_changed'`,
		tenant, r3).Scan(&audits); err != nil {
		t.Fatalf("count audit rows: %v", err)
	}
	if audits != 2 {
		t.Fatalf("placement audit rows for r3 = %d, want 2", audits)
	}
}

// Transaction A holds the rule's lock and the move is observed waiting on
// it before A evaluates and commits. The move takes the rule locks before
// the risk row (#1867), so it waits rather than deadlocking, and its own
// evaluation then sees the new placement.
func TestPlacementMove_ConcurrentWithEvaluation_1954(t *testing.T) {
	app := dbtest.NewAppPool(t)
	admin := dbtest.NewMigratePool(t)
	tenant := freshMoveTenant(t, admin)
	ctx := ctxFor(t, tenant)
	tenantID := uuid.MustParse(tenant)
	rules := aggrule.NewStore(app)
	risks := riskStoreAsWired(app)

	team1 := seedOrgUnit(t, admin, tenant, "team-1")
	team2 := seedOrgUnit(t, admin, tenant, "team-2")
	createAndActivate(t, rules, ctx, makeRule("ownership-move-race", "ownership", 3, 2, 90, "max"))
	now := time.Now().UTC().Add(5 * time.Second)
	seedRisk(t, admin, tenant, "r1", 2, 2, []string{"ownership"}, &team1, now)
	seedRisk(t, admin, tenant, "r2", 2, 2, []string{"ownership"}, &team1, now)
	r3 := seedRisk(t, admin, tenant, "r3", 3, 3, []string{"ownership"}, &team1, now)

	txA, pidA := openTenantTx(t, ctx, app)
	defer func() { _ = txA.Rollback(ctx) }()
	engA := aggrule.NewEngine(dbx.New(txA))
	if err := engA.LockRules(ctx, tenantID, []string{"ownership"}); err != nil {
		t.Fatalf("A: lock rule: %v", err)
	}

	moved := make(chan error, 1)
	go func() {
		_, err := risks.UpdatePlacement(ctx, r3, moveTo(team2))
		moved <- err
	}()
	waitBlockedBy(t, admin, pidA)

	// A evaluates while the move waits: it still sees one team.
	evalsA, err := engA.Evaluate(ctx, tenantID, []string{"ownership"})
	if err != nil || len(evalsA) != 1 || evalsA[0].Outcome == "fired" {
		t.Fatalf("A: want one unfired evaluation, got %v %v", evalOutcomes(evalsA), err)
	}
	if err := txA.Commit(ctx); err != nil {
		t.Fatalf("A: commit: %v", err)
	}
	if err := <-moved; err != nil {
		t.Fatalf("move racing an evaluation: %v", err)
	}

	if _, unit := riskPlacement(t, admin, tenant, r3); unit == nil || *unit != team2 {
		t.Fatalf("r3 org unit = %v, want team-2", unit)
	}
	last := latestEvaluation(t, admin, tenant)
	if last.outcome != "fired" || last.teamCount != 2 || last.metaRisk == nil {
		t.Fatalf("after the move: %+v, want fired with 2 teams", last)
	}
	if got := len(metaChildIDs(t, admin, tenant, *last.metaRisk)); got != 3 {
		t.Fatalf("meta-risk children = %d, want 3", got)
	}
}

// The engine creates a meta-risk at the rule's parent level with no org
// unit. Once someone moves it, a later evaluation refreshes its score and
// links but leaves the placement alone.
func TestPlacementMove_MetaRiskKeepsPlacement_1954(t *testing.T) {
	app := dbtest.NewAppPool(t)
	admin := dbtest.NewMigratePool(t)
	tenant := freshMoveTenant(t, admin)
	ctx := ctxFor(t, tenant)
	rules := aggrule.NewStore(app)
	risks := riskStoreAsWired(app)

	team1 := seedOrgUnit(t, admin, tenant, "team-1")
	team2 := seedOrgUnit(t, admin, tenant, "team-2")
	company := seedOrgUnitAt(t, admin, tenant, "Acme", "company")
	createAndActivate(t, rules, ctx, makeRule("ownership-meta-move", "ownership", 3, 2, 90, "max"))
	now := time.Now().UTC().Add(5 * time.Second)
	seedRisk(t, admin, tenant, "r1", 2, 2, []string{"ownership"}, &team1, now)
	seedRisk(t, admin, tenant, "r2", 2, 2, []string{"ownership"}, &team1, now)
	seedRisk(t, admin, tenant, "r3", 3, 3, []string{"ownership"}, &team2, now)

	evals, err := rules.Evaluate(ctx, []string{"ownership"})
	if err != nil || len(evals) != 1 || evals[0].Outcome != "fired" || evals[0].MetaRiskID == nil {
		t.Fatalf("initial Evaluate: %v %v", evalOutcomes(evals), err)
	}
	meta := *evals[0].MetaRiskID
	if level, unit := riskPlacement(t, admin, tenant, meta); level != "org" || unit != nil {
		t.Fatalf("new meta-risk placement = %s / %v, want org / none", level, unit)
	}

	if _, err := risks.UpdatePlacement(ctx, meta, moveTo(company)); err != nil {
		t.Fatalf("move meta-risk: %v", err)
	}

	// A new child re-evaluates the rule, which refreshes the meta-risk.
	r4 := seedRisk(t, admin, tenant, "r4", 5, 5, []string{"ownership"}, &team1, now)
	evals, err = rules.Evaluate(ctx, []string{"ownership"})
	if err != nil || len(evals) != 1 || evals[0].MetaRiskID == nil || *evals[0].MetaRiskID != meta || evals[0].RiskCount != 4 {
		t.Fatalf("re-Evaluate: want 4 children fired into %s, got %+v %v", meta, evals, err)
	}
	if !metaChildIDs(t, admin, tenant, meta)[r4] {
		t.Fatal("re-evaluation did not link r4")
	}
	if level, unit := riskPlacement(t, admin, tenant, meta); level != "company" || unit == nil || *unit != company {
		t.Fatalf("meta-risk placement after re-evaluation = %s / %v, want company / %s", level, unit, company)
	}
}
