//go:build integration

// #2233 — risk writes hold the target category FOR SHARE, so an archive
// racing a create or a move gives either the old state or a refusal, never
// an active assignment to an archived category. Also the PATCH and name
// cases #2225 and #2258 left untested, and the down migration's refusal.

package risks_test

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/mgoodric/security-atlas/internal/dbtest"
	"github.com/mgoodric/security-atlas/internal/tenancy"
)

const riskAuditCategoryChangedDown = "../../../migrations/sql/20261030000000_risk_audit_category_changed.down.sql"

// holdArchive opens an app-role transaction that archives slug the way
// ArchiveCategory does (row lock, then the update) and leaves it
// uncommitted. It returns the transaction and its backend pid; the caller
// commits it.
func holdArchive(t *testing.T, app *pgxpool.Pool, tenant, slug string) (pgx.Tx, int32) {
	t.Helper()
	ctx, err := tenancy.WithTenant(context.Background(), tenant)
	if err != nil {
		t.Fatalf("tenant context: %v", err)
	}
	tx, err := app.Begin(ctx)
	if err != nil {
		t.Fatalf("begin archive: %v", err)
	}
	t.Cleanup(func() { _ = tx.Rollback(context.Background()) })
	if err := tenancy.ApplyTenant(ctx, tx); err != nil {
		t.Fatalf("apply tenant: %v", err)
	}
	var pid int32
	if err := tx.QueryRow(ctx, `SELECT pg_backend_pid()`).Scan(&pid); err != nil {
		t.Fatalf("backend pid: %v", err)
	}
	if _, err := tx.Exec(ctx, `
		SELECT 1 FROM org_risk_categories WHERE tenant_id = $1 AND slug = $2 FOR UPDATE`,
		tenant, slug); err != nil {
		t.Fatalf("lock %s: %v", slug, err)
	}
	tag, err := tx.Exec(ctx, `
		UPDATE org_risk_categories SET archived = true WHERE tenant_id = $1 AND slug = $2`,
		tenant, slug)
	if err != nil || tag.RowsAffected() != 1 {
		t.Fatalf("archive %s: %v (%d rows)", slug, err, tag.RowsAffected())
	}
	return tx, pid
}

// waitBlockedBy returns once some backend is waiting on a lock held by
// holder: the request under test has reached the category and is queued
// behind the archive. It polls a condition, not a clock.
func waitBlockedBy(t *testing.T, admin *pgxpool.Pool, holder int32) {
	t.Helper()
	deadline := time.Now().Add(30 * time.Second)
	for {
		var n int
		if err := admin.QueryRow(context.Background(), `
			SELECT count(*) FROM pg_locks
			WHERE NOT granted AND $1::int = ANY(pg_blocking_pids(pid))`, holder).Scan(&n); err != nil {
			t.Fatalf("read pg_locks: %v", err)
		}
		if n > 0 {
			return
		}
		if time.Now().After(deadline) {
			t.Fatal("no request blocked on the archive's category lock")
		}
		time.Sleep(10 * time.Millisecond)
	}
}

type lockCallResult struct {
	status int
	body   map[string]any
	err    error
}

// lockCall is taxCall for a request made off the test goroutine: it reports
// a transport error in the result instead of through t.
func lockCall(env taxEnv, method, path string, payload any) lockCallResult {
	var buf bytes.Buffer
	if payload != nil {
		_ = json.NewEncoder(&buf).Encode(payload)
	}
	req, err := http.NewRequest(method, env.url+path, &buf)
	if err != nil {
		return lockCallResult{err: err}
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+env.admin)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return lockCallResult{err: err}
	}
	defer func() { _ = resp.Body.Close() }()
	var body map[string]any
	_ = json.NewDecoder(resp.Body).Decode(&body)
	return lockCallResult{status: resp.StatusCode, body: body}
}

// raceArchive holds an archive of slug open, starts call, waits until call
// is blocked behind the archive, commits the archive and returns call's
// result.
func raceArchive(t *testing.T, admin, app *pgxpool.Pool, tenant, slug string, call func() lockCallResult) lockCallResult {
	t.Helper()
	tx, pid := holdArchive(t, app, tenant, slug)
	done := make(chan lockCallResult, 1)
	go func() { done <- call() }()
	waitBlockedBy(t, admin, pid)
	if err := tx.Commit(context.Background()); err != nil {
		t.Fatalf("commit archive: %v", err)
	}
	select {
	case r := <-done:
		if r.err != nil {
			t.Fatalf("request: %v", r.err)
		}
		return r
	case <-time.After(30 * time.Second):
		t.Fatal("request did not return after the archive committed")
	}
	return lockCallResult{}
}

// countRisksOn counts the tenant's risks on the category slug.
func countRisksOn(t *testing.T, admin *pgxpool.Pool, tenant, slug string) int {
	t.Helper()
	var n int
	if err := admin.QueryRow(context.Background(), `
		SELECT count(*) FROM risks r JOIN org_risk_categories c ON c.id = r.category_id
		WHERE r.tenant_id = $1 AND c.slug = $2`, tenant, slug).Scan(&n); err != nil {
		t.Fatalf("count risks on %s: %v", slug, err)
	}
	return n
}

