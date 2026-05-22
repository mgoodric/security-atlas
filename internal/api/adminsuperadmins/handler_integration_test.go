//go:build integration

// Integration tests for the slice 142 super_admin management surface.
// Requires Postgres reachable via DATABASE_URL_APP. The harness opens
// an atlas_app-backed pool and seeds tenants + users + super_admins
// directly via the BYPASSRLS admin pool (DATABASE_URL).
//
// Run with:
//
//	go test -tags=integration -race ./internal/api/adminsuperadmins/...
//
// Coverage maps to slice 142 acceptance criteria:
//
//	AC-4   POST /v1/admin/super-admins (grant) happy path + idempotent
//	AC-5   DELETE /v1/admin/super-admins/{user_id} (demote) happy path
//	AC-9   Cross-tenant isolation — super_admin_audit_log NOT leaked to
//	       non-super_admin callers
//	AC-10  Last-super_admin safety rail — single + concurrent demote
//	P0-SA-1 Last-super_admin safety rail (409 Conflict)
//	P0-SA-2 super_admin_audit_log + me_audit_log written same-transaction

package adminsuperadmins_test

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"sync"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/mgoodric/security-atlas/internal/api/adminsuperadmins"
	"github.com/mgoodric/security-atlas/internal/api/authctx"
	"github.com/mgoodric/security-atlas/internal/api/credstore"
	"github.com/mgoodric/security-atlas/internal/api/tenancymw"
	"github.com/mgoodric/security-atlas/internal/auth/jwt"
	"github.com/mgoodric/security-atlas/internal/auth/jwtmw"
)

var (
	appPool   *pgxpool.Pool
	adminPool *pgxpool.Pool
)

