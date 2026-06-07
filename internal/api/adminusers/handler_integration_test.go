//go:build integration

// Integration tests for the slice 062 admin users HTTP API. Requires
// Postgres reachable via DATABASE_URL_APP.
package adminusers_test

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"sort"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/mgoodric/security-atlas/internal/api/adminusers"
	"github.com/mgoodric/security-atlas/internal/api/authctx"
	"github.com/mgoodric/security-atlas/internal/api/credstore"
	"github.com/mgoodric/security-atlas/internal/api/tenancymw"
)

// appPool is the RLS-bound atlas_app pool (DATABASE_URL_APP). adminPool is
// the BYPASSRLS atlas_migrate pool (DATABASE_URL) — added in slice 478 for
// the cross-tenant super-admin assign/revoke/list paths. When DATABASE_URL is
// unset, adminPool stays nil and the slice-478 cross-tenant tests skip via
// their own guard (assign_integration_test.go skipIfNoAdminPool).
var (
	appPool   *pgxpool.Pool
	adminPool *pgxpool.Pool
)

func TestMain(m *testing.M) {
	url := os.Getenv("DATABASE_URL_APP")
	if url == "" {
		fmt.Fprintln(os.Stderr, "DATABASE_URL_APP not set; skipping adminusers integration tests")
		os.Exit(0)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	p, err := pgxpool.New(ctx, url)
	if err != nil {
		fmt.Fprintf(os.Stderr, "pgxpool.New: %v\n", err)
		os.Exit(1)
	}
	appPool = p
	if adminURL := os.Getenv("DATABASE_URL"); adminURL != "" {
		a, aerr := pgxpool.New(ctx, adminURL)
		if aerr != nil {
			fmt.Fprintf(os.Stderr, "pgxpool.New admin: %v\n", aerr)
			os.Exit(1)
		}
		adminPool = a
	}
	code := m.Run()
	p.Close()
	if adminPool != nil {
		adminPool.Close()
	}
	os.Exit(code)
}

// newRouter wires the handler with an admin credential and a configurable
// caller user-id so self-demotion tests can simulate the caller being
// the target user.
func newRouter(t *testing.T, tenantID uuid.UUID, callerUserID string, isAdmin bool) http.Handler {
	t.Helper()
	h := adminusers.New(appPool)

	r := chi.NewRouter()
	r.Use(func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
			ctx := authctx.WithCredential(req.Context(), credstore.Credential{
				ID:       "key_test_users",
				TenantID: tenantID.String(),
				IsAdmin:  isAdmin,
				UserID:   callerUserID,
			})
			next.ServeHTTP(w, req.WithContext(ctx))
		})
	})
	r.Use(tenancymw.Middleware)
	r.Get("/v1/admin/users", h.List)
	r.Get("/v1/admin/users/{id}", h.Get)
	r.Patch("/v1/admin/users/{id}/roles", h.PatchRoles)
	return r
}

// seedUser inserts a row directly via the app pool (no app code), running
// under the right tenant GUC. Returns the user's id.
func seedUser(t *testing.T, tenantID uuid.UUID, email, displayName string) uuid.UUID {
	t.Helper()
	id := uuid.New()
	ctx := context.Background()
	tx, err := appPool.Begin(ctx)
	if err != nil {
		t.Fatalf("seedUser begin: %v", err)
	}
	defer tx.Rollback(ctx)
	if _, err := tx.Exec(ctx, "SELECT set_config('app.current_tenant', $1, true)", tenantID.String()); err != nil {
		t.Fatalf("seedUser set_config: %v", err)
	}
	if _, err := tx.Exec(ctx,
		`INSERT INTO users (id, tenant_id, email, display_name, status)
		   VALUES ($1, $2, $3, $4, 'active')`,
		id, tenantID, email, displayName); err != nil {
		t.Fatalf("seedUser insert: %v", err)
	}
	if err := tx.Commit(ctx); err != nil {
		t.Fatalf("seedUser commit: %v", err)
	}
	return id
}