// An archive committing while a create waits on its category: the create
// sees the archived row and is refused, and no risk lands on it. The
// taxonomy fixture has no `financial` risk.
func TestCategoryLock_CreateRacingArchiveIsRefused(t *testing.T) {
	admin := dbtest.NewMigratePool(t)
	app := dbtest.NewAppPool(t)
	env := taxServer(t, app, taxFixture(t, admin))

	r := raceArchive(t, admin, app, env.tenant, "financial", func() lockCallResult {
		return lockCall(env, http.MethodPost, "/v1/risks", map[string]any{
			"title": "racing create", "category": "financial",
			"inherent_score": map[string]int{"likelihood": 2, "impact": 2},
		})
	})
	if r.status != http.StatusBadRequest {
		t.Fatalf("create racing archive: status %d (%v), want 400", r.status, r.body)
	}
	if msg, _ := r.body["error"].(string); !strings.Contains(msg, "archived") {
		t.Fatalf("create racing archive: error %q does not name the archive", msg)
	}
	if n := countRisksOn(t, admin, env.tenant, "financial"); n != 0 {
		t.Fatalf("%d risk(s) assigned to the archived category, want 0", n)
	}
}

// The same race for a PATCH category move: the risk keeps its category.
func TestCategoryLock_MoveRacingArchiveIsRefused(t *testing.T) {
	admin := dbtest.NewMigratePool(t)
	app := dbtest.NewAppPool(t)
	env := taxServer(t, app, taxFixture(t, admin))
	riskID := riskIDsByCategory(t, admin, env.tenant, "regulatory")[0]

	r := raceArchive(t, admin, app, env.tenant, "financial", func() lockCallResult {
		return lockCall(env, http.MethodPatch, "/v1/risks/"+riskID,
			map[string]any{"category": "financial"})
	})
	if r.status != http.StatusBadRequest {
		t.Fatalf("move racing archive: status %d (%v), want 400", r.status, r.body)
	}
	if _, slug, _, _ := storedCategory(t, admin, riskID); slug != "regulatory" {
		t.Fatalf("after refused move: category %s, want regulatory", slug)
	}
	if n := countRisksOn(t, admin, env.tenant, "financial"); n != 0 {
		t.Fatalf("%d risk(s) assigned to the archived category, want 0", n)
	}
}

// The other order: a risk write holding the category share lock makes the
// archive wait, so the archive lands after the assignment (the old state,
// archived with the risk on it), never between the check and the write.
func TestCategoryLock_ArchiveWaitsForRiskWrite(t *testing.T) {
	admin := dbtest.NewMigratePool(t)
	app := dbtest.NewAppPool(t)
	env := taxServer(t, app, taxFixture(t, admin))

	ctx, err := tenancy.WithTenant(context.Background(), env.tenant)
	if err != nil {
		t.Fatalf("tenant context: %v", err)
	}
	tx, err := app.Begin(ctx)
	if err != nil {
		t.Fatalf("begin: %v", err)
	}
	t.Cleanup(func() { _ = tx.Rollback(context.Background()) })
	if err := tenancy.ApplyTenant(ctx, tx); err != nil {
		t.Fatalf("apply tenant: %v", err)
	}
	var pid int32
	if err := tx.QueryRow(ctx, `SELECT pg_backend_pid()`).Scan(&pid); err != nil {
		t.Fatalf("backend pid: %v", err)
	}
	// What ResolveCategoryTx takes for a risk write.
	if _, err := tx.Exec(ctx, `
		SELECT 1 FROM org_risk_categories WHERE tenant_id = $1 AND slug = 'financial' FOR SHARE`,
		env.tenant); err != nil {
		t.Fatalf("share-lock financial: %v", err)
	}

	done := make(chan lockCallResult, 1)
	go func() { done <- lockCall(env, http.MethodPost, "/v1/risks/categories/financial/archive", nil) }()
	waitBlockedBy(t, admin, pid)
	if err := tx.Commit(ctx); err != nil {
		t.Fatalf("commit: %v", err)
	}
	select {
	case r := <-done:
		if r.err != nil || r.status != http.StatusOK {
			t.Fatalf("archive: status %d (%v, %v)", r.status, r.body, r.err)
		}
	case <-time.After(30 * time.Second):
		t.Fatal("archive did not return after the share lock was released")
	}
}

// #2225: a category id from another tenant is unknown here, so the move is
// a 400 and the risk keeps its category.
func TestCategoryLock_PatchWithOtherTenantsCategoryIs400(t *testing.T) {
	admin := dbtest.NewMigratePool(t)
	app := dbtest.NewAppPool(t)
	env := taxServer(t, app, taxFixture(t, admin))
	other := taxFixture(t, admin)
	riskID := riskIDsByCategory(t, admin, env.tenant, "regulatory")[0]

	var foreignID string
	if err := admin.QueryRow(context.Background(), `
		SELECT id::text FROM org_risk_categories WHERE tenant_id = $1 AND slug = 'privacy'`,
		other).Scan(&foreignID); err != nil {
		t.Fatalf("read other tenant's category: %v", err)
	}
	status, body := taxCall(t, env, http.MethodPatch, "/v1/risks/"+riskID, env.admin,
		map[string]any{"category": foreignID})
	if status != http.StatusBadRequest {
		t.Fatalf("PATCH with another tenant's category id: status %d (%v), want 400", status, body)
	}
	if _, slug, catTenant, _ := storedCategory(t, admin, riskID); slug != "regulatory" || catTenant != env.tenant {
		t.Fatalf("after refused move: category %s of tenant %s", slug, catTenant)
	}
}

