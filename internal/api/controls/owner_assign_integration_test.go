//go:build integration

// Slice 468 — integration tests for server-backed control owner-assignment
// (single-item + bulk) + the saved-views surface. Real Postgres, real RLS
// (atlas_app NOSUPERUSER NOBYPASSRLS), real tenancy GUC.
//
// THE LOAD-BEARING TEST is TestBulkAssign_CrossTenant_Amplifier (AC-11):
// tenant B cannot bulk-assign tenant A's control through the bulk path. The
// bulk path is the SAME per-item check the single-item path runs
// (assignOwnerInTx), so it is provably not weaker. RLS hides the
// cross-tenant control, the per-item ControlExistsInTenant returns false,
// the whole transaction rolls back, and NOTHING in tenant A is mutated.
//
// Deliberate-weakening proof (recorded in docs/audit-log/468-...):
// commenting out the `if !exists { return assignErrControlNotFound }` branch
// in assignOwnerInTx makes this test FAIL (the cross-tenant control gets
// owner-assigned and a tenant-A row appears) — i.e. the test bites. The
// shipped code keeps the check; the proof is run locally, never committed
// weakened.
//
// These drive the routes through a router carrying credential-injection +
// tenancymw (the export_integration_test.go precedent), so RLS is active
// exactly as production. The authz-middleware ROLE gate is separately proven
// by internal/authz's matrix test; here we exercise the per-item TENANT
// amplifier + the audit + the cap, which live in the handler + RLS.

package controls_test

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/mgoodric/security-atlas/internal/api/authctx"
	controlsapi "github.com/mgoodric/security-atlas/internal/api/controls"
	"github.com/mgoodric/security-atlas/internal/api/credstore"
	"github.com/mgoodric/security-atlas/internal/api/tenancymw"
	"github.com/mgoodric/security-atlas/internal/dbtest"
)

// ----- harness -----

// ownerAssignServer mounts the slice-468 routes behind credential-injection
// (a real UUID userID so the handler's actor parse succeeds) + tenancymw, on
// a real atlas_app (RLS) pool. The cap (200) is enforced server-side.
func ownerAssignServer(t *testing.T, app *pgxpool.Pool, tenant, userID string) *httptest.Server {
	t.Helper()
	ownerH := controlsapi.NewOwnerAssignHandler(app)
	savedH := controlsapi.NewSavedViewsHandler(app)
	r := chi.NewRouter()
	r.Use(func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
			ctx := authctx.WithCredential(req.Context(), credstore.Credential{
				ID:       "test-owner-assign",
				TenantID: tenant,
				IsAdmin:  true, // -> admin role at BuildInput; write-on-controls
				UserID:   userID,
			})
			next.ServeHTTP(w, req.WithContext(ctx))
		})
	})
	r.Use(tenancymw.Middleware)
	r.Post("/v1/controls/{id}/owner", ownerH.AssignOwner)
	r.Post("/v1/controls:bulk-assign-owner", ownerH.BulkAssignOwner)
	r.Get("/v1/saved-views", savedH.List)
	r.Post("/v1/saved-views", savedH.Create)
	r.Delete("/v1/saved-views/{id}", savedH.Delete)
	ts := httptest.NewServer(r)
	t.Cleanup(ts.Close)
	return ts
}

// seedControl inserts ONE active control for the tenant (migrate pool, so
// the FK target exists cross-RLS). Returns its id.
func seedControl(t *testing.T, migrate *pgxpool.Pool, tenant string) uuid.UUID {
	t.Helper()
	id := uuid.New()
	if _, err := migrate.Exec(context.Background(),
		`INSERT INTO controls (id, tenant_id, bundle_id, title, control_family, implementation_type, owner_role)
		 VALUES ($1, $2, $3, $4, 'IAC', 'manual_attested', 'control_owner')`,
		id, tenant, "bundle-468-"+id.String()[:8], "slice-468 control "+id.String()[:8]); err != nil {
		t.Fatalf("seed control: %v", err)
	}
	return id
}