func seedUserRole(t *testing.T, tenantID uuid.UUID, userID, role string) {
	t.Helper()
	ctx := context.Background()
	tx, err := appPool.Begin(ctx)
	if err != nil {
		t.Fatalf("seedUserRole begin: %v", err)
	}
	defer tx.Rollback(ctx)
	if _, err := tx.Exec(ctx, "SELECT set_config('app.current_tenant', $1, true)", tenantID.String()); err != nil {
		t.Fatalf("seedUserRole set_config: %v", err)
	}
	if _, err := tx.Exec(ctx,
		`INSERT INTO user_roles (tenant_id, user_id, role, granted_by)
		   VALUES ($1, $2, $3, 'test-seeder')
		   ON CONFLICT DO NOTHING`,
		tenantID, userID, role); err != nil {
		t.Fatalf("seedUserRole insert: %v", err)
	}
	if err := tx.Commit(ctx); err != nil {
		t.Fatalf("seedUserRole commit: %v", err)
	}
}

func cleanupUsers(t *testing.T, tenantID uuid.UUID) {
	t.Helper()
	t.Cleanup(func() {
		ctx := context.Background()
		_, _ = appPool.Exec(ctx, "DELETE FROM user_roles WHERE tenant_id = $1", tenantID)
		_, _ = appPool.Exec(ctx, "DELETE FROM users WHERE tenant_id = $1", tenantID)
	})
}

// AC-3: GET /v1/admin/users returns the tenant's users with roles.
func TestListAdminUsers(t *testing.T) {
	tenant := uuid.New()
	cleanupUsers(t, tenant)

	u1 := seedUser(t, tenant, "alice@example.com", "Alice")
	u2 := seedUser(t, tenant, "bob@example.com", "Bob")
	seedUserRole(t, tenant, u1.String(), "admin")
	seedUserRole(t, tenant, u1.String(), "grc_engineer")
	seedUserRole(t, tenant, u2.String(), "viewer")

	r := newRouter(t, tenant, "caller", true)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/v1/admin/users", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d; body = %s", rec.Code, rec.Body.String())
	}
	var resp adminusers.ListResponse
	if err := json.NewDecoder(rec.Body).Decode(&resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(resp.Items) != 2 {
		t.Fatalf("items = %d; want 2", len(resp.Items))
	}
	// Find Alice's row and verify roles.
	for _, item := range resp.Items {
		if item.ID == u1.String() {
			roles := append([]string{}, item.Roles...)
			sort.Strings(roles)
			if len(roles) != 2 || roles[0] != "admin" || roles[1] != "grc_engineer" {
				t.Errorf("Alice roles = %v; want [admin grc_engineer]", roles)
			}
		}
	}
}

// AC-4: GET /v1/admin/users/{id} returns a single user with roles.
func TestGetAdminUser(t *testing.T) {
	tenant := uuid.New()
	cleanupUsers(t, tenant)
	uID := seedUser(t, tenant, "single@example.com", "Single User")
	seedUserRole(t, tenant, uID.String(), "auditor")

	r := newRouter(t, tenant, "caller", true)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/v1/admin/users/"+uID.String(), nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d; body = %s", rec.Code, rec.Body.String())
	}
	var resp adminusers.UserResponse
	if err := json.NewDecoder(rec.Body).Decode(&resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if resp.Email != "single@example.com" {
		t.Errorf("email = %q; want single@example.com", resp.Email)
	}
	if len(resp.Roles) != 1 || resp.Roles[0] != "auditor" {
		t.Errorf("roles = %v; want [auditor]", resp.Roles)
	}
}

// AC-5: PATCH .../roles replaces the role set.
func TestPatchRolesReplaces(t *testing.T) {
	tenant := uuid.New()
	cleanupUsers(t, tenant)
	uID := seedUser(t, tenant, "patcher@example.com", "Patcher")
	seedUserRole(t, tenant, uID.String(), "viewer")

	// Caller is a DIFFERENT user, so self-demotion guard doesn't fire.
	r := newRouter(t, tenant, "caller-not-target", true)

	body, _ := json.Marshal(adminusers.PatchRolesRequest{
		Roles: []string{"admin", "control_owner"},
	})
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, httptest.NewRequest(http.MethodPatch, "/v1/admin/users/"+uID.String()+"/roles", bytes.NewReader(body)))
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d; body = %s", rec.Code, rec.Body.String())
	}
	var resp adminusers.UserResponse
	if err := json.NewDecoder(rec.Body).Decode(&resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	roles := append([]string{}, resp.Roles...)
	sort.Strings(roles)
	if len(roles) != 2 || roles[0] != "admin" || roles[1] != "control_owner" {
		t.Errorf("roles after PATCH = %v; want [admin control_owner]", roles)
	}
}

