//go:build integration

// Integration tests for the slice 508 admin SCIM-credential surface
// (/v1/admin/scim-credentials). Requires DATABASE_URL_APP (atlas_app, RLS) +
// DATABASE_URL (BYPASSRLS) for the issue/list/revoke round-trip and the
// lookup-by-hash auth path.
package adminscim_test

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

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/mgoodric/security-atlas/internal/api/adminscim"
	"github.com/mgoodric/security-atlas/internal/api/authctx"
	"github.com/mgoodric/security-atlas/internal/api/credstore"
	"github.com/mgoodric/security-atlas/internal/api/tenancymw"
	"github.com/mgoodric/security-atlas/internal/auth/bearer"
	"github.com/mgoodric/security-atlas/internal/scim"
)

var (
	appPool   *pgxpool.Pool
	adminPool *pgxpool.Pool
)

const testHashKey = "test-bearer-hash-key-at-least-32-bytes-long!!"

func TestMain(m *testing.M) {
	appURL := os.Getenv("DATABASE_URL_APP")
	if appURL == "" {
		fmt.Fprintln(os.Stderr, "DATABASE_URL_APP not set; skipping adminscim integration tests")
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
		`INSERT INTO tenants (id, name) VALUES ($1, $2)`, id, name); err != nil {
		t.Fatalf("seed tenant: %v", err)
	}
	t.Cleanup(func() {
		ctx := context.Background()
		_, _ = adminPool.Exec(ctx, `DELETE FROM scim_credentials WHERE tenant_id = $1`, id)
		_, _ = adminPool.Exec(ctx, `DELETE FROM tenants WHERE id = $1`, id)
	})
	return id
}

// newRouter wires the admin SCIM handler behind a credential-injecting
// middleware + the tenancy middleware (which sets app.current_tenant from the
// credential), mirroring the production chain for the /v1 admin surface.
func newRouter(t *testing.T, tenantID uuid.UUID, isAdmin bool) http.Handler {
	t.Helper()
	h, err := bearer.NewHasher([]byte(testHashKey))
	if err != nil {
		t.Fatalf("hasher: %v", err)
	}
	store := scim.NewCredentialStore(appPool, adminPool, h)
	store.SetPrefix(bearer.PrefixTest)
	handler := adminscim.New(store)

	mux := http.NewServeMux()
	chain := func(next http.HandlerFunc) http.Handler {
		var hh http.Handler = next
		hh = tenancymw.Middleware(hh)
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			ctx := authctx.WithCredential(r.Context(), credstore.Credential{
				ID:       "key_test",
				TenantID: tenantID.String(),
				IsAdmin:  isAdmin,
				UserID:   "key_test",
			})
			hh.ServeHTTP(w, r.WithContext(ctx))
		})
	}
	mux.Handle("POST /v1/admin/scim-credentials", chain(handler.Issue))
	mux.Handle("GET /v1/admin/scim-credentials", chain(handler.List))
	return mux
}

func TestIssueListAndAuthenticate(t *testing.T) {
	if adminPool == nil {
		t.Skip("DATABASE_URL not set; skipping")
	}
	tenant := seedTenant(t, "adminscim-issue")
	router := newRouter(t, tenant, true)

	// Issue.
	req := httptest.NewRequest(http.MethodPost, "/v1/admin/scim-credentials",
		bytes.NewReader([]byte(`{"description":"Okta prod"}`)))
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)
	if rec.Code != http.StatusCreated {
		t.Fatalf("issue status = %d; want 201 (body=%s)", rec.Code, rec.Body.String())
	}
	var issued adminscim.IssueResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &issued); err != nil {
		t.Fatalf("decode issue: %v", err)
	}
	if issued.BearerToken == "" || issued.Last4 == "" {
		t.Fatalf("issue response missing bearer/last4: %+v", issued)
	}

	// The issued token authenticates via the SCIM store.
	h, _ := bearer.NewHasher([]byte(testHashKey))
	store := scim.NewCredentialStore(appPool, adminPool, h)
	cred, err := store.Authenticate(context.Background(), issued.BearerToken)
	if err != nil {
		t.Fatalf("authenticate issued token: %v", err)
	}
	if cred.TenantID != tenant {
		t.Fatalf("authenticated tenant = %s; want %s", cred.TenantID, tenant)
	}

	// List shows it (no bearer plaintext).
	req = httptest.NewRequest(http.MethodGet, "/v1/admin/scim-credentials", nil)
	rec = httptest.NewRecorder()
	router.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("list status = %d; want 200", rec.Code)
	}
	var list adminscim.ListResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &list); err != nil {
		t.Fatalf("decode list: %v", err)
	}
	if len(list.Items) != 1 || list.Items[0].Last4 != issued.Last4 {
		t.Fatalf("list wrong: %+v", list.Items)
	}
}

func TestNonAdminForbidden(t *testing.T) {
	if adminPool == nil {
		t.Skip("DATABASE_URL not set; skipping")
	}
	tenant := seedTenant(t, "adminscim-forbidden")
	router := newRouter(t, tenant, false) // non-admin

	req := httptest.NewRequest(http.MethodPost, "/v1/admin/scim-credentials",
		bytes.NewReader([]byte(`{"description":"x"}`)))
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("non-admin issue status = %d; want 403", rec.Code)
	}
}
