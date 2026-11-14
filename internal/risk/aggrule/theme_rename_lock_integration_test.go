//go:build integration

// #1811 — a theme rename or delete contended with a theme assignment and
// with an evaluation of a rule targeting the theme.
//
// As in lock_order_integration_test.go, one side is held at a fixed point
// and the other is observed waiting on it before the first is released, so
// the interleaving is fixed rather than left to timing.

package aggrule_test

import (
	"context"
	"errors"
	"slices"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/mgoodric/security-atlas/internal/db/dbx"
	"github.com/mgoodric/security-atlas/internal/dbtest"
	"github.com/mgoodric/security-atlas/internal/risk"
	"github.com/mgoodric/security-atlas/internal/risk/aggrule"
)

const contendedTheme = "cloud-drift"

// seedTenantTheme adds an organisation theme to tenant.
func seedTenantTheme(t *testing.T, admin *pgxpool.Pool, tenant, name string) {
	t.Helper()
	if _, err := admin.Exec(context.Background(),
		`INSERT INTO org_themes (id, tenant_id, theme_name) VALUES (gen_random_uuid(), $1, $2)`,
		tenant, name); err != nil {
		t.Fatalf("seed org theme: %v", err)
	}
	t.Cleanup(func() {
		_, _ = admin.Exec(context.Background(), `DELETE FROM org_themes WHERE tenant_id = $1`, tenant)
	})
}

