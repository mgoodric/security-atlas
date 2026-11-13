//go:build integration

// #1867 — lock order between risk writes and rule evaluation, the theme
// read a theme write re-evaluates, and the ledger pointer of a
// below-threshold refresh.
//
// The concurrency tests hold one transaction open and wait, through
// pg_blocking_pids, until the other is observed waiting on it, so the
// interleaving is fixed rather than left to timing.

package aggrule_test

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/mgoodric/security-atlas/internal/db/dbx"
	"github.com/mgoodric/security-atlas/internal/dbtest"
	"github.com/mgoodric/security-atlas/internal/risk"
	"github.com/mgoodric/security-atlas/internal/risk/aggrule"
	"github.com/mgoodric/security-atlas/internal/tenancy"
)

// waitBlockedBy waits until some backend is waiting on a lock held by
// holderPID.
func waitBlockedBy(t *testing.T, admin *pgxpool.Pool, holderPID int32) {
	t.Helper()
	deadline := time.Now().Add(15 * time.Second)
	for time.Now().Before(deadline) {
		var n int
		if err := admin.QueryRow(context.Background(),
			`SELECT count(*) FROM pg_stat_activity WHERE $1::int = ANY(pg_blocking_pids(pid))`,
			holderPID).Scan(&n); err != nil {
			t.Fatalf("poll pg_blocking_pids: %v", err)
		}
		if n > 0 {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("no backend blocked on pid %d within 15s", holderPID)
}

// openTenantTx begins a tenant transaction on app and returns it with its
// backend pid.
func openTenantTx(t *testing.T, ctx context.Context, app *pgxpool.Pool) (pgx.Tx, int32) {
	t.Helper()
	tx, err := app.Begin(ctx)
	if err != nil {
		t.Fatalf("begin: %v", err)
	}
	if err := tenancy.ApplyTenant(ctx, tx); err != nil {
		_ = tx.Rollback(ctx)
		t.Fatalf("apply tenant: %v", err)
	}
	var pid int32
	if err := tx.QueryRow(ctx, `SELECT pg_backend_pid()`).Scan(&pid); err != nil {
		_ = tx.Rollback(ctx)
		t.Fatalf("backend pid: %v", err)
	}
	return tx, pid
}

// riskStoreAsWired builds risk.Store with both hooks, as httpserver.go does.
func riskStoreAsWired(app *pgxpool.Pool) *risk.Store {
	return risk.NewStore(app).
		WithBeforeRiskWrite(aggrule.LockOnRiskWrite).
		WithAfterRiskWrite(aggrule.EvaluateOnRiskWrite)
}

// A risk delete racing an evaluation that is about to link the risk. Before
// #1867 the delete took the row lock and then the rule's advisory lock (in
// the hook), while the evaluation held the advisory lock and then needed the
// row (FOR KEY SHARE, to link the child): a 40P01 deadlock, and a 500 for
// whichever side Postgres picked. The delete now takes the rule locks first.
//
// Transaction A holds the rule's lock, the delete is started and observed
// waiting on A, and only then does A evaluate and link the risk. Unfixed,
// that link deadlocks; fixed, the delete waits for A and then unlinks.
func TestRiskDelete_TakesRuleLocksBeforeRow_1867(t *testing.T) {
	app := dbtest.NewAppPool(t)
	admin := dbtest.NewMigratePool(t)
	tenant := freshTenant(t, admin)
	ctx := ctxFor(t, tenant)
	tenantID := uuid.MustParse(tenant)
	rules := aggrule.NewStore(app)
	risks := riskStoreAsWired(app)

	team1 := seedOrgUnit(t, admin, tenant, "team-1")
	createAndActivate(t, rules, ctx, makeRule("ownership-delete-race", "ownership", 3, 1, 90, "max"))
	now := time.Now().UTC().Add(5 * time.Second)
	r1 := seedRisk(t, admin, tenant, "r1", 2, 2, []string{"ownership"}, &team1, now)
	r2 := seedRisk(t, admin, tenant, "r2", 2, 2, []string{"ownership"}, &team1, now)
	r3 := seedRisk(t, admin, tenant, "r3", 3, 3, []string{"ownership"}, &team1, now)
	evals, err := rules.Evaluate(ctx, []string{"ownership"})
	if err != nil || len(evals) != 1 || evals[0].MetaRiskID == nil {
		t.Fatalf("initial Evaluate: %v %v", evalOutcomes(evals), err)
	}
	meta := *evals[0].MetaRiskID
	// r4 is written without the hook, so no evaluation has linked it yet.
	r4 := seedRisk(t, admin, tenant, "r4", 5, 5, []string{"ownership"}, &team1, now)

	txA, pidA := openTenantTx(t, ctx, app)
	defer func() { _ = txA.Rollback(ctx) }()
	engA := aggrule.NewEngine(dbx.New(txA))
	if err := engA.LockRules(ctx, tenantID, []string{"ownership"}); err != nil {
		t.Fatalf("A: lock rule: %v", err)
	}

	deleted := make(chan error, 1)
	go func() { deleted <- risks.Delete(ctx, r4) }()
	waitBlockedBy(t, admin, pidA)

	// A evaluates while the delete waits: it links r4 as a child.
	evalsA, err := engA.Evaluate(ctx, tenantID, []string{"ownership"})
	if err != nil {
		t.Fatalf("A: Evaluate while the delete waits: %v", err)
	}
	if len(evalsA) != 1 || evalsA[0].MetaRiskID == nil || *evalsA[0].MetaRiskID != meta || evalsA[0].RiskCount != 4 {
		t.Fatalf("A: want 4 children fired into %s, got %+v", meta, evalsA)
	}
	if err := txA.Commit(ctx); err != nil {
		t.Fatalf("A: commit: %v", err)
	}
	if err := <-deleted; err != nil {
		t.Fatalf("Delete r4 racing an evaluation: %v", err)
	}

	children := metaChildIDs(t, admin, tenant, meta)
	if len(children) != 3 || !children[r1] || !children[r2] || !children[r3] {
		t.Fatalf("after delete: children %v, want r1..r3", children)
	}
	if got := metaChildCount(t, admin, tenant, meta); got != 3 {
		t.Fatalf("after delete: child_count %d, want 3", got)
	}
	if got := metaSeverity(t, admin, tenant, meta); got != 9 {
		t.Fatalf("after delete: severity %d, want 9", got)
	}
}

// AssignThemes must re-evaluate the themes the risk carried when its own
// write landed, not the ones an unlocked read saw earlier. Transaction A
// adds `ownership` to r and links it, and stays open; a concurrent
// AssignThemes then replaces r's themes with another. Before #1867 its
// before-themes came from a plain read that could not see A's uncommitted
// `ownership`, so the ownership rule was never re-evaluated and r stayed
// linked under the meta-risk after losing the theme.
func TestAssignThemes_ReadsBeforeThemesUnderRowLock_1867(t *testing.T) {
	app := dbtest.NewAppPool(t)
	admin := dbtest.NewMigratePool(t)
	tenant := freshTenant(t, admin)
	ctx := ctxFor(t, tenant)
	tenantID := uuid.MustParse(tenant)
	rules := aggrule.NewStore(app)
	risks := riskStoreAsWired(app)

	team1 := seedOrgUnit(t, admin, tenant, "team-1")
	createAndActivate(t, rules, ctx, makeRule("ownership-theme-race", "ownership", 3, 1, 90, "max"))
	now := time.Now().UTC().Add(5 * time.Second)
	for _, title := range []string{"r1", "r2", "r3"} {
		seedRisk(t, admin, tenant, title, 2, 2, []string{"ownership"}, &team1, now)
	}
	r := seedRisk(t, admin, tenant, "r", 4, 4, nil, &team1, now)
	evals, err := rules.Evaluate(ctx, []string{"ownership"})
	if err != nil || len(evals) != 1 || evals[0].MetaRiskID == nil {
		t.Fatalf("initial Evaluate: %v %v", evalOutcomes(evals), err)
	}
	meta := *evals[0].MetaRiskID

	txA, pidA := openTenantTx(t, ctx, app)
	defer func() { _ = txA.Rollback(ctx) }()
	if _, err := txA.Exec(ctx,
		`UPDATE risks SET themes = ARRAY['ownership'] WHERE tenant_id = $1 AND id = $2`,
		tenant, r); err != nil {
		t.Fatalf("A: add theme: %v", err)
	}
	if _, err := aggrule.NewEngine(dbx.New(txA)).Evaluate(ctx, tenantID, []string{"ownership"}); err != nil {
		t.Fatalf("A: Evaluate: %v", err)
	}
	if !metaChildIDsTx(t, ctx, txA, meta)[r] {
		t.Fatal("A: r not linked after adding the theme")
	}

	assigned := make(chan error, 1)
	go func() {
		_, err := risks.AssignThemes(ctx, r, []string{"monitoring"})
		assigned <- err
	}()
	waitBlockedBy(t, admin, pidA)
	if err := txA.Commit(ctx); err != nil {
		t.Fatalf("A: commit: %v", err)
	}
	if err := <-assigned; err != nil {
		t.Fatalf("AssignThemes racing A: %v", err)
	}

	if children := metaChildIDs(t, admin, tenant, meta); children[r] || len(children) != 3 {
		t.Fatalf("r lost the theme but children are %v, want r1..r3 only", children)
	}
	if got := metaChildCount(t, admin, tenant, meta); got != 3 {
		t.Fatalf("child_count %d, want 3", got)
	}
}

// metaChildIDsTx lists a meta-risk's children as transaction tx sees them.
func metaChildIDsTx(t *testing.T, ctx context.Context, tx pgx.Tx, parentID uuid.UUID) map[uuid.UUID]bool {
	t.Helper()
	rows, err := tx.Query(ctx, `SELECT child_risk_id FROM risk_aggregations WHERE parent_risk_id = $1`, parentID)
	if err != nil {
		t.Fatalf("list child links in tx: %v", err)
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

// A below-threshold evaluation that refreshes the activation's meta-risk
// names it on its ledger row, so the change to the meta-risk's score and
// links is traceable to an evaluation.
func TestEngine_BelowThresholdRefreshNamesMetaRisk_1867(t *testing.T) {
	app := dbtest.NewAppPool(t)
	admin := dbtest.NewMigratePool(t)
	tenant := freshTenant(t, admin)
	ctx := ctxFor(t, tenant)
	rules := aggrule.NewStore(app)

	team1 := seedOrgUnit(t, admin, tenant, "team-1")
	createAndActivate(t, rules, ctx, makeRule("ownership-ledger", "ownership", 3, 1, 90, "max"))
	now := time.Now().UTC().Add(5 * time.Second)
	var ids []uuid.UUID
	for _, title := range []string{"r1", "r2", "r3"} {
		ids = append(ids, seedRisk(t, admin, tenant, title, 2, 2, []string{"ownership"}, &team1, now))
	}
	evals, err := rules.Evaluate(ctx, []string{"ownership"})
	if err != nil || len(evals) != 1 || evals[0].MetaRiskID == nil {
		t.Fatalf("initial Evaluate: %v %v", evalOutcomes(evals), err)
	}
	meta := *evals[0].MetaRiskID

	deleteRisk(t, admin, tenant, ids[2])
	evals, err = rules.Evaluate(ctx, []string{"ownership"})
	if err != nil {
		t.Fatalf("Evaluate below threshold: %v", err)
	}
	if len(evals) != 1 || evals[0].Outcome == "fired" {
		t.Fatalf("want 1 below-threshold evaluation, got %v", evalOutcomes(evals))
	}
	if evals[0].MetaRiskID == nil || *evals[0].MetaRiskID != meta || evals[0].WindowStart == nil {
		t.Fatalf("below-threshold refresh: meta_risk_id %v window %v, want %s and a window",
			evals[0].MetaRiskID, evals[0].WindowStart, meta)
	}
	var stored uuid.UUID
	if err := admin.QueryRow(context.Background(),
		`SELECT meta_risk_id FROM aggregation_rule_evaluations WHERE tenant_id = $1 AND id = $2`,
		tenant, evals[0].ID).Scan(&stored); err != nil {
		t.Fatalf("read ledger row: %v", err)
	}
	if stored != meta {
		t.Fatalf("ledger meta_risk_id %s, want %s", stored, meta)
	}

	// With no meta-risk to refresh, a below-threshold row names none.
	deleteRisk(t, admin, tenant, meta)
	evals, err = rules.Evaluate(ctx, []string{"ownership"})
	if err != nil || len(evals) != 1 {
		t.Fatalf("Evaluate with no meta-risk: %v %v", evalOutcomes(evals), err)
	}
	if evals[0].MetaRiskID != nil || evals[0].WindowStart != nil {
		t.Fatalf("no meta-risk to refresh, but the row names %v / %v", evals[0].MetaRiskID, evals[0].WindowStart)
	}
}

// themeRacingHook wraps LockOnRiskWrite and records the themes of each call.
// On the first call it commits race, a theme write on another connection,
// between the store's unlocked read and its row lock: the window #1880
// closes.
type themeRacingHook struct {
	race  func()
	calls [][]string
}

func (h *themeRacingHook) before(ctx context.Context, q *dbx.Queries, tenantID uuid.UUID, themes []string) error {
	h.calls = append(h.calls, append([]string(nil), themes...))
	if len(h.calls) == 1 {
		h.race()
	}
	return aggrule.LockOnRiskWrite(ctx, q, tenantID, themes)
}

// A theme write that commits between a risk write's unlocked read and its
// row lock adds a theme whose rules were not locked first. The after-hook
// would take that rule's lock while holding the row lock, the order #1867
// removed. The store notices the locked themes are not covered, rolls back,
// and retries once, so the second attempt pre-locks the added theme.
func TestRiskWrite_RetriesWhenThemesChangeBeforeRowLock_1880(t *testing.T) {
	app := dbtest.NewAppPool(t)
	admin := dbtest.NewMigratePool(t)
	tenant := freshTenant(t, admin)
	ctx := ctxFor(t, tenant)
	team1 := seedOrgUnit(t, admin, tenant, "team-1")
	now := time.Now().UTC().Add(5 * time.Second)

	addMonitoring := func(id uuid.UUID) func() {
		return func() {
			if _, err := admin.Exec(context.Background(),
				`UPDATE risks SET themes = ARRAY['monitoring'] WHERE tenant_id = $1 AND id = $2`,
				tenant, id); err != nil {
				t.Errorf("racing theme write: %v", err)
			}
		}
	}
	wired := func(h *themeRacingHook) *risk.Store {
		return risk.NewStore(app).
			WithBeforeRiskWrite(h.before).
			WithAfterRiskWrite(aggrule.EvaluateOnRiskWrite)
	}

	t.Run("AssignThemes", func(t *testing.T) {
		r := seedRisk(t, admin, tenant, "assign", 2, 2, nil, &team1, now)
		h := &themeRacingHook{race: addMonitoring(r)}
		if _, err := wired(h).AssignThemes(ctx, r, []string{"ownership"}); err != nil {
			t.Fatalf("AssignThemes: %v", err)
		}
		if len(h.calls) != 2 || !contains(h.calls[1], "monitoring") {
			t.Fatalf("want a retry that pre-locks monitoring, got calls %v", h.calls)
		}
	})

	t.Run("Delete", func(t *testing.T) {
		r := seedRisk(t, admin, tenant, "delete", 2, 2, []string{"ownership"}, &team1, now)
		h := &themeRacingHook{race: addMonitoring(r)}
		if err := wired(h).Delete(ctx, r); err != nil {
			t.Fatalf("Delete: %v", err)
		}
		if len(h.calls) != 2 || !contains(h.calls[1], "monitoring") {
			t.Fatalf("want a retry that pre-locks monitoring, got calls %v", h.calls)
		}
	})

	t.Run("NoRaceNoRetry", func(t *testing.T) {
		r := seedRisk(t, admin, tenant, "steady", 2, 2, []string{"ownership"}, &team1, now)
		h := &themeRacingHook{race: func() {}}
		if _, err := wired(h).AssignThemes(ctx, r, []string{"monitoring"}); err != nil {
			t.Fatalf("AssignThemes: %v", err)
		}
		if len(h.calls) != 1 {
			t.Fatalf("no concurrent write, want one attempt, got calls %v", h.calls)
		}
	})
}