func TestMain(m *testing.M) {
	appURL := os.Getenv("DATABASE_URL_APP")
	adminURL := os.Getenv("DATABASE_URL")
	if appURL == "" || adminURL == "" {
		fmt.Fprintln(os.Stderr, "DATABASE_URL_APP or DATABASE_URL not set; skipping adminsuperadmins integration tests")
		os.Exit(0)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	p, err := pgxpool.New(ctx, appURL)
	if err != nil {
		fmt.Fprintf(os.Stderr, "pgxpool.New app: %v\n", err)
		os.Exit(1)
	}
	appPool = p
	a, err := pgxpool.New(ctx, adminURL)
	if err != nil {
		fmt.Fprintf(os.Stderr, "pgxpool.New admin: %v\n", err)
		os.Exit(1)
	}
	adminPool = a
	code := m.Run()
	p.Close()
	a.Close()
	os.Exit(code)
}

// ----- harness -----

// seedTenant inserts a fresh tenants row under the admin pool.
func seedTenant(t *testing.T, name string) uuid.UUID {
	t.Helper()
	id := uuid.New()
	if _, err := adminPool.Exec(context.Background(),
		`INSERT INTO tenants (id, name) VALUES ($1, $2)`, id, name); err != nil {
		t.Fatalf("seed tenant: %v", err)
	}
	t.Cleanup(func() {
		_, _ = adminPool.Exec(context.Background(),
			`DELETE FROM me_audit_log WHERE tenant_id = $1`, id)
		_, _ = adminPool.Exec(context.Background(),
			`DELETE FROM tenants WHERE id = $1`, id)
	})
	return id
}

// seedSuperAdmin inserts a super_admins row with the given user_id +
// granted_via value. Registers cleanup.
func seedSuperAdmin(t *testing.T, userID uuid.UUID, grantedVia string) {
	t.Helper()
	if _, err := adminPool.Exec(context.Background(),
		`INSERT INTO super_admins (user_id, granted_via) VALUES ($1, $2)
		 ON CONFLICT (user_id) DO NOTHING`,
		userID, grantedVia); err != nil {
		t.Fatalf("seed super_admin: %v", err)
	}
	t.Cleanup(func() {
		_, _ = adminPool.Exec(context.Background(),
			`DELETE FROM super_admin_audit_log WHERE target_user_id = $1 OR actor_user_id = $1`,
			userID)
		_, _ = adminPool.Exec(context.Background(),
			`DELETE FROM super_admins WHERE user_id = $1`, userID)
	})
}

// resetSuperAdmins drops all super_admins rows so each test starts
// from a clean baseline. Also clears the audit log + any me_audit_log
// rows tagged with super_admin actions to keep cross-test assertions
// hermetic.
func resetSuperAdmins(t *testing.T) {
	t.Helper()
	if _, err := adminPool.Exec(context.Background(),
		`DELETE FROM super_admin_audit_log`); err != nil {
		t.Fatalf("clean super_admin_audit_log: %v", err)
	}
	if _, err := adminPool.Exec(context.Background(),
		`DELETE FROM super_admins`); err != nil {
		t.Fatalf("clean super_admins: %v", err)
	}
	if _, err := adminPool.Exec(context.Background(),
		`DELETE FROM me_audit_log WHERE action IN ('super_admin_grant', 'super_admin_revoke')`); err != nil {
		t.Fatalf("clean me_audit_log super_admin actions: %v", err)
	}
}

// newRouter wires the slice-142 handler under a tenancy middleware +
// a JWT claims injector. superAdmin controls whether the claims carry
// `atlas:super_admin=true`.
func newRouter(t *testing.T, tenantID uuid.UUID, userID uuid.UUID, superAdmin bool) http.Handler {
	t.Helper()
	h := adminsuperadmins.New(appPool)
	r := chi.NewRouter()
	r.Use(func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
			claims := &jwt.AtlasClaims{
				RegisteredClaims: jwt.RegisteredClaims{
					Subject: userID.String(),
				},
				CurrentTenantID:  tenantID,
				AvailableTenants: []uuid.UUID{tenantID},
				SuperAdmin:       superAdmin,
			}
			ctx := jwtmw.WithClaimsForTest(req.Context(), claims)
			// Mirror what the production jwtmw middleware does: also
			// attach a credstore.Credential so tenancymw.Middleware
			// can extract TenantID for the RLS GUC. IsAdmin mirrors
			// SuperAdmin per the slice-187 jwtmw bridge.
			cred := credstore.Credential{
				ID:       "jwt:test",
				TenantID: tenantID.String(),
				UserID:   userID.String(),
				IsAdmin:  superAdmin,
			}
			ctx = authctx.WithCredential(ctx, cred)
			next.ServeHTTP(w, req.WithContext(ctx))
		})
	})
	r.Use(tenancymw.Middleware)
	r.Get("/v1/admin/super-admins", h.List)
	r.Post("/v1/admin/super-admins", h.Grant)
	r.Delete("/v1/admin/super-admins/{user_id}", h.Demote)
	return r
}

func doRequest(t *testing.T, h http.Handler, method, path string, body any) (*http.Response, map[string]any) {
	t.Helper()
	var reader *bytes.Reader
	if body != nil {
		buf, _ := json.Marshal(body)
		reader = bytes.NewReader(buf)
	} else {
		reader = bytes.NewReader(nil)
	}
	req := httptest.NewRequest(method, path, reader)
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)
	res := rr.Result()
	out := map[string]any{}
	if res.ContentLength != 0 && res.Header.Get("Content-Type") == "application/json" {
		_ = json.NewDecoder(res.Body).Decode(&out)
	}
	return res, out
}

// ----- tests -----

