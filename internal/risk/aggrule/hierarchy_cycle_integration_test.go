//go:build integration

// #1748: links made by hand can place a rule's meta-risk under one of the
// rule's candidates. Re-evaluating the rule must not then link that
// candidate back under the meta-risk, which would close a cycle.

package aggrule_test

import (
	"context"
	"testing"
	"time"

	"github.com/mgoodric/security-atlas/internal/dbtest"
	"github.com/mgoodric/security-atlas/internal/risk/aggrule"
)

func TestEngine_DoesNotLinkAnAncestorOfItsMetaRisk(t *testing.T) {
	app := dbtest.NewAppPool(t)
	admin := dbtest.NewMigratePool(t)
	tenant := freshTenant(t, admin)
	ctx := ctxFor(t, tenant)
	store := aggrule.NewStore(app)

	team1 := seedOrgUnit(t, admin, tenant, "team-1")
	team2 := seedOrgUnit(t, admin, tenant, "team-2")
	now := time.Now().UTC().Add(5 * time.Second)
	ancestor := seedRisk(t, admin, tenant, "r1", 3, 3, []string{"ownership"}, &team1, now)
	_ = seedRisk(t, admin, tenant, "r2", 4, 3, []string{"ownership"}, &team1, now)
	_ = seedRisk(t, admin, tenant, "r3", 3, 3, []string{"ownership"}, &team2, now)

	createAndActivate(t, store, ctx, makeRule("ownership-cycle", "ownership", 2, 1, 90, "max"))
	evals, err := store.Evaluate(ctx, []string{"ownership"})
	if err != nil {
		t.Fatalf("first Evaluate: %v", err)
	}
	if len(evals) != 1 || evals[0].Outcome != "fired" || evals[0].MetaRiskID == nil {
		t.Fatalf("first evaluation = %+v, want one fired with a meta-risk", evals)
	}
	meta := *evals[0].MetaRiskID

	// By hand, roll the meta-risk up under r1, one of its own children.
	if _, err := admin.Exec(context.Background(), `
		INSERT INTO risk_aggregations (parent_risk_id, child_risk_id, tenant_id)
		VALUES ($1, $2, $3)`, ancestor, meta, tenant); err != nil {
		t.Fatalf("link meta-risk under r1: %v", err)
	}

	evals, err = store.Evaluate(ctx, []string{"ownership"})
	if err != nil {
		t.Fatalf("second Evaluate: %v", err)
	}
	if len(evals) != 1 || evals[0].RiskCount != 2 {
		t.Fatalf("second evaluation = %+v, want RiskCount 2 (r1 dropped as an ancestor)", evals)
	}

	var cyclic bool
	if err := admin.QueryRow(context.Background(), `
		SELECT EXISTS (
			SELECT 1 FROM risk_aggregations
			WHERE tenant_id = $1 AND parent_risk_id = $2 AND child_risk_id = $3
		)`, tenant, meta, ancestor).Scan(&cyclic); err != nil {
		t.Fatalf("read link: %v", err)
	}
	if cyclic {
		t.Fatalf("meta-risk %s still has its ancestor %s as a child: cycle", meta, ancestor)
	}
}
