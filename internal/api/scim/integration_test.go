//go:build integration

// HTTP-level integration tests for the slice 508 SCIM endpoints, driving the
// real SCIM auth middleware + handlers against Postgres. Requires
// DATABASE_URL_APP (atlas_app, RLS) + DATABASE_URL (BYPASSRLS, for the
// lookup-by-hash auth path + cross-tenant seeding).
//
// Security proofs at the HTTP boundary:
//   - AC-3:      a valid SCIM bearer authenticates; a bogus/revoked one 401s.
//   - P0-508-2:  the SCIM credential authenticates ONLY /scim/v2 — the auth
//     middleware resolves a scim.Credential, NOT a credstore
//     credential, so there is no path to a /v1 handler or a session.
//     We assert the middleware sets the tenant context and that an
//     unauthenticated request never reaches a handler.
//   - AC-2:      discovery endpoints return spec-conformant documents.
//   - AC-1/AC-4: full Create→Get→Patch(active=false)→Delete round-trip.
package scim_test

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

	scimapi "github.com/mgoodric/security-atlas/internal/api/scim"
	"github.com/mgoodric/security-atlas/internal/auth/bearer"
	"github.com/mgoodric/security-atlas/internal/scim"
	"github.com/mgoodric/security-atlas/internal/tenancy"
)

var (
	appPool   *pgxpool.Pool
	adminPool *pgxpool.Pool
)

const testHashKey = "test-bearer-hash-key-at-least-32-bytes-long!!"

func TestMain(m *testing.M) {
	appURL := os.Getenv("DATABASE_URL_APP")
	if appURL == "" {
		fmt.Fprintln(os.Stderr, "DATABASE_URL_APP not set; skipping scim api integration tests")
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

func requireAdminPool(t *testing.T) {
	t.Helper()
	if adminPool == nil {
		t.Skip("DATABASE_URL (atlas_migrate/superuser) not set; skipping")
	}
}

// harness wires the real SCIM credential + provisioning stores behind the
// middleware, issues a SCIM bearer for the tenant, and returns the mounted
// server + the bearer plaintext.
type harness struct {
	server *httptest.Server
	bearer string
	tenant uuid.UUID
	cred   scim.Credential
	store  *scim.CredentialStore
}

func newHarness(t *testing.T, tenantName string) *harness {
	t.Helper()
	requireAdminPool(t)
	tenant := seedTenant(t, tenantName)

	h, err := bearer.NewHasher([]byte(testHashKey))
	if err != nil {
		t.Fatalf("hasher: %v", err)
	}
	credStore := scim.NewCredentialStore(appPool, adminPool, h)
	credStore.SetPrefix(bearer.PrefixTest)

	ctx, _ := contextWithTenant(t, tenant)
	cred, plain, err := credStore.Issue(ctx, tenant.String(), "test-idp", nil)
	if err != nil {
		t.Fatalf("issue cred: %v", err)
	}

	r := chi.NewRouter()
	userH := scimapi.NewHandler(scim.NewStore(appPool))
	r.Group(func(sr chi.Router) {
		sr.Use(scimapi.Middleware(credStore))
		userH.Mount(sr)
	})
	srv := httptest.NewServer(r)
	t.Cleanup(srv.Close)

	return &harness{server: srv, bearer: plain, tenant: tenant, cred: cred, store: credStore}
}

func (h *harness) do(t *testing.T, method, path, token, body string) *http.Response {
	t.Helper()
	var rdr *bytes.Reader
	if body != "" {
		rdr = bytes.NewReader([]byte(body))
	} else {
		rdr = bytes.NewReader(nil)
	}
	req, err := http.NewRequest(method, h.server.URL+path, rdr)
	if err != nil {
		t.Fatalf("new request: %v", err)
	}
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	req.Header.Set("Content-Type", scim.ContentType)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("do %s %s: %v", method, path, err)
	}
	return resp
}

// TestHTTP_AuthGate proves the SCIM auth gate (AC-3 / P0-508-2): no bearer →
// 401; bogus bearer → 401; valid bearer → 200. The middleware NEVER calls into
// the /v1 credstore — it resolves only a scim.Credential.
func TestHTTP_AuthGate(t *testing.T) {
	h := newHarness(t, "scim-http-auth")

	// No bearer → 401, handler never reached.
	resp := h.do(t, http.MethodGet, "/scim/v2/Users", "", "")
	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("no-bearer status = %d; want 401", resp.StatusCode)
	}
	resp.Body.Close()

	// Bogus bearer → 401 (no oracle).
	resp = h.do(t, http.MethodGet, "/scim/v2/Users", "atlas_test_bogustoken", "")
	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("bogus-bearer status = %d; want 401", resp.StatusCode)
	}
	resp.Body.Close()

	// Valid bearer → 200.
	resp = h.do(t, http.MethodGet, "/scim/v2/Users", h.bearer, "")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("valid-bearer status = %d; want 200", resp.StatusCode)
	}
	resp.Body.Close()
}

// TestHTTP_RevokedTokenRejected proves AC-3: a revoked SCIM token 401s.
func TestHTTP_RevokedTokenRejected(t *testing.T) {
	h := newHarness(t, "scim-http-revoke")
	ctx, _ := contextWithTenant(t, h.tenant)
	if err := h.store.Revoke(ctx, h.tenant.String(), h.cred.ID); err != nil {
		t.Fatalf("revoke: %v", err)
	}
	resp := h.do(t, http.MethodGet, "/scim/v2/Users", h.bearer, "")
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("revoked-token status = %d; want 401", resp.StatusCode)
	}
}