// AC-4: POST /v1/admin/super-admins happy path.
func TestGrant_HappyPath(t *testing.T) {
	resetSuperAdmins(t)
	tenantID := seedTenant(t, "Tenant for grant happy")
	actorID := uuid.New()
	seedSuperAdmin(t, actorID, "bootstrap_first_install") // actor exists so demote-not-the-last works
	targetID := uuid.New()
	t.Cleanup(func() {
		_, _ = adminPool.Exec(context.Background(),
			`DELETE FROM super_admin_audit_log WHERE target_user_id = $1`, targetID)
		_, _ = adminPool.Exec(context.Background(),
			`DELETE FROM super_admins WHERE user_id = $1`, targetID)
	})

	h := newRouter(t, tenantID, actorID, true)
	res, body := doRequest(t, h, http.MethodPost, "/v1/admin/super-admins",
		map[string]any{"user_id": targetID.String()})
	if res.StatusCode != http.StatusOK {
		t.Fatalf("expected 200, got %d body=%v", res.StatusCode, body)
	}
	if body["user_id"] != targetID.String() {
		t.Errorf("user_id mismatch: %v vs %s", body["user_id"], targetID)
	}
	if body["granted_via"] != "manual_grant" {
		t.Errorf("granted_via not manual_grant: %v", body["granted_via"])
	}

	// Verify super_admins row exists.
	var count int
	_ = adminPool.QueryRow(context.Background(),
		`SELECT count(*) FROM super_admins WHERE user_id = $1`, targetID,
	).Scan(&count)
	if count != 1 {
		t.Errorf("expected super_admins row, got %d", count)
	}

	// P0-SA-2: super_admin_audit_log + me_audit_log both written.
	var saLogCount, meLogCount int
	_ = adminPool.QueryRow(context.Background(),
		`SELECT count(*) FROM super_admin_audit_log WHERE target_user_id = $1 AND action = 'super_admin_grant'`,
		targetID,
	).Scan(&saLogCount)
	if saLogCount != 1 {
		t.Errorf("expected 1 super_admin_audit_log row, got %d", saLogCount)
	}
	_ = adminPool.QueryRow(context.Background(),
		`SELECT count(*) FROM me_audit_log WHERE tenant_id = $1 AND action = 'super_admin_grant'`,
		tenantID,
	).Scan(&meLogCount)
	if meLogCount != 1 {
		t.Errorf("expected 1 me_audit_log row, got %d", meLogCount)
	}
}

// AC-4 (idempotent): re-granting an existing super_admin returns 200
// without producing a duplicate audit-log row.
func TestGrant_Idempotent(t *testing.T) {
	resetSuperAdmins(t)
	tenantID := seedTenant(t, "Tenant for grant idempotent")
	actorID := uuid.New()
	seedSuperAdmin(t, actorID, "bootstrap_first_install")
	targetID := uuid.New()
	seedSuperAdmin(t, targetID, "manual_grant")

	h := newRouter(t, tenantID, actorID, true)
	res, _ := doRequest(t, h, http.MethodPost, "/v1/admin/super-admins",
		map[string]any{"user_id": targetID.String()})
	if res.StatusCode != http.StatusOK {
		t.Fatalf("expected 200, got %d", res.StatusCode)
	}

	// No audit-log row should have been written — the row pre-existed.
	var saLogCount int
	_ = adminPool.QueryRow(context.Background(),
		`SELECT count(*) FROM super_admin_audit_log WHERE target_user_id = $1`,
		targetID,
	).Scan(&saLogCount)
	if saLogCount != 0 {
		t.Errorf("expected 0 audit rows on idempotent re-grant, got %d", saLogCount)
	}
}

// Non-super_admin caller is rejected with 403 on POST.
func TestGrant_NonSuperAdminForbidden(t *testing.T) {
	resetSuperAdmins(t)
	tenantID := seedTenant(t, "Tenant for grant 403")
	actorID := uuid.New()
	targetID := uuid.New()
	h := newRouter(t, tenantID, actorID, false /*not super_admin*/)
	res, body := doRequest(t, h, http.MethodPost, "/v1/admin/super-admins",
		map[string]any{"user_id": targetID.String()})
	if res.StatusCode != http.StatusForbidden {
		t.Fatalf("expected 403, got %d body=%v", res.StatusCode, body)
	}
}

// AC-10 (single demote): when the target is the LAST super_admin, the
// DELETE returns 409 Conflict.
func TestDemote_LastSuperAdmin_Returns409(t *testing.T) {
	resetSuperAdmins(t)
	tenantID := seedTenant(t, "Tenant last super_admin")
	actorID := uuid.New()
	seedSuperAdmin(t, actorID, "bootstrap_first_install")

	h := newRouter(t, tenantID, actorID, true)
	res, body := doRequest(t, h, http.MethodDelete,
		"/v1/admin/super-admins/"+actorID.String(), nil)
	if res.StatusCode != http.StatusConflict {
		t.Fatalf("expected 409 on last-super_admin demote, got %d body=%v", res.StatusCode, body)
	}
	// Confirm the row still exists.
	var count int
	_ = adminPool.QueryRow(context.Background(),
		`SELECT count(*) FROM super_admins WHERE user_id = $1`, actorID,
	).Scan(&count)
	if count != 1 {
		t.Errorf("super_admin row should still exist, got count=%d", count)
	}
}

