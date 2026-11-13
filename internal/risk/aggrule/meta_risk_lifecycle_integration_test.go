//go:build integration

// #1861 — a rule's meta-risk across time, concurrency and child removal.
//
//   - the rolling window moving past window_days keeps updating ONE
//     meta-risk instead of spawning one per day;
//   - concurrent evaluations of one rule create one meta-risk, not several;
//   - removing a theme from a child, or deleting a child, re-evaluates the
//     rule, so the meta-risk drops the child's link and score.
//
// Real Postgres with RLS, like the rest of this package's integration tier.

package aggrule_test

import (
	"context"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/mgoodric/security-atlas/internal/db/dbx"
	"github.com/mgoodric/security-atlas/internal/dbtest"
	"github.com/mgoodric/security-atlas/internal/risk/aggrule"
	"github.com/mgoodric/security-atlas/internal/tenancy"
)

// metaChildIDs returns the child risk ids linked under a meta-risk.
func metaChildIDs(t *testing.T, admin *pgxpool.Pool, tenant string, parentID uuid.UUID) map[uuid.UUID]bool {
	t.Helper()
	rows, err := admin.Query(context.Background(),
		`SELECT child_risk_id FROM risk_aggregations WHERE tenant_id=$1 AND parent_risk_id=$2`,
		tenant, parentID)
	if err != nil {
		t.Fatalf("list child links: %v", err)
	}
	defer rows.Close()
	out := map[uuid.UUID]bool{}
	for rows.Next() {
		var id uuid.UUID
		if err := rows.Scan(&id); err != nil {
			t.Fatalf("scan child link: %v", err)
		}
		out[id] = true
	}
	return out
}

func metaChildCount(t *testing.T, admin *pgxpool.Pool, tenant string, metaID uuid.UUID) int {
	t.Helper()
	var n int
	if err := admin.QueryRow(context.Background(),
		`SELECT (inherent_score->>'child_count')::int FROM risks WHERE tenant_id=$1 AND id=$2`,
		tenant, metaID).Scan(&n); err != nil {
		t.Fatalf("read meta-risk child_count: %v", err)
	}
	return n
}

// The rolling window moves forward one UTC day at a time once window_days
// have passed since activation. Before #1861 the meta-risk key included the
// window start, so from day window_days+1 every day with a matching write
// created a new meta-risk.
func TestEngine_RollingWindowKeepsOneMetaRisk_1861(t *testing.T) {
	app := dbtest.NewAppPool(t)
	admin := dbtest.NewMigratePool(t)
	tenant := freshTenant(t, admin)
	ctx := ctxFor(t, tenant)

	var clockMu sync.Mutex
	var clock time.Time
	setClock := func(t time.Time) { clockMu.Lock(); clock = t; clockMu.Unlock() }
	store := aggrule.NewStore(app).WithClock(func() time.Time {
		clockMu.Lock()
		defer clockMu.Unlock()
		return clock
	})

	team1 := seedOrgUnit(t, admin, tenant, "team-1")
	team2 := seedOrgUnit(t, admin, tenant, "team-2")

	const windowDays = 90
	rule := createAndActivate(t, store, ctx, makeRule("ownership-rolling", "ownership", 3, 2, windowDays, "max"))
	if rule.ActivatedAt == nil {
		t.Fatal("active rule has no activated_at")
	}
	act := rule.ActivatedAt.UTC()
	day := func(n int) time.Time { return act.AddDate(0, 0, n) }

	// Day 1: three risks inside the first window fire the rule.
	early := map[uuid.UUID]bool{}
	for i, team := range []uuid.UUID{team1, team1, team2} {
		early[seedRisk(t, admin, tenant, "early", 3, 3+i%2, []string{"ownership"}, &team, act.Add(time.Hour))] = true
	}
	setClock(day(1))
	evals, err := store.Evaluate(ctx, []string{"ownership"})
	if err != nil {
		t.Fatalf("Evaluate day 1: %v", err)
	}
	if len(evals) != 1 || evals[0].Outcome != "fired" || evals[0].MetaRiskID == nil {
		t.Fatalf("day 1: want 1 fired with a meta-risk, got %v", evalOutcomes(evals))
	}
	meta := *evals[0].MetaRiskID

	// Day 94: three more risks. From day 95 the window start is past
	// activation and past the early risks.
	late := map[uuid.UUID]bool{}
	for _, team := range []uuid.UUID{team1, team2, team2} {
		late[seedRisk(t, admin, tenant, "late", 4, 4, []string{"ownership"}, &team, day(94))] = true
	}

	// Days 95..98 (> window_days since activation): each day brings a new
	// matching risk and an evaluation. Every one updates the same meta-risk.
	for d := windowDays + 5; d <= windowDays+8; d++ {
		late[seedRisk(t, admin, tenant, "daily", 2, 2, []string{"ownership"}, &team1, day(d).Add(-time.Hour))] = true
		setClock(day(d))
		evals, err := store.Evaluate(ctx, []string{"ownership"})
		if err != nil {
			t.Fatalf("Evaluate day %d: %v", d, err)
		}
		if len(evals) != 1 || evals[0].Outcome != "fired" {
			t.Fatalf("day %d: want 1 fired, got %v", d, evalOutcomes(evals))
		}
		if got := *evals[0].MetaRiskID; got != meta {
			t.Fatalf("day %d: fired into meta-risk %s, want the original %s", d, got, meta)
		}
		if n := metaRiskCount(t, admin, tenant); n != 1 {
			t.Fatalf("day %d: %d meta-risks, want 1", d, n)
		}
	}

	// The meta-risk tracks the current window: the early risks aged out and
	// were unlinked; every in-window risk is linked.
	children := metaChildIDs(t, admin, tenant, meta)
	for id := range early {
		if children[id] {
			t.Errorf("early risk %s still linked after leaving the window", id)
		}
	}
	for id := range late {
		if !children[id] {
			t.Errorf("in-window risk %s not linked", id)
		}
	}
	if got := metaChildCount(t, admin, tenant, meta); got != len(late) {
		t.Errorf("meta-risk child_count = %d, want %d", got, len(late))
	}
}

