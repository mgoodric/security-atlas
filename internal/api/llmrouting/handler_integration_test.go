//go:build integration

// Integration tests for the slice-499 tenant-admin routing-config endpoint
// against real Postgres + the real cloud.Store (real four-policy RLS, real
// AES-GCM encryption). Covers:
//
//   - the tenant-admin gate (P0-499 threat-model S): non-admin -> 403.
//   - the closed provider enum (P0-499-3): a free-text / URL provider -> 400.
//   - AC-3 / AC-11: the provider key is WRITE-ONLY — never echoed in the PUT or
//     GET response, never appears in the response body.
//   - set -> get -> clear round-trip through the masked API.
//
// Run with:  go test -tags=integration -p 1 ./internal/api/llmrouting/...

package llmrouting_test

import (
	"context"
	"crypto/rand"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"

	"github.com/mgoodric/security-atlas/internal/api/authctx"
	"github.com/mgoodric/security-atlas/internal/api/credstore"
	llmrouting "github.com/mgoodric/security-atlas/internal/api/llmrouting"
	"github.com/mgoodric/security-atlas/internal/dbtest"
	"github.com/mgoodric/security-atlas/internal/llm/cloud"
	"github.com/mgoodric/security-atlas/internal/tenancy"
)

// fakeKey is an obviously-fake provider key (no real sk-ant-/sk-/AKIA prefix).
const fakeKey = "handler-fake-provider-key-00000"

func testCrypter(t *testing.T) *cloud.Crypter {
	t.Helper()
	key := make([]byte, 32)
	if _, err := io.ReadFull(rand.Reader, key); err != nil {
		t.Fatalf("rand: %v", err)
	}
	c, err := cloud.NewCrypter(key)
	if err != nil {
		t.Fatalf("NewCrypter: %v", err)
	}
	return c
}

// routerFor wires the routing handler behind an injected credential (roles) and
// a tenant on the context so RLS is scoped.
func routerFor(store *cloud.Store, tenant string, roles []string, superAdmin bool) http.Handler {
	h := llmrouting.New(store)
	r := chi.NewRouter()
	r.Use(func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
			ctx := authctx.WithCredential(req.Context(), credstore.Credential{
				TenantID:   tenant,
				UserID:     "user:00000000-0000-0000-0000-000000000001",
				IsAdmin:    superAdmin,
				OwnerRoles: roles,
			})
			ctx, _ = tenancy.WithTenant(ctx, tenant)
			next.ServeHTTP(w, req.WithContext(ctx))
		})
	})
	h.RegisterRoutes(r)
	return r
}

func do(t *testing.T, h http.Handler, method, path, body string) (int, string) {
	t.Helper()
	var rdr io.Reader
	if body != "" {
		rdr = strings.NewReader(body)
	}
	req := httptest.NewRequest(method, path, rdr)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec.Code, rec.Body.String()
}

func freshTenant(t *testing.T) string {
	t.Helper()
	admin := dbtest.NewMigratePool(t)
	return dbtest.SeedTenant(t, admin, "tenant_llm_routing")
}

func TestHandler_NonAdminForbidden(t *testing.T) {
	app := dbtest.NewAppPool(t)
	tenant := freshTenant(t)
	store := cloud.NewStore(app, testCrypter(t))

	// A viewer (no admin role, not super_admin) is 403 on every verb.
	h := routerFor(store, tenant, []string{"viewer"}, false)
	for _, tc := range []struct{ method, body string }{
		{http.MethodGet, ""},
		{http.MethodPut, `{"provider":"anthropic","api_key":"x"}`},
		{http.MethodDelete, ""},
	} {
		code, _ := do(t, h, tc.method, "/v1/admin/llm-routing", tc.body)
		if code != http.StatusForbidden {
			t.Errorf("%s as viewer = %d, want 403", tc.method, code)
		}
	}
}

func TestHandler_AdminSetGetClear_KeyNeverReturned(t *testing.T) {
	app := dbtest.NewAppPool(t)
	tenant := freshTenant(t)
	store := cloud.NewStore(app, testCrypter(t))
	h := routerFor(store, tenant, []string{"admin"}, false)

	// PUT cloud provider + key.
	code, body := do(t, h, http.MethodPut, "/v1/admin/llm-routing",
		`{"provider":"anthropic","api_key":"`+fakeKey+`"}`)
	if code != http.StatusOK {
		t.Fatalf("PUT = %d: %s", code, body)
	}
	// AC-11: the key must NOT appear in the response.
	if strings.Contains(body, fakeKey) {
		t.Fatalf("PUT response leaked the key: %s", body)
	}
	var pr map[string]any
	_ = json.Unmarshal([]byte(body), &pr)
	if pr["provider"] != "anthropic" || pr["has_api_key"] != true || pr["is_cloud"] != true {
		t.Fatalf("PUT response = %v", pr)
	}

	// GET: masked, key absent.
	code, body = do(t, h, http.MethodGet, "/v1/admin/llm-routing", "")
	if code != http.StatusOK {
		t.Fatalf("GET = %d: %s", code, body)
	}
	if strings.Contains(body, fakeKey) {
		t.Fatalf("GET response leaked the key: %s", body)
	}

	// DELETE: reverts to local default.
	code, body = do(t, h, http.MethodDelete, "/v1/admin/llm-routing", "")
	if code != http.StatusOK {
		t.Fatalf("DELETE = %d: %s", code, body)
	}
	var dr map[string]any
	_ = json.Unmarshal([]byte(body), &dr)
	if dr["provider"] != "local-ollama" {
		t.Fatalf("DELETE response = %v, want local-ollama", dr)
	}
}

func TestHandler_ClosedEnum_RejectsArbitraryProvider(t *testing.T) {
	app := dbtest.NewAppPool(t)
	tenant := freshTenant(t)
	store := cloud.NewStore(app, testCrypter(t))
	h := routerFor(store, tenant, nil, true) // super_admin

	for _, prov := range []string{"https://evil.example/v1", "custom", "gpt-4", ""} {
		code, _ := do(t, h, http.MethodPut, "/v1/admin/llm-routing",
			`{"provider":"`+prov+`","api_key":"x"}`)
		if code != http.StatusBadRequest {
			t.Errorf("PUT provider=%q = %d, want 400 (closed enum / no URL)", prov, code)
		}
	}
}

func TestHandler_LocalProviderTakesNoKey(t *testing.T) {
	app := dbtest.NewAppPool(t)
	tenant := freshTenant(t)
	store := cloud.NewStore(app, testCrypter(t))
	h := routerFor(store, tenant, []string{"admin"}, false)

	code, _ := do(t, h, http.MethodPut, "/v1/admin/llm-routing",
		`{"provider":"local-ollama","api_key":"should-not-be-here"}`)
	if code != http.StatusBadRequest {
		t.Fatalf("local+key = %d, want 400", code)
	}

	// local-ollama with no key succeeds.
	code, _ = do(t, h, http.MethodPut, "/v1/admin/llm-routing",
		`{"provider":"local-ollama"}`)
	if code != http.StatusOK {
		t.Fatalf("local no-key = %d, want 200", code)
	}
}

var _ = context.Background