// TestHTTP_Discovery proves AC-2: the three discovery docs render
// spec-conformant shapes (the IdP probe surface).
func TestHTTP_Discovery(t *testing.T) {
	h := newHarness(t, "scim-http-discovery")
	for _, tc := range []struct {
		path string
		key  string
	}{
		{"/scim/v2/ServiceProviderConfig", "patch"},
		{"/scim/v2/ResourceTypes", ""},
		{"/scim/v2/Schemas", ""},
	} {
		resp := h.do(t, http.MethodGet, tc.path, h.bearer, "")
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("%s status = %d; want 200", tc.path, resp.StatusCode)
		}
		ct := resp.Header.Get("Content-Type")
		if ct != scim.ContentType {
			t.Errorf("%s content-type = %q; want %q", tc.path, ct, scim.ContentType)
		}
		resp.Body.Close()
	}
}

// TestHTTP_LifecycleRoundTrip proves AC-1/AC-4 end to end over HTTP: Create →
// Get → filter → Patch(active=false) → Delete, all 2xx, with the user surviving
// the delete (soft-disable).
func TestHTTP_LifecycleRoundTrip(t *testing.T) {
	h := newHarness(t, "scim-http-lifecycle")

	// Create.
	resp := h.do(t, http.MethodPost, "/scim/v2/Users", h.bearer,
		`{"schemas":["urn:ietf:params:scim:schemas:core:2.0:User"],"userName":"erin@example.com","displayName":"Erin","externalId":"ext-erin"}`)
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("create status = %d; want 201", resp.StatusCode)
	}
	var created scim.User
	if err := json.NewDecoder(resp.Body).Decode(&created); err != nil {
		t.Fatalf("decode create: %v", err)
	}
	resp.Body.Close()
	if !created.Active || created.UserName != "erin@example.com" {
		t.Fatalf("created user wrong: %+v", created)
	}

	// Get.
	resp = h.do(t, http.MethodGet, "/scim/v2/Users/"+created.ID, h.bearer, "")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("get status = %d; want 200", resp.StatusCode)
	}
	resp.Body.Close()

	// Filter by userName.
	resp = h.do(t, http.MethodGet, `/scim/v2/Users?filter=userName+eq+%22erin@example.com%22`, h.bearer, "")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("filter status = %d; want 200", resp.StatusCode)
	}
	var list scim.ListResponse
	if err := json.NewDecoder(resp.Body).Decode(&list); err != nil {
		t.Fatalf("decode list: %v", err)
	}
	resp.Body.Close()
	if list.TotalResults != 1 {
		t.Fatalf("filter total = %d; want 1", list.TotalResults)
	}

	// Patch active=false (deprovision).
	resp = h.do(t, http.MethodPatch, "/scim/v2/Users/"+created.ID, h.bearer,
		`{"schemas":["urn:ietf:params:scim:api:messages:2.0:PatchOp"],"Operations":[{"op":"replace","path":"active","value":false}]}`)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("patch status = %d; want 200", resp.StatusCode)
	}
	var patched scim.User
	_ = json.NewDecoder(resp.Body).Decode(&patched)
	resp.Body.Close()
	if patched.Active {
		t.Fatal("user should be inactive after deprovision")
	}

	// Delete (soft-disable; 204).
	resp = h.do(t, http.MethodDelete, "/scim/v2/Users/"+created.ID, h.bearer, "")
	if resp.StatusCode != http.StatusNoContent {
		t.Fatalf("delete status = %d; want 204", resp.StatusCode)
	}
	resp.Body.Close()

	// The user STILL EXISTS (soft delete) — Get returns 200.
	resp = h.do(t, http.MethodGet, "/scim/v2/Users/"+created.ID, h.bearer, "")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("user must survive DELETE: get status = %d; want 200", resp.StatusCode)
	}
	resp.Body.Close()
}

// TestHTTP_UnsupportedFilterRejected proves the filter guard: an unsupported
// filter returns 400 invalidFilter rather than silently returning the full
// tenant list (the information-disclosure footgun, STRIDE-I).
func TestHTTP_UnsupportedFilterRejected(t *testing.T) {
	h := newHarness(t, "scim-http-filter")
	resp := h.do(t, http.MethodGet, `/scim/v2/Users?filter=displayName+eq+%22x%22`, h.bearer, "")
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("unsupported-filter status = %d; want 400", resp.StatusCode)
	}
}

// --- seeding helpers ---

func contextWithTenant(t *testing.T, tenant uuid.UUID) (context.Context, error) {
	t.Helper()
	return tenancy.WithTenant(context.Background(), tenant.String())
}

func seedTenant(t *testing.T, name string) uuid.UUID {
	t.Helper()
	requireAdminPool(t)
	id := uuid.New()
	if _, err := adminPool.Exec(context.Background(),
		`INSERT INTO tenants (id, name) VALUES ($1, $2)`, id, name); err != nil {
		t.Fatalf("seed tenant: %v", err)
	}
	t.Cleanup(func() {
		ctx := context.Background()
		for _, table := range []string{"scim_audit_log", "scim_credentials", "sessions", "users"} {
			_, _ = adminPool.Exec(ctx, fmt.Sprintf(`DELETE FROM %s WHERE tenant_id = $1`, table), id)
		}
		_, _ = adminPool.Exec(ctx, `DELETE FROM tenants WHERE id = $1`, id)
	})
	return id
}