// Meta-risk de-dup is a read then an insert. A second evaluation of the same
// rule, started while the first has created the meta-risk but not yet
// committed, must wait and then update that meta-risk, not create its own.
// Holding transaction A open makes the overlap deterministic; with free-running
// goroutines the transactions are too short to overlap reliably.
func TestEngine_ConcurrentEvaluationsCreateOneMetaRisk_1861(t *testing.T) {
	app := dbtest.NewAppPool(t)
	admin := dbtest.NewMigratePool(t)
	tenant := freshTenant(t, admin)
	ctx := ctxFor(t, tenant)
	store := aggrule.NewStore(app)

	team1 := seedOrgUnit(t, admin, tenant, "team-1")
	team2 := seedOrgUnit(t, admin, tenant, "team-2")
	now := time.Now().UTC().Add(5 * time.Second)
	createAndActivate(t, store, ctx, makeRule("ownership-concurrent", "ownership", 3, 2, 90, "max"))
	seedRisk(t, admin, tenant, "r1", 3, 3, []string{"ownership"}, &team1, now)
	seedRisk(t, admin, tenant, "r2", 4, 3, []string{"ownership"}, &team1, now)
	seedRisk(t, admin, tenant, "r3", 3, 3, []string{"ownership"}, &team2, now)
	tenantID := uuid.MustParse(tenant)

	// evaluateInTx runs the engine in a fresh tenant transaction and leaves
	// it open for the caller to commit.
	evaluateInTx := func() (pgx.Tx, *uuid.UUID, error) {
		tx, err := app.Begin(ctx)
		if err != nil {
			return nil, nil, err
		}
		if err := tenancy.ApplyTenant(ctx, tx); err != nil {
			_ = tx.Rollback(ctx)
			return nil, nil, err
		}
		evals, err := aggrule.NewEngine(dbx.New(tx)).Evaluate(ctx, tenantID, []string{"ownership"})
		if err != nil {
			_ = tx.Rollback(ctx)
			return nil, nil, err
		}
		if len(evals) != 1 || evals[0].MetaRiskID == nil {
			_ = tx.Rollback(ctx)
			return nil, nil, fmt.Errorf("want 1 fired evaluation, got %v", evalOutcomes(evals))
		}
		return tx, evals[0].MetaRiskID, nil
	}

	txA, metaA, err := evaluateInTx()
	if err != nil {
		t.Fatalf("evaluation A: %v", err)
	}

	type result struct {
		tx   pgx.Tx
		meta *uuid.UUID
		err  error
	}
	done := make(chan result, 1)
	go func() {
		tx, meta, err := evaluateInTx()
		done <- result{tx, meta, err}
	}()

	// Give B time to reach the meta-risk lookup (or, unserialized, to create
	// its own) while A still holds its uncommitted meta-risk.
	select {
	case r := <-done:
		if r.tx != nil {
			_ = r.tx.Rollback(ctx)
		}
		_ = txA.Rollback(ctx)
		t.Fatalf("evaluation B finished while A was uncommitted (meta %v, err %v): not serialized on the rule's key", r.meta, r.err)
	case <-time.After(300 * time.Millisecond):
	}
	if err := txA.Commit(ctx); err != nil {
		t.Fatalf("commit A: %v", err)
	}

	r := <-done
	if r.err != nil {
		t.Fatalf("evaluation B: %v", r.err)
	}
	if err := r.tx.Commit(ctx); err != nil {
		t.Fatalf("commit B: %v", err)
	}
	if *r.meta != *metaA {
		t.Fatalf("evaluation B fired into meta-risk %s, want A's %s", *r.meta, *metaA)
	}
	if n := metaRiskCount(t, admin, tenant); n != 1 {
		t.Fatalf("%d meta-risks after two overlapping evaluations, want 1", n)
	}
}