// #2225: one PATCH changing both level and category writes a
// placement_changed row and a category_changed row.
func TestCategoryLock_PatchLevelAndCategoryWritesTwoAuditRows(t *testing.T) {
	admin := dbtest.NewMigratePool(t)
	app := dbtest.NewAppPool(t)
	env := taxServer(t, app, taxFixture(t, admin))
	riskID := riskIDsByCategory(t, admin, env.tenant, "regulatory")[0]

	status, body := taxCall(t, env, http.MethodPatch, "/v1/risks/"+riskID, env.admin,
		map[string]any{"level": "company", "category": "financial"})
	if status != http.StatusOK {
		t.Fatalf("PATCH level and category: status %d (%v)", status, body)
	}
	rows, err := admin.Query(context.Background(), `
		SELECT action_type FROM risk_audit_log
		WHERE tenant_id = $1 AND risk_id = $2 ORDER BY action_type`, env.tenant, riskID)
	if err != nil {
		t.Fatalf("read audit rows: %v", err)
	}
	actions, err := pgx.CollectRows(rows, pgx.RowTo[string])
	if err != nil {
		t.Fatalf("collect audit rows: %v", err)
	}
	if strings.Join(actions, ",") != "category_changed,placement_changed" {
		t.Fatalf("audit rows = %v, want one category_changed and one placement_changed", actions)
	}
}

// #2258: POST and PATCH take a category by display name, and a name shared
// by one active and one archived category resolves to the active one.
func TestCategoryLock_DisplayNameResolvesThroughPostAndPatch(t *testing.T) {
	admin := dbtest.NewMigratePool(t)
	app := dbtest.NewAppPool(t)
	env := taxServer(t, app, taxFixture(t, admin))

	status, body := taxCall(t, env, http.MethodPost, "/v1/risks", env.admin, map[string]any{
		"title": "named category", "category": "  fInAnCiAl ",
		"inherent_score": map[string]int{"likelihood": 2, "impact": 2},
	})
	if status != http.StatusCreated {
		t.Fatalf("POST by display name: status %d (%v)", status, body)
	}
	riskID := body["risk"].(map[string]any)["id"].(string)
	if _, slug, _, _ := storedCategory(t, admin, riskID); slug != "financial" {
		t.Fatalf("POST by display name: category %s, want financial", slug)
	}

	// An archived category carrying the active `Regulatory` name. Archived
	// names are not unique, so the admin pool can add one directly.
	if _, err := admin.Exec(context.Background(), `
		INSERT INTO org_risk_categories (tenant_id, slug, name, archived)
		VALUES ($1, 'old-regulatory', 'Regulatory', true)`, env.tenant); err != nil {
		t.Fatalf("seed archived namesake: %v", err)
	}
	status, body = taxCall(t, env, http.MethodPatch, "/v1/risks/"+riskID, env.admin,
		map[string]any{"category": "regulatory "})
	if status != http.StatusOK {
		t.Fatalf("PATCH by a name one archived category shares: status %d (%v)", status, body)
	}
	if _, slug, _, _ := storedCategory(t, admin, riskID); slug != "regulatory" {
		t.Fatalf("PATCH by display name: category %s, want regulatory", slug)
	}
}

// The down migration of 20261030000000 refuses, with a message naming the
// rows, while category_changed audit rows exist.
func TestCategoryLock_DownMigrationRefusesWithCategoryChangedRows(t *testing.T) {
	admin := dbtest.NewMigratePool(t)
	app := dbtest.NewAppPool(t)
	env := taxServer(t, app, taxFixture(t, admin))
	riskID := riskIDsByCategory(t, admin, env.tenant, "regulatory")[0]
	if status, body := taxCall(t, env, http.MethodPatch, "/v1/risks/"+riskID, env.admin,
		map[string]any{"category": "financial"}); status != http.StatusOK {
		t.Fatalf("move: status %d (%v)", status, body)
	}

	down, err := os.ReadFile(riskAuditCategoryChangedDown)
	if err != nil {
		t.Fatalf("read down migration: %v", err)
	}
	ctx := context.Background()
	tx, err := admin.Begin(ctx)
	if err != nil {
		t.Fatalf("admin begin: %v", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	_, err = tx.Exec(ctx, string(down))
	if err == nil {
		t.Fatal("down migration applied while category_changed rows exist")
	}
	if !strings.Contains(err.Error(), "category_changed row(s); refusing to delete audit evidence") {
		t.Fatalf("down migration error = %v, want the category_changed refusal", err)
	}
}