// AC-5 / P0: self-demotion from admin without confirm_self_demotion is rejected.
func TestPatchRolesRejectsSelfDemotionWithoutConfirm(t *testing.T) {
	tenant := uuid.New()
	cleanupUsers(t, tenant)
	uID := seedUser(t, tenant, "selfdemo@example.com", "Self Demo")
	seedUserRole(t, tenant, uID.String(), "admin")

	// Caller IS the target. Dropping admin without confirm should 400.
	r := newRouter(t, tenant, uID.String(), true)
	body, _ := json.Marshal(adminusers.PatchRolesRequest{Roles: []string{"viewer"}})
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, httptest.NewRequest(http.MethodPatch, "/v1/admin/users/"+uID.String()+"/roles", bytes.NewReader(body)))
	if rec.Code != http.StatusBadRequest {
		t.Errorf("status = %d; want 400 (self-demotion rejected); body = %s", rec.Code, rec.Body.String())
	}
}

// AC-5 (counterpart): self-demotion WITH confirm_self_demotion=true succeeds.
func TestPatchRolesAllowsSelfDemotionWithConfirm(t *testing.T) {
	tenant := uuid.New()
	cleanupUsers(t, tenant)
	uID := seedUser(t, tenant, "selfdemo-ok@example.com", "Self Demo OK")
	seedUserRole(t, tenant, uID.String(), "admin")

	r := newRouter(t, tenant, uID.String(), true)
	body, _ := json.Marshal(adminusers.PatchRolesRequest{
		Roles:               []string{"viewer"},
		ConfirmSelfDemotion: true,
	})
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, httptest.NewRequest(http.MethodPatch, "/v1/admin/users/"+uID.String()+"/roles", bytes.NewReader(body)))
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d; body = %s", rec.Code, rec.Body.String())
	}
}

// PATCH rejects unknown roles.
func TestPatchRolesRejectsUnknownRole(t *testing.T) {
	tenant := uuid.New()
	cleanupUsers(t, tenant)
	uID := seedUser(t, tenant, "u@example.com", "U")

	r := newRouter(t, tenant, "caller", true)
	body, _ := json.Marshal(adminusers.PatchRolesRequest{Roles: []string{"super-admin"}})
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, httptest.NewRequest(http.MethodPatch, "/v1/admin/users/"+uID.String()+"/roles", bytes.NewReader(body)))
	if rec.Code != http.StatusBadRequest {
		t.Errorf("status = %d; want 400 (unknown role); body = %s", rec.Code, rec.Body.String())
	}
}

// Non-admin gets 403 on every endpoint.
func TestUsersRejectsNonAdmin(t *testing.T) {
	tenant := uuid.New()
	r := newRouter(t, tenant, "caller", false)

	cases := []struct {
		name string
		req  *http.Request
	}{
		{"list", httptest.NewRequest(http.MethodGet, "/v1/admin/users", nil)},
		{"get", httptest.NewRequest(http.MethodGet, "/v1/admin/users/"+uuid.New().String(), nil)},
		{"patch_roles", httptest.NewRequest(http.MethodPatch, "/v1/admin/users/"+uuid.New().String()+"/roles", bytes.NewReader([]byte(`{"roles":["admin"]}`)))},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			rec := httptest.NewRecorder()
			r.ServeHTTP(rec, tc.req)
			if rec.Code != http.StatusForbidden {
				t.Errorf("status = %d; want 403", rec.Code)
			}
		})
	}
}

// Cross-tenant: a user from tenant A is invisible to tenant B's admin.
func TestUsersTenantIsolation(t *testing.T) {
	tenantA := uuid.New()
	tenantB := uuid.New()
	cleanupUsers(t, tenantA)
	cleanupUsers(t, tenantB)
	_ = seedUser(t, tenantA, "ta-user@example.com", "TA User")
	_ = seedUser(t, tenantB, "tb-user@example.com", "TB User")

	// As tenant B admin, list should see only tenant B's users.
	r := newRouter(t, tenantB, "caller", true)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/v1/admin/users", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d", rec.Code)
	}
	var resp adminusers.ListResponse
	if err := json.NewDecoder(rec.Body).Decode(&resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	for _, item := range resp.Items {
		if item.Email == "ta-user@example.com" {
			t.Errorf("tenant B saw tenant A's user (RLS bypass)")
		}
	}
}

// Suppress unused import in case time.RFC3339 is the only consumer.
var _ = time.RFC3339