// seedUser inserts an active user for the tenant; returns its id. The bulk +
// single paths validate the target owner against this row (UserExistsInTenant).
func seedUser(t *testing.T, migrate *pgxpool.Pool, tenant string) uuid.UUID {
	t.Helper()
	id := uuid.New()
	if _, err := migrate.Exec(context.Background(),
		`INSERT INTO users (id, tenant_id, email, display_name, status)
		 VALUES ($1, $2, $3, 'Slice 468 User', 'active')`,
		id, tenant, id.String()[:8]+"@example.test"); err != nil {
		t.Fatalf("seed user: %v", err)
	}
	return id
}

func ownerAssignCleanupTables() []string {
	// children before parents (FK-safe). The audit log + assignments
	// reference controls/users; saved_views is independent.
	return []string{
		"control_owner_assignment_audit_log",
		"control_owner_assignments",
		"saved_views",
		"controls",
		"users",
	}
}

func postJSON(t *testing.T, url, body string) (int, []byte) {
	t.Helper()
	req, _ := http.NewRequestWithContext(context.Background(), http.MethodPost, url, bytes.NewBufferString(body))
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("POST %s: %v", url, err)
	}
	defer func() { _ = resp.Body.Close() }()
	raw, _ := io.ReadAll(resp.Body)
	return resp.StatusCode, raw
}

// ----- AC-10: bulk applies to all + audits -----

func TestBulkAssign_AppliesToAll_AndAudits(t *testing.T) {
	migrate := dbtest.NewMigratePool(t)
	app := dbtest.NewAppPool(t)
	tenant := dbtest.SeedTenant(t, migrate, ownerAssignCleanupTables()...)

	owner := seedUser(t, migrate, tenant)
	actor := seedUser(t, migrate, tenant)
	c1 := seedControl(t, migrate, tenant)
	c2 := seedControl(t, migrate, tenant)
	c3 := seedControl(t, migrate, tenant)

	ts := ownerAssignServer(t, app, tenant, actor.String())
	body := fmt.Sprintf(`{"owner_user_id":%q,"control_ids":[%q,%q,%q]}`,
		owner, c1, c2, c3)
	status, raw := postJSON(t, ts.URL+"/v1/controls:bulk-assign-owner", body)
	if status != http.StatusOK {
		t.Fatalf("bulk-assign: expected 200; got %d body=%s", status, raw)
	}

	// Every control now carries the owner assignment. Assertion reads use
	// the migrate (BYPASSRLS) pool so the assertion is about what is
	// ACTUALLY in the table, not what RLS happens to surface (dbtest
	// README: migrate pool for fixture reads; app pool for RLS assertions —
	// the RLS assertion is AC-11 below, this is a presence assertion).
	ctx := context.Background()
	for _, cid := range []uuid.UUID{c1, c2, c3} {
		var got uuid.UUID
		if err := migrate.QueryRow(ctx,
			`SELECT owner_user_id FROM control_owner_assignments WHERE tenant_id=$1 AND control_id=$2`,
			tenant, cid).Scan(&got); err != nil {
			t.Fatalf("read assignment for %s: %v", cid, err)
		}
		if got != owner {
			t.Fatalf("control %s owner = %s; want %s", cid, got, owner)
		}
	}

	// ONE bulk audit row referencing the whole set (threat-model R / AC-8).
	var (
		isBulk     bool
		controlIDs []uuid.UUID
		auditOwner uuid.UUID
		auditActor uuid.UUID
	)
	if err := migrate.QueryRow(ctx,
		`SELECT is_bulk, control_ids, owner_user_id, actor_user_id
		 FROM control_owner_assignment_audit_log WHERE tenant_id=$1`,
		tenant).Scan(&isBulk, &controlIDs, &auditOwner, &auditActor); err != nil {
		t.Fatalf("read audit row: %v", err)
	}
	if !isBulk {
		t.Fatalf("audit row is_bulk = false; want true")
	}
	if len(controlIDs) != 3 {
		t.Fatalf("audit control_ids len = %d; want 3 (the affected set)", len(controlIDs))
	}
	if auditOwner != owner || auditActor != actor {
		t.Fatalf("audit owner/actor mismatch: owner=%s actor=%s", auditOwner, auditActor)
	}
}