// AC-5: demote happy path. count > 1; one row goes away; audit rows
// land.
func TestDemote_HappyPath(t *testing.T) {
	resetSuperAdmins(t)
	tenantID := seedTenant(t, "Tenant for demote happy")
	actorID := uuid.New()
	seedSuperAdmin(t, actorID, "bootstrap_first_install")
	targetID := uuid.New()
	seedSuperAdmin(t, targetID, "manual_grant")

	h := newRouter(t, tenantID, actorID, true)
	res, _ := doRequest(t, h, http.MethodDelete,
		"/v1/admin/super-admins/"+targetID.String(), nil)
	if res.StatusCode != http.StatusNoContent {
		t.Fatalf("expected 204, got %d", res.StatusCode)
	}

	// Target row gone; actor row preserved.
	var targetCount, actorCount int
	_ = adminPool.QueryRow(context.Background(),
		`SELECT count(*) FROM super_admins WHERE user_id = $1`, targetID,
	).Scan(&targetCount)
	if targetCount != 0 {
		t.Errorf("target super_admin should be removed, got count=%d", targetCount)
	}
	_ = adminPool.QueryRow(context.Background(),
		`SELECT count(*) FROM super_admins WHERE user_id = $1`, actorID,
	).Scan(&actorCount)
	if actorCount != 1 {
		t.Errorf("actor super_admin should still exist, got count=%d", actorCount)
	}

	// P0-SA-2: both audit rows written.
	var saLogCount, meLogCount int
	_ = adminPool.QueryRow(context.Background(),
		`SELECT count(*) FROM super_admin_audit_log WHERE target_user_id = $1 AND action = 'super_admin_revoke'`,
		targetID,
	).Scan(&saLogCount)
	if saLogCount != 1 {
		t.Errorf("expected 1 revoke audit row, got %d", saLogCount)
	}
	_ = adminPool.QueryRow(context.Background(),
		`SELECT count(*) FROM me_audit_log WHERE tenant_id = $1 AND action = 'super_admin_revoke'`,
		tenantID,
	).Scan(&meLogCount)
	if meLogCount != 1 {
		t.Errorf("expected 1 me_audit_log revoke row, got %d", meLogCount)
	}
}

// Demoting a non-super_admin user_id returns 404.
func TestDemote_NotFound(t *testing.T) {
	resetSuperAdmins(t)
	tenantID := seedTenant(t, "Tenant 404")
	actorID := uuid.New()
	seedSuperAdmin(t, actorID, "bootstrap_first_install")
	ghostID := uuid.New()
	h := newRouter(t, tenantID, actorID, true)
	res, body := doRequest(t, h, http.MethodDelete,
		"/v1/admin/super-admins/"+ghostID.String(), nil)
	if res.StatusCode != http.StatusNotFound {
		t.Fatalf("expected 404, got %d body=%v", res.StatusCode, body)
	}
}

