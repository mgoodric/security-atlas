//go:build integration

// #2961 — the links an aggregation rule makes and removes are audited on
// the meta-risk's trail, by the rule: one child_linked row per new link,
// none for a re-link on a later evaluation, and one child_unlinked row
// for a link the rule drops when its child leaves the window.

package aggrule_test

import (
	"context"
	"encoding/json"
	"slices"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/mgoodric/security-atlas/internal/dbtest"
	"github.com/mgoodric/security-atlas/internal/risk/aggrule"
)

type ruleLinkRow struct {
	Action string
	Actor  string
	Child  string
}

// ruleLinkRows reads the hierarchy rows on parent's trail, oldest first.
func ruleLinkRows(t *testing.T, admin *pgxpool.Pool, tenant string, parent uuid.UUID) []ruleLinkRow {
	t.Helper()
	rows, err := admin.Query(context.Background(), `
		SELECT action_type, actor, before_state, after_state
		FROM risk_audit_log
		WHERE tenant_id = $1 AND risk_id = $2
		  AND action_type IN ('child_linked', 'child_unlinked')
		ORDER BY created_at, id`, tenant, parent)
	if err != nil {
		t.Fatalf("read hierarchy rows: %v", err)
	}
	defer rows.Close()
	var out []ruleLinkRow
	for rows.Next() {
		var (
			r             ruleLinkRow
			before, after []byte
		)
		if err := rows.Scan(&r.Action, &r.Actor, &before, &after); err != nil {
			t.Fatalf("scan: %v", err)
		}
		state := after
		if r.Action == "child_unlinked" {
			state = before
		}
		var s map[string]any
		if err := json.Unmarshal(state, &s); err != nil {
			t.Fatalf("state: %v", err)
		}
		r.Child, _ = s["child_risk_id"].(string)
		out = append(out, r)
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("rows: %v", err)
	}
	return out
}

func TestEngine_RuleLinksAreAudited_2961(t *testing.T) {
	app := dbtest.NewAppPool(t)
	admin := dbtest.NewMigratePool(t)
	tenant := freshTenant(t, admin)
	ctx := ctxFor(t, tenant)
	store := aggrule.NewStore(app).WithClock(func() time.Time { return time.Now().Add(time.Hour) })

	team1 := seedOrgUnit(t, admin, tenant, "team-1")
	team2 := seedOrgUnit(t, admin, tenant, "team-2")
	rule := createAndActivate(t, store, ctx, makeRule("ownership-2961", "ownership", 2, 1, 90, "max"))
	var children []uuid.UUID
	for _, team := range []uuid.UUID{team1, team1, team2} {
		children = append(children,
			seedRisk(t, admin, tenant, "child", 3, 3, []string{"ownership"}, &team, rule.ActivatedAt.Add(time.Minute)))
	}

	evals, err := store.Evaluate(ctx, []string{"ownership"})
	if err != nil {
		t.Fatalf("Evaluate: %v", err)
	}
	if len(evals) != 1 || evals[0].Outcome != "fired" || evals[0].MetaRiskID == nil {
		t.Fatalf("want 1 fired evaluation with a meta-risk, got %v", evalOutcomes(evals))
	}
	meta := *evals[0].MetaRiskID
	actor := aggrule.RuleActor(rule.ID)

	rows := ruleLinkRows(t, admin, tenant, meta)
	if len(rows) != 3 {
		t.Fatalf("rows after first evaluation = %v, want 3 child_linked", rows)
	}
	var linked []string
	for _, r := range rows {
		if r.Action != "child_linked" || r.Actor != actor {
			t.Errorf("row = %+v, want child_linked by %s", r, actor)
		}
		linked = append(linked, r.Child)
	}
	want := []string{children[0].String(), children[1].String(), children[2].String()}
	slices.Sort(linked)
	slices.Sort(want)
	if !slices.Equal(linked, want) {
		t.Errorf("linked = %v, want %v", linked, want)
	}

	// A second evaluation re-links the same children: no new rows.
	if _, err := store.Evaluate(ctx, []string{"ownership"}); err != nil {
		t.Fatalf("second Evaluate: %v", err)
	}
	if n := len(ruleLinkRows(t, admin, tenant, meta)); n != 3 {
		t.Fatalf("rows after re-evaluation = %d, want 3", n)
	}

	// A child that loses the theme leaves the window: the rule drops its
	// link and writes one child_unlinked row.
	if _, err := admin.Exec(context.Background(),
		`UPDATE risks SET themes = '{}' WHERE tenant_id = $1 AND id = $2`, tenant, children[2]); err != nil {
		t.Fatalf("drop theme: %v", err)
	}
	if _, err := store.Evaluate(ctx, []string{"ownership"}); err != nil {
		t.Fatalf("third Evaluate: %v", err)
	}
	rows = ruleLinkRows(t, admin, tenant, meta)
	if len(rows) != 4 {
		t.Fatalf("rows after the child left = %v, want 4", rows)
	}
	if got := rows[3]; got.Action != "child_unlinked" || got.Actor != actor || got.Child != children[2].String() {
		t.Errorf("last row = %+v, want child_unlinked of %s by %s", got, children[2], actor)
	}
	if n := childLinkCount(t, admin, tenant, meta); n != 2 {
		t.Errorf("links after the child left = %d, want 2", n)
	}
}