// ----- AC-11: the authz amplifier (LOAD-BEARING) -----

// TestBulkAssign_CrossTenant_Amplifier proves the bulk path is NOT weaker
// than the single-item path: tenant B cannot bulk-assign tenant A's control
// through the bulk path. The per-item ControlExistsInTenant check (the same
// one the single-item path runs) returns false under tenant B's RLS context,
// the transaction rolls back, and tenant A's control is untouched.
func TestBulkAssign_CrossTenant_Amplifier(t *testing.T) {
	migrate := dbtest.NewMigratePool(t)
	app := dbtest.NewAppPool(t)
	tenantA := dbtest.SeedTenant(t, migrate, ownerAssignCleanupTables()...)
	tenantB := dbtest.SeedTenant(t, migrate, ownerAssignCleanupTables()...)

	// A control + owner live in tenant A.
	controlA := seedControl(t, migrate, tenantA)
	ownerA := seedUser(t, migrate, tenantA)
	// Tenant B's actor + a tenant-B owner (so the owner is not the failing
	// factor — the CONTROL being cross-tenant is).
	actorB := seedUser(t, migrate, tenantB)
	ownerB := seedUser(t, migrate, tenantB)

	// Tenant B's server tries to bulk-assign tenant A's control.
	tsB := ownerAssignServer(t, app, tenantB, actorB.String())
	body := fmt.Sprintf(`{"owner_user_id":%q,"control_ids":[%q]}`, ownerB, controlA)
	status, raw := postJSON(t, tsB.URL+"/v1/controls:bulk-assign-owner", body)

	// The cross-tenant control is invisible to tenant B (RLS) → per-item
	// check fails → 404, whole batch rolls back. NEVER a 200.
	if status != http.StatusNotFound {
		t.Fatalf("AMPLIFIER BREACH: tenant B bulk-assigned tenant A's control; expected 404, got %d body=%s",
			status, raw)
	}

	// PROOF the mutation did not happen, read through the migrate
	// (BYPASSRLS) pool so we see the GROUND TRUTH of the table — not just
	// what RLS hides. If the per-item check were removed, the row WOULD be
	// here (that is the deliberate-weakening failure mode this asserts
	// against). tenant A's control has NO owner assignment.
	ctx := context.Background()
	var n int
	if err := migrate.QueryRow(ctx,
		`SELECT count(*) FROM control_owner_assignments WHERE control_id=$1`,
		controlA).Scan(&n); err != nil {
		t.Fatalf("count tenant-A assignments: %v", err)
	}
	if n != 0 {
		t.Fatalf("AMPLIFIER BREACH: tenant A control got %d owner assignments via tenant B's bulk path; want 0", n)
	}
	// And ownerA was never used (sanity — the breach would have used ownerB,
	// but assert tenant A is pristine).
	_ = ownerA

	// Tenant B wrote no audit row either (the tx rolled back before the
	// audit insert).
	if err := migrate.QueryRow(ctx,
		`SELECT count(*) FROM control_owner_assignment_audit_log WHERE tenant_id=$1`,
		tenantB).Scan(&n); err != nil {
		t.Fatalf("count tenant-B audit: %v", err)
	}
	if n != 0 {
		t.Fatalf("expected no tenant-B audit row on a rolled-back bulk; got %d", n)
	}
}

// TestSingleAssign_CrossTenant_NotFound is the single-item half of the
// amplifier symmetry: the same cross-tenant control is 404 via the
// single-item path too. The bulk path being not-weaker means BOTH reject it.
func TestSingleAssign_CrossTenant_NotFound(t *testing.T) {
	migrate := dbtest.NewMigratePool(t)
	app := dbtest.NewAppPool(t)
	tenantA := dbtest.SeedTenant(t, migrate, ownerAssignCleanupTables()...)
	tenantB := dbtest.SeedTenant(t, migrate, ownerAssignCleanupTables()...)

	controlA := seedControl(t, migrate, tenantA)
	actorB := seedUser(t, migrate, tenantB)
	ownerB := seedUser(t, migrate, tenantB)

	tsB := ownerAssignServer(t, app, tenantB, actorB.String())
	body := fmt.Sprintf(`{"owner_user_id":%q}`, ownerB)
	status, raw := postJSON(t, tsB.URL+"/v1/controls/"+controlA.String()+"/owner", body)
	if status != http.StatusNotFound {
		t.Fatalf("single-item cross-tenant: expected 404; got %d body=%s", status, raw)
	}
}