// AC-10 (concurrent demote): two callers each see count==2 and attempt
// to demote — exactly one wins (204); the other sees the post-DELETE
// state via FOR UPDATE serialisation and is 409'd as "last
// super_admin" since the table is now down to one row + their target
// is itself the only remaining row. This proves the safety rail
// survives parallel demote attempts.
//
// The scenario: actor + two siblings. Both siblings demoted
// concurrently. End-state: actor remains; one sibling removed; one
// sibling 409 (because by the time its tx runs, count == 2 -> 1
// remains -> 1 == last super admin if its target equals the only
// remaining row — but its target is sibling1 which still exists,
// so it actually goes through). To force the safety rail under
// concurrency we need ONE actor + ONE sibling, both attempted by
// concurrent callers as targets. Race: caller A tries to demote
// sibling; caller B tries to demote the actor. Caller A wins (count
// was 2, removes sibling; tx commits). Caller B's FOR UPDATE blocks
// until A commits; then count=1 with actor as the only row; B
// targets actor; count==1 -> 409.
func TestDemote_ConcurrentLastSuperAdmin(t *testing.T) {
	resetSuperAdmins(t)
	tenantID := seedTenant(t, "Tenant concurrent demote")
	actorID := uuid.New()
	siblingID := uuid.New()
	seedSuperAdmin(t, actorID, "bootstrap_first_install")
	seedSuperAdmin(t, siblingID, "manual_grant")

	// Two callers — both are super_admins, racing.
	hActorAsCaller := newRouter(t, tenantID, actorID, true)
	hSiblingAsCaller := newRouter(t, tenantID, siblingID, true)

	type result struct {
		status int
		caller string
		target string
	}
	results := make(chan result, 2)
	var wg sync.WaitGroup
	wg.Add(2)

	// Caller A (actor) demotes sibling.
	go func() {
		defer wg.Done()
		res, _ := doRequest(t, hActorAsCaller, http.MethodDelete,
			"/v1/admin/super-admins/"+siblingID.String(), nil)
		results <- result{status: res.StatusCode, caller: "actor", target: "sibling"}
	}()
	// Caller B (sibling) demotes actor.
	go func() {
		defer wg.Done()
		res, _ := doRequest(t, hSiblingAsCaller, http.MethodDelete,
			"/v1/admin/super-admins/"+actorID.String(), nil)
		results <- result{status: res.StatusCode, caller: "sibling", target: "actor"}
	}()

	wg.Wait()
	close(results)

	statuses := []result{}
	for r := range results {
		statuses = append(statuses, r)
	}
	if len(statuses) != 2 {
		t.Fatalf("expected 2 results, got %d", len(statuses))
	}

	// Exactly one must be 204 + one must be 409. The order is non-
	// deterministic — whichever transaction commits first wins.
	successes, conflicts := 0, 0
	for _, r := range statuses {
		switch r.status {
		case http.StatusNoContent:
			successes++
		case http.StatusConflict:
			conflicts++
		default:
			t.Errorf("unexpected status %d from caller=%s target=%s", r.status, r.caller, r.target)
		}
	}
	if successes != 1 || conflicts != 1 {
		t.Fatalf("expected exactly 1 success + 1 conflict; got %d successes %d conflicts", successes, conflicts)
	}

	// Exactly one super_admins row remains.
	var remaining int
	_ = adminPool.QueryRow(context.Background(),
		`SELECT count(*) FROM super_admins`).Scan(&remaining)
	if remaining != 1 {
		t.Errorf("expected exactly 1 super_admin remaining, got %d", remaining)
	}
}

// AC-9: cross-tenant isolation. The super_admin_audit_log is platform-
// global; a non-super_admin caller cannot enumerate it via the slice-
// 142 List endpoint (which requires super_admin). Verifies the 403
// gate doesn't leak any rows.
func TestList_NonSuperAdminBlocked(t *testing.T) {
	resetSuperAdmins(t)
	tenantID := seedTenant(t, "Tenant for list 403")
	actorID := uuid.New()
	seedSuperAdmin(t, actorID, "bootstrap_first_install")

	// Caller is NOT a super_admin.
	h := newRouter(t, tenantID, uuid.New(), false)
	res, body := doRequest(t, h, http.MethodGet, "/v1/admin/super-admins", nil)
	if res.StatusCode != http.StatusForbidden {
		t.Fatalf("expected 403, got %d body=%v", res.StatusCode, body)
	}

	// List as a super_admin works.
	h2 := newRouter(t, tenantID, actorID, true)
	res2, body2 := doRequest(t, h2, http.MethodGet, "/v1/admin/super-admins", nil)
	if res2.StatusCode != http.StatusOK {
		t.Fatalf("super_admin list expected 200, got %d body=%v", res2.StatusCode, body2)
	}
}