// waitAdvisoryWaiterOr waits until some backend is waiting for an advisory
// lock (the theme-vocabulary lock, in these tests), or until done fires,
// meaning the side that should be waiting finished without waiting. It
// reports whether it saw the waiter; the caller then asserts on the
// outcome, so a missing lock fails the assertions rather than a timeout.
func waitAdvisoryWaiterOr(t *testing.T, admin *pgxpool.Pool, done <-chan error) bool {
	t.Helper()
	deadline := time.Now().Add(15 * time.Second)
	for time.Now().Before(deadline) {
		if len(done) > 0 {
			return false
		}
		var n int
		if err := admin.QueryRow(context.Background(),
			`SELECT count(*) FROM pg_locks WHERE locktype = 'advisory' AND NOT granted`).Scan(&n); err != nil {
			t.Fatalf("poll pg_locks: %v", err)
		}
		if n > 0 {
			return true
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("no backend waiting on an advisory lock within 15s")
	return false
}

// pausedAssigner is a risk store wired as httpserver.go wires it, except
// that its BeforeRiskWrite stops once -- after AssignThemes has validated
// the themes (holding the shared vocabulary lock) and before it writes --
// until release is called. release is idempotent and also runs at test
// cleanup, so a failed test never leaves the assignment holding a
// connection.
func pausedAssigner(t *testing.T, app *pgxpool.Pool) (store *risk.Store, paused <-chan struct{}, release func()) {
	p := make(chan struct{})
	r := make(chan struct{})
	var pauseOnce, releaseOnce sync.Once
	release = func() { releaseOnce.Do(func() { close(r) }) }
	t.Cleanup(release)
	store = risk.NewStore(app).
		WithBeforeRiskWrite(func(ctx context.Context, q *dbx.Queries, tenantID uuid.UUID, themes []string) error {
			pauseOnce.Do(func() {
				close(p)
				<-r
			})
			return aggrule.LockOnRiskWrite(ctx, q, tenantID, themes)
		}).
		WithAfterRiskWrite(aggrule.EvaluateOnRiskWrite)
	return store, p, release
}

func isDeadlock(err error) bool {
	var pgErr *pgconn.PgError
	return errors.As(err, &pgErr) && pgErr.Code == "40P01"
}

func riskThemesOf(t *testing.T, admin *pgxpool.Pool, id uuid.UUID) []string {
	t.Helper()
	var themes []string
	if err := admin.QueryRow(context.Background(), `SELECT themes FROM risks WHERE id = $1`, id).Scan(&themes); err != nil {
		t.Fatalf("read risk themes: %v", err)
	}
	return themes
}

// An assignment of X that has validated X but not yet written it, racing a
// rename of X. Without the vocabulary lock the rename commits in between,
// its UPDATE misses the risk, and the assignment then stores the old name.
// With it the rename waits for the assignment and rewrites what it wrote.
func TestThemeRename_WaitsForInFlightAssignment_1811(t *testing.T) {
	app := dbtest.NewAppPool(t)
	admin := dbtest.NewMigratePool(t)
	tenant := freshTenant(t, admin)
	ctx := ctxFor(t, tenant)
	seedTenantTheme(t, admin, tenant, contendedTheme)
	team := seedOrgUnit(t, admin, tenant, "team-1")
	createAndActivate(t, aggrule.NewStore(app), ctx, makeRule("drift-rename-assign", contendedTheme, 3, 1, 90, "max"))
	r := seedRisk(t, admin, tenant, "r", 2, 2, nil, &team, time.Now().UTC().Add(5*time.Second))

	assigner, paused, release := pausedAssigner(t, app)
	assigned := make(chan error, 1)
	go func() {
		_, err := assigner.AssignThemes(ctx, r, []string{contendedTheme})
		assigned <- err
	}()
	<-paused

	renamed := make(chan error, 1)
	go func() {
		_, err := riskStoreAsWired(app).RenameTheme(ctx, contendedTheme,
			risk.ThemeText{Name: ptr("cloud-config")}, "tester")
		renamed <- err
	}()
	if !waitAdvisoryWaiterOr(t, admin, renamed) {
		t.Error("the rename did not wait for the in-flight assignment")
	}
	release()

	if err := <-assigned; err != nil {
		t.Fatalf("AssignThemes: %v (deadlock: %v)", err, isDeadlock(err))
	}
	if err := <-renamed; err != nil {
		t.Fatalf("RenameTheme: %v (deadlock: %v)", err, isDeadlock(err))
	}
	if got := riskThemesOf(t, admin, r); !slices.Equal(got, []string{"cloud-config"}) {
		t.Fatalf("risk themes = %v, want [cloud-config]", got)
	}
	var stale int
	if err := admin.QueryRow(context.Background(),
		`SELECT count(*) FROM risks WHERE tenant_id = $1 AND $2 = ANY(themes)`, tenant, contendedTheme).Scan(&stale); err != nil || stale != 0 {
		t.Fatalf("risks keeping %s = %d (%v), want 0", contendedTheme, stale, err)
	}
}

// The same race against a delete: the delete waits for the assignment and
// then sees the theme in use, rather than deleting it from under the risk.
func TestThemeDelete_WaitsForInFlightAssignment_1811(t *testing.T) {
	app := dbtest.NewAppPool(t)
	admin := dbtest.NewMigratePool(t)
	tenant := freshTenant(t, admin)
	ctx := ctxFor(t, tenant)
	seedTenantTheme(t, admin, tenant, contendedTheme)
	team := seedOrgUnit(t, admin, tenant, "team-1")
	r := seedRisk(t, admin, tenant, "r", 2, 2, nil, &team, time.Now().UTC().Add(5*time.Second))

	assigner, paused, release := pausedAssigner(t, app)
	assigned := make(chan error, 1)
	go func() {
		_, err := assigner.AssignThemes(ctx, r, []string{contendedTheme})
		assigned <- err
	}()
	<-paused

	deleted := make(chan error, 1)
	go func() { deleted <- riskStoreAsWired(app).DeleteTheme(ctx, contendedTheme, "tester") }()
	if !waitAdvisoryWaiterOr(t, admin, deleted) {
		t.Error("the delete did not wait for the in-flight assignment")
	}
	release()

	if err := <-assigned; err != nil {
		t.Fatalf("AssignThemes: %v (deadlock: %v)", err, isDeadlock(err))
	}
	var inUse *risk.ThemeInUseError
	if err := <-deleted; !errors.As(err, &inUse) || inUse.Risks != 1 {
		t.Fatalf("DeleteTheme = %v, want in use by 1 risk", err)
	}
	if got := riskThemesOf(t, admin, r); !slices.Equal(got, []string{contendedTheme}) {
		t.Fatalf("risk themes = %v", got)
	}
	var n int
	if err := admin.QueryRow(context.Background(),
		`SELECT count(*) FROM org_themes WHERE tenant_id = $1 AND theme_name = $2`, tenant, contendedTheme).Scan(&n); err != nil || n != 1 {
		t.Fatalf("theme rows = %d (%v), want 1", n, err)
	}
}

// A rename during an evaluation of a rule targeting the theme. Transaction
// A holds the rule's lock and evaluates (linking the children, which takes
// FOR KEY SHARE on them); the rename is observed waiting on A, then A
// commits. The rename must neither deadlock with A nor leave the rule or
// the risks on the old name, and the rule must evaluate the same after.
func TestThemeRename_DuringEvaluation_1811(t *testing.T) {
	app := dbtest.NewAppPool(t)
	admin := dbtest.NewMigratePool(t)
	tenant := freshTenant(t, admin)
	ctx := ctxFor(t, tenant)
	tenantID := uuid.MustParse(tenant)
	rules := aggrule.NewStore(app)
	seedTenantTheme(t, admin, tenant, contendedTheme)
	team := seedOrgUnit(t, admin, tenant, "team-1")
	rule := createAndActivate(t, rules, ctx, makeRule("drift-rename-eval", contendedTheme, 3, 1, 90, "max"))
	now := time.Now().UTC().Add(5 * time.Second)
	for _, title := range []string{"r1", "r2", "r3"} {
		seedRisk(t, admin, tenant, title, 2, 2, []string{contendedTheme}, &team, now)
	}

	txA, pidA := openTenantTx(t, ctx, app)
	defer func() { _ = txA.Rollback(ctx) }()
	engA := aggrule.NewEngine(dbx.New(txA))
	if err := engA.LockRules(ctx, tenantID, []string{contendedTheme}); err != nil {
		t.Fatalf("A: lock rule: %v", err)
	}
	evalsA, err := engA.Evaluate(ctx, tenantID, []string{contendedTheme})
	if err != nil || len(evalsA) != 1 || evalsA[0].MetaRiskID == nil || evalsA[0].RiskCount != 3 {
		t.Fatalf("A: Evaluate = %+v, %v; want fired with 3", evalsA, err)
	}
	meta := *evalsA[0].MetaRiskID

	renamed := make(chan error, 1)
	go func() {
		_, err := riskStoreAsWired(app).RenameTheme(ctx, contendedTheme,
			risk.ThemeText{Name: ptr("cloud-config")}, "tester")
		renamed <- err
	}()
	waitBlockedBy(t, admin, pidA)
	if err := txA.Commit(ctx); err != nil {
		t.Fatalf("A: commit: %v", err)
	}
	if err := <-renamed; err != nil {
		t.Fatalf("RenameTheme during evaluation: %v (deadlock: %v)", err, isDeadlock(err))
	}

	var column, body string
	if err := admin.QueryRow(context.Background(),
		`SELECT target_theme, rule_body->>'target_theme' FROM aggregation_rules WHERE id = $1`, rule.ID).Scan(&column, &body); err != nil {
		t.Fatalf("read rule: %v", err)
	}
	if column != "cloud-config" || body != "cloud-config" {
		t.Fatalf("rule target = %q / %q, want cloud-config", column, body)
	}
	var stale int
	if err := admin.QueryRow(context.Background(),
		`SELECT count(*) FROM risks WHERE tenant_id = $1 AND $2 = ANY(themes)`, tenant, contendedTheme).Scan(&stale); err != nil || stale != 0 {
		t.Fatalf("risks keeping %s = %d (%v), want 0 (the meta-risk included)", contendedTheme, stale, err)
	}
	evals, err := rules.Evaluate(ctx, nil)
	if err != nil || len(evals) != 1 || evals[0].MetaRiskID == nil || *evals[0].MetaRiskID != meta || evals[0].RiskCount != 3 {
		t.Fatalf("Evaluate after rename = %+v, %v; want the same meta-risk with 3", evals, err)
	}
}

func ptr(s string) *string { return &s }
