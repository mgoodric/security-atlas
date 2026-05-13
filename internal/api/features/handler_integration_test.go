//go:build integration

// Integration tests for the slice 059 admin features HTTP API.
// Requires Postgres reachable via DATABASE_URL_APP.

package features_test

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

	"github.com/mgoodric/security-atlas/internal/api/authctx"
	"github.com/mgoodric/security-atlas/internal/api/credstore"
	"github.com/mgoodric/security-atlas/internal/api/features"
	"github.com/mgoodric/security-atlas/internal/api/tenancymw"
	"github.com/mgoodric/security-atlas/internal/featureflag"
	"github.com/mgoodric/security-atlas/internal/tenancy"
)

var appPool *pgxpool.Pool

func TestMain(m *testing.M) {
	url := os.Getenv("DATABASE_URL_APP")
	if url == "" {
		fmt.Fprintln(os.Stderr, "DATABASE_URL_APP not set; skipping features HTTP integration tests")
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
	code := m.Run()
	p.Close()
	os.Exit(code)
}

// newRouter wires the handler with an admin or non-admin credential
// injection middleware -- mimics httpserver.go's stack (bearer auth ->
// tenancymw -> handler).
func newRouter(t *testing.T, tenantID uuid.UUID, isAdmin bool) http.Handler {
	t.Helper()
	store := featureflag.NewStore(appPool)
	h := features.New(store)

	r := chi.NewRouter()
	// Stand-in for the bearer-auth middleware (slice 014): inject a
	// canned credential.
	r.Use(func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
			ctx := authctx.WithCredential(req.Context(), credstore.Credential{
				ID:       "key_test",
				TenantID: tenantID.String(),
				IsAdmin:  isAdmin,
				UserID:   "test-user",
			})
			next.ServeHTTP(w, req.WithContext(ctx))
		})
	})
	r.Use(tenancymw.Middleware)
	r.Get("/v1/admin/features", h.List)
	r.Patch("/v1/admin/features/{key}", h.Patch)
	return r
}

// ISC-32 + ISC-37: GET /v1/admin/features returns full list as admin.
func TestAdminListReturnsAllSeedFlags(t *testing.T) {
	tenant := uuid.New()
	r := newRouter(t, tenant, true)

	req := httptest.NewRequest(http.MethodGet, "/v1/admin/features", nil)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d; body = %s", rec.Code, rec.Body.String())
	}
	var resp features.ListResponse
	if err := json.NewDecoder(rec.Body).Decode(&resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if got, want := len(resp.Items), len(featureflag.Seed); got != want {
		t.Errorf("items = %d; want %d", got, want)
	}
	t.Cleanup(func() {
		_, _ = appPool.Exec(context.Background(), "DELETE FROM feature_flags WHERE tenant_id = $1", tenant)
		_, _ = appPool.Exec(context.Background(), "DELETE FROM feature_flag_audit_log WHERE tenant_id = $1", tenant)
	})
}

// ISC-37: non-admin gets 403 on GET.
func TestAdminListRejectsNonAdmin(t *testing.T) {
	tenant := uuid.New()
	r := newRouter(t, tenant, false)

	req := httptest.NewRequest(http.MethodGet, "/v1/admin/features", nil)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)

	if rec.Code != http.StatusForbidden {
		t.Errorf("status = %d; want 403", rec.Code)
	}
}

// ISC-33 + ISC-28 + AC-7: PATCH toggles a flag.
func TestAdminPatchToggles(t *testing.T) {
	tenant := uuid.New()
	r := newRouter(t, tenant, true)

	body, _ := json.Marshal(map[string]any{"enabled": false, "reason": "no risks today"})
	req := httptest.NewRequest(http.MethodPatch, "/v1/admin/features/risk.enabled", bytes.NewReader(body))
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d; body = %s", rec.Code, rec.Body.String())
	}
	var resp features.PatchResponse
	if err := json.NewDecoder(rec.Body).Decode(&resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if resp.Key != "risk.enabled" {
		t.Errorf("key = %q; want risk.enabled", resp.Key)
	}
	if resp.Enabled {
		t.Errorf("enabled = true; want false")
	}
	if !resp.HasOverride {
		t.Errorf("has_override = false; want true")
	}

	// Confirm round-trip via List.
	listReq := httptest.NewRequest(http.MethodGet, "/v1/admin/features", nil)
	listRec := httptest.NewRecorder()
	r.ServeHTTP(listRec, listReq)
	var listResp features.ListResponse
	_ = json.NewDecoder(listRec.Body).Decode(&listResp)
	for _, item := range listResp.Items {
		if item.Key == "risk.enabled" {
			if item.Enabled {
				t.Errorf("after PATCH, list shows risk.enabled=true; want false")
			}
			if !item.HasOverride {
				t.Errorf("after PATCH, list shows has_override=false; want true")
			}
			return
		}
	}
	t.Errorf("risk.enabled not in list after PATCH")

	t.Cleanup(func() {
		_, _ = appPool.Exec(context.Background(), "DELETE FROM feature_flags WHERE tenant_id = $1", tenant)
		_, _ = appPool.Exec(context.Background(), "DELETE FROM feature_flag_audit_log WHERE tenant_id = $1", tenant)
	})
}

