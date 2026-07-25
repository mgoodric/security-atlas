//go:build integration

// Integration tests for the slice 509 admin group-role-mapping surface
// (/v1/admin/group-role-mappings). Requires DATABASE_URL_APP (atlas_app, RLS) +
// DATABASE_URL (BYPASSRLS) for the create/list/delete round-trip. Proves the
// admin-gated CRUD happy path (AC-8) end-to-end through the production
// credential + tenancy middleware chain.
package admingroupmappings_test

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/mgoodric/security-atlas/internal/api/admingroupmappings"
	"github.com/mgoodric/security-atlas/internal/api/authctx"
	"github.com/mgoodric/security-atlas/internal/api/credstore"
	"github.com/mgoodric/security-atlas/internal/api/tenancymw"
	"github.com/mgoodric/security-atlas/internal/auth/grouprole"
)

var (
	appPool   *pgxpool.Pool
	adminPool *pgxpool.Pool
)

func TestMain(m *testing.M) {
	appURL := os.Getenv("DATABASE_URL_APP")
	if appURL == "" {
		fmt.Fprintln(os.Stderr, "DATABASE_URL_APP not set; skipping admingroupmappings integration tests")
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

func seedTenant(t *testing.T, name string) uuid.UUID {
	t.Helper()
	if adminPool == nil {
		t.Skip("DATABASE_URL not set; skipping")
	}
	id := uuid.New()
	if _, err := adminPool.Exec(context.Background(),
		`INSERT INTO tenants (id, name) VALUES ($1, $2)`, id, name+"-"+id.String()[:8]); err != nil {
		t.Fatalf("seed tenant: %v", err)
	}
	t.Cleanup(func() {
		ctx := context.Background()
		_, _ = adminPool.Exec(ctx, `DELETE FROM oidc_idp_group_mappings WHERE tenant_id = $1`, id)
		_, _ = adminPool.Exec(ctx, `DELETE FROM tenants WHERE id = $1`, id)
	})
	return id
}

func newRouter(t *testing.T, tenantID uuid.UUID, isAdmin bool) http.Handler {
	t.Helper()
	handler := admingroupmappings.New(grouprole.NewStore(appPool))
	r := chi.NewRouter()
	// Inject the admin credential + tenant GUC exactly like the production /v1
	// chain (chi so chi.URLParam resolves the {id} path param on Delete).
	r.Use(func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
			ctx := authctx.WithCredential(req.Context(), credstore.Credential{
				ID:       "key_test",
				TenantID: tenantID.String(),
				IsAdmin:  isAdmin,
				UserID:   "key_test",
			})
			next.ServeHTTP(w, req.WithContext(ctx))
		})
	})
	r.Use(tenancymw.Middleware)
	r.Post("/v1/admin/group-role-mappings", handler.Create)
	r.Get("/v1/admin/group-role-mappings", handler.List)
	r.Delete("/v1/admin/group-role-mappings/{id}", handler.Delete)
	return r
}

func TestCRUDRoundTrip(t *testing.T) {
	if adminPool == nil {
		t.Skip("DATABASE_URL not set; skipping")
	}
	tenant := seedTenant(t, "agm-crud")
	router := newRouter(t, tenant, true)

	// Create.
	req := httptest.NewRequest(http.MethodPost, "/v1/admin/group-role-mappings",
		bytes.NewReader([]byte(`{"group_ref":"SecurityTeam","role":"grc_engineer"}`)))
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)
	if rec.Code != http.StatusCreated {
		t.Fatalf("create status = %d; want 201 (body=%s)", rec.Code, rec.Body.String())
	}
	var created admingroupmappings.MappingResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &created); err != nil {
		t.Fatalf("decode create: %v", err)
	}
	if created.Role != "grc_engineer" || created.GroupRef != "SecurityTeam" {
		t.Fatalf("created wrong: %+v", created)
	}
	if created.IDPConfigID != nil {
		t.Fatalf("SCIM-source mapping should have null idp_config_id, got %v", *created.IDPConfigID)
	}

	// List.
	req = httptest.NewRequest(http.MethodGet, "/v1/admin/group-role-mappings", nil)
	rec = httptest.NewRecorder()
	router.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("list status = %d; want 200", rec.Code)
	}
	var list admingroupmappings.ListResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &list); err != nil {
		t.Fatalf("decode list: %v", err)
	}
	if len(list.Items) != 1 {
		t.Fatalf("list = %d; want 1", len(list.Items))
	}

	// Delete.
	req = httptest.NewRequest(http.MethodDelete, "/v1/admin/group-role-mappings/"+created.ID, nil)
	rec = httptest.NewRecorder()
	router.ServeHTTP(rec, req)
	if rec.Code != http.StatusNoContent {
		t.Fatalf("delete status = %d; want 204", rec.Code)
	}

	// Delete again → 404.
	req = httptest.NewRequest(http.MethodDelete, "/v1/admin/group-role-mappings/"+created.ID, nil)
	rec = httptest.NewRecorder()
	router.ServeHTTP(rec, req)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("re-delete status = %d; want 404", rec.Code)
	}
}

func TestNonAdminForbidden(t *testing.T) {
	if adminPool == nil {
		t.Skip("DATABASE_URL not set; skipping")
	}
	tenant := seedTenant(t, "agm-forbidden")
	router := newRouter(t, tenant, false) // non-admin

	req := httptest.NewRequest(http.MethodGet, "/v1/admin/group-role-mappings", nil)
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("non-admin list status = %d; want 403", rec.Code)
	}
}