// Removing a theme from a child, or deleting a child, runs the hook for the
// rule that targeted it, through risk.Store as the server wires it.
func TestEngine_ThemeRemovalAndDeleteRefreshMetaRisk_1861(t *testing.T) {
	app := dbtest.NewAppPool(t)
	admin := dbtest.NewMigratePool(t)
	tenant := freshTenant(t, admin)
	ctx := ctxFor(t, tenant)
	rules := aggrule.NewStore(app)
	risks := riskStoreAsWired(app)

	team1 := seedOrgUnit(t, admin, tenant, "team-1")
	createAndActivate(t, rules, ctx, makeRule("ownership-stale", "ownership", 3, 1, 90, "max"))

	now := time.Now().UTC().Add(5 * time.Second)
	r1 := seedRisk(t, admin, tenant, "r1", 2, 2, nil, &team1, now) // sev 4
	r2 := seedRisk(t, admin, tenant, "r2", 2, 2, nil, &team1, now) // sev 4
	r3 := seedRisk(t, admin, tenant, "r3", 3, 3, nil, &team1, now) // sev 9
	r4 := seedRisk(t, admin, tenant, "r4", 5, 5, nil, &team1, now) // sev 25
	for _, id := range []uuid.UUID{r1, r2, r3, r4} {
		if _, err := risks.AssignThemes(ctx, id, []string{"ownership"}); err != nil {
			t.Fatalf("AssignThemes %s: %v", id, err)
		}
	}
	var meta uuid.UUID
	if err := admin.QueryRow(context.Background(),
		`SELECT id FROM risks WHERE tenant_id=$1 AND (inherent_score->>'rule_generated')::bool`,
		tenant).Scan(&meta); err != nil {
		t.Fatalf("find meta-risk: %v", err)
	}
	if got := metaSeverity(t, admin, tenant, meta); got != 25 {
		t.Fatalf("meta-risk severity with 4 children = %d, want 25", got)
	}

	// Remove the theme from the highest-severity child.
	if _, err := risks.AssignThemes(ctx, r4, nil); err != nil {
		t.Fatalf("remove theme from r4: %v", err)
	}
	if children := metaChildIDs(t, admin, tenant, meta); children[r4] || len(children) != 3 {
		t.Fatalf("after theme removal: children %v, want r1..r3 only", children)
	}
	if got := metaSeverity(t, admin, tenant, meta); got != 9 {
		t.Fatalf("after theme removal: severity %d, want 9 (max of 4,4,9)", got)
	}
	if got := metaChildCount(t, admin, tenant, meta); got != 3 {
		t.Fatalf("after theme removal: child_count %d, want 3", got)
	}

	// Delete a child. The meta-risk is now below threshold; it stays (no
	// auto-close) but no longer reports the deleted child's score.
	if err := risks.Delete(ctx, r3); err != nil {
		t.Fatalf("Delete r3: %v", err)
	}
	if got := metaSeverity(t, admin, tenant, meta); got != 4 {
		t.Fatalf("after delete: severity %d, want 4 (max of 4,4)", got)
	}
	if got := metaChildCount(t, admin, tenant, meta); got != 2 {
		t.Fatalf("after delete: child_count %d, want 2", got)
	}
	if n := metaRiskCount(t, admin, tenant); n != 1 {
		t.Fatalf("after delete: %d meta-risks, want 1 (parent survives)", n)
	}

	// Deleting the meta-risk itself does not re-create it in the same write.
	if err := risks.Delete(ctx, meta); err != nil {
		t.Fatalf("Delete meta-risk: %v", err)
	}
	if n := metaRiskCount(t, admin, tenant); n != 0 {
		t.Fatalf("deleting the meta-risk re-created it: %d meta-risks", n)
	}
}