// ISC-38: non-admin gets 403 on PATCH.
func TestAdminPatchRejectsNonAdmin(t *testing.T) {
	tenant := uuid.New()
	r := newRouter(t, tenant, false)

	body, _ := json.Marshal(map[string]any{"enabled": false})
	req := httptest.NewRequest(http.MethodPatch, "/v1/admin/features/risk.enabled", bytes.NewReader(body))
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)

	if rec.Code != http.StatusForbidden {
		t.Errorf("status = %d; want 403", rec.Code)
	}
}

// ISC-29: PATCH on unknown key returns 404.
func TestAdminPatchUnknownKeyReturns404(t *testing.T) {
	tenant := uuid.New()
	r := newRouter(t, tenant, true)

	body, _ := json.Marshal(map[string]any{"enabled": false})
	req := httptest.NewRequest(http.MethodPatch, "/v1/admin/features/not.a.real.flag", bytes.NewReader(body))
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)

	if rec.Code != http.StatusNotFound {
		t.Errorf("status = %d; want 404", rec.Code)
	}
}

// ISC-30: PATCH on spine-forbidden key returns 400 OR 404.
// (Spine keys are absent from Seed, so the canonical path is ErrNotFound
// -> 404. If a future regression adds a spine key to Seed, the
// IsSpineForbidden check fires first and returns 400. Either is correct.)
func TestAdminPatchSpineForbiddenReturnsClientError(t *testing.T) {
	tenant := uuid.New()
	r := newRouter(t, tenant, true)

	body, _ := json.Marshal(map[string]any{"enabled": false})
	req := httptest.NewRequest(http.MethodPatch, "/v1/admin/features/rls.policies", bytes.NewReader(body))
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)

	if rec.Code != http.StatusNotFound && rec.Code != http.StatusBadRequest {
		t.Errorf("status = %d; want 400 or 404 (spine-forbidden rejection)", rec.Code)
	}
}

// AC-10 (audit log) end-to-end via HTTP: toggle off, toggle on, assert
// two audit-log rows via the Store directly (no HTTP audit-log surface
// in v1).
func TestAdminPatchWritesAuditLog(t *testing.T) {
	tenant := uuid.New()
	r := newRouter(t, tenant, true)

	for _, enabled := range []bool{false, true} {
		body, _ := json.Marshal(map[string]any{"enabled": enabled, "reason": "test"})
		req := httptest.NewRequest(http.MethodPatch, "/v1/admin/features/policy.enabled", bytes.NewReader(body))
		rec := httptest.NewRecorder()
		r.ServeHTTP(rec, req)
		if rec.Code != http.StatusOK {
			t.Fatalf("PATCH enabled=%v: status = %d; body = %s", enabled, rec.Code, rec.Body.String())
		}
	}

	// Reach into the Store directly to read the audit log (same RLS
	// context as the handler used).
	store := featureflag.NewStore(appPool)
	ctx, terr := tenancy.WithTenant(context.Background(), tenant.String())
	if terr != nil {
		t.Fatalf("WithTenant: %v", terr)
	}
	entries, err := store.AuditLog(ctx)
	if err != nil {
		t.Fatalf("AuditLog: %v", err)
	}
	count := 0
	for _, e := range entries {
		if e.FlagKey == "policy.enabled" {
			count++
			if e.Actor != "test-user" {
				t.Errorf("audit actor = %q; want test-user", e.Actor)
			}
		}
	}
	if count != 2 {
		t.Errorf("audit entries for policy.enabled = %d; want 2", count)
	}

	t.Cleanup(func() {
		_, _ = appPool.Exec(context.Background(), "DELETE FROM feature_flags WHERE tenant_id = $1", tenant)
		_, _ = appPool.Exec(context.Background(), "DELETE FROM feature_flag_audit_log WHERE tenant_id = $1", tenant)
	})
}
