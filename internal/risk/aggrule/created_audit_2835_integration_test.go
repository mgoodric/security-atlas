//go:build integration

// #2835 — a rule-generated meta-risk's creation is in its audit trail,
// naming the rule that made it. Updating the same meta-risk on a later
// evaluation writes no second created row.

package aggrule_test

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/mgoodric/security-atlas/internal/dbtest"
	"github.com/mgoodric/security-atlas/internal/risk/aggrule"
)

func TestEngine_MetaRiskCreationIsAudited_2835(t *testing.T) {
	app := dbtest.NewAppPool(t)
	admin := dbtest.NewMigratePool(t)
	tenant := freshTenant(t, admin)
	ctx := ctxFor(t, tenant)
	// Evaluate an hour on, so children made after activation are in the
	// rule's window.
	store := aggrule.NewStore(app).WithClock(func() time.Time { return time.Now().Add(time.Hour) })

	team1 := seedOrgUnit(t, admin, tenant, "team-1")
	team2 := seedOrgUnit(t, admin, tenant, "team-2")
	rule := createAndActivate(t, store, ctx, makeRule("ownership-2835", "ownership", 3, 2, 90, "max"))
	for _, team := range []uuid.UUID{team1, team1, team2} {
		seedRisk(t, admin, tenant, "child", 3, 3, []string{"ownership"}, &team, rule.ActivatedAt.Add(time.Minute))
	}

	evals, err := store.Evaluate(ctx, []string{"ownership"})
	if err != nil {
		t.Fatalf("Evaluate: %v", err)
	}
	if len(evals) != 1 || evals[0].Outcome != "fired" || evals[0].MetaRiskID == nil {
		t.Fatalf("want 1 fired evaluation with a meta-risk, got %v", evalOutcomes(evals))
	}
	meta := *evals[0].MetaRiskID
	// A second evaluation updates the meta-risk; it is not a creation.
	if _, err := store.Evaluate(ctx, []string{"ownership"}); err != nil {
		t.Fatalf("second Evaluate: %v", err)
	}

	rows, err := admin.Query(context.Background(), `
		SELECT tenant_id::text, actor, before_state, after_state
		FROM risk_audit_log WHERE risk_id = $1 AND action_type = 'created'`, meta)
	if err != nil {
		t.Fatalf("read created rows: %v", err)
	}
	defer rows.Close()
	n := 0
	for rows.Next() {
		n++
		var gotTenant, actor string
		var before, after []byte
		if err := rows.Scan(&gotTenant, &actor, &before, &after); err != nil {
			t.Fatalf("scan: %v", err)
		}
		if gotTenant != tenant {
			t.Errorf("tenant_id = %s, want %s", gotTenant, tenant)
		}
		if want := aggrule.RuleActor(rule.ID); actor != want {
			t.Errorf("actor = %q, want %q", actor, want)
		}
		var a map[string]any
		if err := json.Unmarshal(after, &a); err != nil {
			t.Fatalf("after_state: %v", err)
		}
		if a["title"] == "" || a["treatment"] != "untreated" || a["category_id"] == nil || a["owner_user_id"] != nil {
			t.Errorf("after_state = %s", after)
		}
		if string(before) != "{}" {
			t.Errorf("before_state = %s, want {}", before)
		}
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("rows: %v", err)
	}
	if n != 1 {
		t.Fatalf("created rows for the meta-risk = %d, want 1", n)
	}
}