// TestBulkAssign_BadOwner_FailsBatch proves the target-owner validation:
// an owner that is not a tenant user fails the WHOLE batch (no silent
// partial apply — P0-448-2), even if every control id is valid.
func TestBulkAssign_BadOwner_FailsBatch(t *testing.T) {
	migrate := dbtest.NewMigratePool(t)
	app := dbtest.NewAppPool(t)
	tenant := dbtest.SeedTenant(t, migrate, ownerAssignCleanupTables()...)

	actor := seedUser(t, migrate, tenant)
	c1 := seedControl(t, migrate, tenant)
	c2 := seedControl(t, migrate, tenant)
	bogusOwner := uuid.New() // not a users row in this tenant

	ts := ownerAssignServer(t, app, tenant, actor.String())
	body := fmt.Sprintf(`{"owner_user_id":%q,"control_ids":[%q,%q]}`, bogusOwner, c1, c2)
	status, raw := postJSON(t, ts.URL+"/v1/controls:bulk-assign-owner", body)
	if status != http.StatusUnprocessableEntity {
		t.Fatalf("bad-owner bulk: expected 422; got %d body=%s", status, raw)
	}
	// No partial apply: neither control got an assignment (ground truth via
	// the migrate pool).
	var n int
	if err := migrate.QueryRow(context.Background(),
		`SELECT count(*) FROM control_owner_assignments WHERE tenant_id=$1`, tenant).Scan(&n); err != nil {
		t.Fatalf("count assignments: %v", err)
	}
	if n != 0 {
		t.Fatalf("silent partial apply: %d assignments written on a failed batch; want 0", n)
	}
}

// ----- AC-13: over-cap rejected -----

func TestBulkAssign_OverCap_Rejected(t *testing.T) {
	migrate := dbtest.NewMigratePool(t)
	app := dbtest.NewAppPool(t)
	tenant := dbtest.SeedTenant(t, migrate, ownerAssignCleanupTables()...)
	owner := seedUser(t, migrate, tenant)
	actor := seedUser(t, migrate, tenant)

	// 201 ids (> BulkAssignCap = 200). They need not exist — the cap check
	// fires BEFORE any per-item work (threat-model D).
	ids := make([]string, 201)
	for i := range ids {
		ids[i] = fmt.Sprintf("%q", uuid.New())
	}
	idsJSON := "[" + joinComma(ids) + "]"
	ts := ownerAssignServer(t, app, tenant, actor.String())
	body := fmt.Sprintf(`{"owner_user_id":%q,"control_ids":%s}`, owner, idsJSON)
	status, raw := postJSON(t, ts.URL+"/v1/controls:bulk-assign-owner", body)
	if status != http.StatusUnprocessableEntity {
		t.Fatalf("over-cap: expected 422; got %d body=%s", status, raw)
	}
	if !bytes.Contains(raw, []byte("cap")) {
		t.Fatalf("over-cap error should mention the cap; got %s", raw)
	}
}

func joinComma(ss []string) string {
	out := ""
	for i, s := range ss {
		if i > 0 {
			out += ","
		}
		out += s
	}
	return out
}

// ----- AC-12: saved-view isolation (per-user) -----

