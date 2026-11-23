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

	"github.com/mgoodric/security-atlas/internal/db/dbx"
	"github.com/mgoodric/security-atlas/internal/dbtest"
	"github.com/mgoodric/security-atlas/internal/risk"
	"github.com/mgoodric/security-atlas/internal/risk/aggrule"
	"github.com/mgoodric/security-atlas/internal/tenancy"
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

// A person unlinks a rule-made link while an evaluation is about to drop
// the same link as stale. The link is removed once and audited once, by
// the person: UnlinkChildTx holds the hierarchy lock the evaluation waits
// on, so the evaluation reads the links after the unlink commits.
func TestEngine_ConcurrentUnlinkIsAuditedOnce_2961(t *testing.T) {
	app := dbtest.NewAppPool(t)
	admin := dbtest.NewMigratePool(t)
	tenant := freshTenant(t, admin)
	ctx := ctxFor(t, tenant)
	store := aggrule.NewStore(app).WithClock(func() time.Time { return time.Now().Add(time.Hour) })

	team := seedOrgUnit(t, admin, tenant, "team-1")
	rule := createAndActivate(t, store, ctx, makeRule("ownership-2961-race", "ownership", 2, 1, 90, "max"))
	var children []uuid.UUID
	for range 3 {
		children = append(children,
			seedRisk(t, admin, tenant, "child", 3, 3, []string{"ownership"}, &team, rule.ActivatedAt.Add(time.Minute)))
	}
	evals, err := store.Evaluate(ctx, []string{"ownership"})
	if err != nil || len(evals) != 1 || evals[0].MetaRiskID == nil {
		t.Fatalf("first Evaluate: %v %v", evalOutcomes(evals), err)
	}
	meta := *evals[0].MetaRiskID
	stale := children[2]
	if _, err := admin.Exec(context.Background(),
		`UPDATE risks SET themes = '{}' WHERE tenant_id = $1 AND id = $2`, tenant, stale); err != nil {
		t.Fatalf("drop theme: %v", err)
	}

	// The person's unlink, held open.
	tx, err := app.Begin(ctx)
	if err != nil {
		t.Fatalf("begin: %v", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if err := tenancy.ApplyTenant(ctx, tx); err != nil {
		t.Fatalf("apply tenant: %v", err)
	}
	const person = "user:race-2961"
	if err := risk.UnlinkChildTx(ctx, dbx.New(tx), uuid.MustParse(tenant), meta, stale,
		risk.HierarchyAudit{Actor: person}); err != nil {
		t.Fatalf("UnlinkChildTx: %v", err)
	}

	// The evaluation starts while the unlink is open, and waits on it.
	done := make(chan error, 1)
	go func() {
		_, err := store.Evaluate(ctx, []string{"ownership"})
		done <- err
	}()
	waitForLockWaiter(t, admin)
	if err := tx.Commit(ctx); err != nil {
		t.Fatalf("commit unlink: %v", err)
	}
	if err := <-done; err != nil {
		t.Fatalf("concurrent Evaluate: %v", err)
	}

	var unlinks []ruleLinkRow
	for _, r := range ruleLinkRows(t, admin, tenant, meta) {
		if r.Action == "child_unlinked" {
			unlinks = append(unlinks, r)
		}
	}
	if len(unlinks) != 1 || unlinks[0].Actor != person || unlinks[0].Child != stale.String() {
		t.Fatalf("child_unlinked rows = %+v, want one of %s by %s", unlinks, stale, person)
	}
}

// waitForLockWaiter waits until some session is blocked on a lock, so the
// evaluation is known to be queued behind the open unlink. It gives up
// after 10s and lets the test go on, where a missing wait shows as a
// wrong row count rather than a hang.
func waitForLockWaiter(t *testing.T, admin *pgxpool.Pool) {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		var n int
		if err := admin.QueryRow(context.Background(), `
			SELECT count(*) FROM pg_stat_activity
			WHERE datname = current_database() AND wait_event_type = 'Lock'`).Scan(&n); err != nil {
			t.Fatalf("pg_stat_activity: %v", err)
		}
		if n > 0 {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Log("no lock waiter seen within 10s")
}