// TestSavedViews_PerUserIsolation proves a saved view persists + re-reads for
// its owner, and another user IN THE SAME TENANT cannot read it (threat-model
// I / P0-448-5). The tenant half is RLS; the user half is the query's
// mandatory user_id predicate.
func TestSavedViews_PerUserIsolation(t *testing.T) {
	migrate := dbtest.NewMigratePool(t)
	app := dbtest.NewAppPool(t)
	tenant := dbtest.SeedTenant(t, migrate, ownerAssignCleanupTables()...)

	alice := seedUser(t, migrate, tenant)
	bob := seedUser(t, migrate, tenant)

	// Alice saves a view.
	tsAlice := ownerAssignServer(t, app, tenant, alice.String())
	status, raw := postJSON(t, tsAlice.URL+"/v1/saved-views",
		`{"name":"Weekly triage","filters":{"family":"IAC","bogus":"dropme"}}`)
	if status != http.StatusCreated {
		t.Fatalf("alice create view: expected 201; got %d body=%s", status, raw)
	}
	var created struct {
		ID      string            `json:"id"`
		Name    string            `json:"name"`
		Filters map[string]string `json:"filters"`
	}
	if err := json.Unmarshal(raw, &created); err != nil {
		t.Fatalf("decode created view: %v", err)
	}
	// Filter validation dropped the unknown key (threat-model T).
	if _, bad := created.Filters["bogus"]; bad {
		t.Fatalf("unknown filter key 'bogus' was persisted; want dropped")
	}
	if created.Filters["family"] != "IAC" {
		t.Fatalf("known filter key 'family' missing; got %#v", created.Filters)
	}

	// Alice reads her own view back.
	if views := listViews(t, tsAlice.URL); len(views) != 1 || views[0].Name != "Weekly triage" {
		t.Fatalf("alice list: want 1 view 'Weekly triage'; got %#v", views)
	}

	// Bob (same tenant, different user) sees ZERO of Alice's views.
	tsBob := ownerAssignServer(t, app, tenant, bob.String())
	if views := listViews(t, tsBob.URL); len(views) != 0 {
		t.Fatalf("ISOLATION BREACH: bob read alice's view; got %#v", views)
	}

	// Bob cannot delete Alice's view (404 — scoped to bob's user_id).
	req, _ := http.NewRequestWithContext(context.Background(), http.MethodDelete,
		tsBob.URL+"/v1/saved-views/"+created.ID, nil)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("bob delete alice view: %v", err)
	}
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("ISOLATION BREACH: bob delete alice's view returned %d; want 404", resp.StatusCode)
	}
	// Alice's view still exists.
	if views := listViews(t, tsAlice.URL); len(views) != 1 {
		t.Fatalf("alice's view vanished after bob's delete attempt; got %#v", views)
	}
}

type savedViewWire struct {
	ID      string            `json:"id"`
	Name    string            `json:"name"`
	Filters map[string]string `json:"filters"`
}

func listViews(t *testing.T, base string) []savedViewWire {
	t.Helper()
	req, _ := http.NewRequestWithContext(context.Background(), http.MethodGet, base+"/v1/saved-views", nil)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("list views: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()
	raw, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("list views: expected 200; got %d body=%s", resp.StatusCode, raw)
	}
	var out struct {
		Views []savedViewWire `json:"views"`
	}
	if err := json.Unmarshal(raw, &out); err != nil {
		t.Fatalf("decode views: %v", err)
	}
	return out.Views
}

// TestSavedViews_DuplicateName_409 proves the case-insensitive name-unique
// constraint surfaces as a 409 (the client's duplicate-name UX).
func TestSavedViews_DuplicateName_409(t *testing.T) {
	migrate := dbtest.NewMigratePool(t)
	app := dbtest.NewAppPool(t)
	tenant := dbtest.SeedTenant(t, migrate, ownerAssignCleanupTables()...)
	alice := seedUser(t, migrate, tenant)
	ts := ownerAssignServer(t, app, tenant, alice.String())

	if status, raw := postJSON(t, ts.URL+"/v1/saved-views", `{"name":"Triage","filters":{"family":"IAC"}}`); status != http.StatusCreated {
		t.Fatalf("first create: expected 201; got %d body=%s", status, raw)
	}
	// Case-insensitive duplicate.
	status, raw := postJSON(t, ts.URL+"/v1/saved-views", `{"name":"triage","filters":{"result":"pass"}}`)
	if status != http.StatusConflict {
		t.Fatalf("duplicate name: expected 409; got %d body=%s", status, raw)
	}
}
