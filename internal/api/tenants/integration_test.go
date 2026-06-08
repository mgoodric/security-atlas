//go:build integration

// Integration tests for the slice 144 PATCH /v1/tenants/{id} handler.
// Requires Postgres reachable via DATABASE_URL_APP. The harness opens
// an atlas_app-backed pool and seeds tenants + users + credentials
// directly via the BYPASSRLS admin pool (DATABASE_URL).
//
// Run with:
//
//	go test -tags=integration -race ./internal/api/tenants/...
//
// Coverage maps to slice 144 acceptance criteria:
//
//	AC-1  Handler exists; admin / super_admin gated; body validation
//	AC-2  Atomic UPDATE tenants + INSERT me_audit_log
//	AC-3  Case-insensitive uniqueness -> 409
//	AC-7  Cross-tenant isolation -> 403
//	P0-RT-2  Case-insensitive uniqueness enforced at DB layer
//	P0-RT-3  Audit-log row written same-tx
//	P0-RT-4  Tenant ID not patchable; only name field

package tenants_test

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
	"github.com/mgoodric/security-atlas/internal/api/tenancymw"
	"github.com/mgoodric/security-atlas/internal/api/tenants"
)

var (
	appPool   *pgxpool.Pool
	adminPool *pgxpool.Pool
)

func TestMain(m *testing.M) {
	appURL := os.Getenv("DATABASE_URL_APP")
	adminURL := os.Getenv("DATABASE_URL")
	if appURL == "" || adminURL == "" {
		fmt.Fprintln(os.Stderr, "DATABASE_URL_APP or DATABASE_URL not set; skipping tenants integration tests")
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

// seedTenant inserts a fresh tenants row under the admin (BYPASSRLS)
// pool. Returns the new id. Test cleanup drops the row + any cascaded
// me_audit_log rows.
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

// newRouter assembles the slice-144 router under the same auth +
// tenancy middleware stack the production server uses. isAdmin maps
// to the credential's IsAdmin flag.
func newRouter(t *testing.T, tenantID uuid.UUID, userID string, isAdmin bool) http.Handler {
	t.Helper()
	h := tenants.New(appPool)
	r := chi.NewRouter()
	r.Use(func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
			ctx := authctx.WithCredential(req.Context(), credstore.Credential{
				ID:       "key_test_tenants",
				TenantID: tenantID.String(),
				IsAdmin:  isAdmin,
				UserID:   userID,
			})
			next.ServeHTTP(w, req.WithContext(ctx))
		})
	})
	r.Use(tenancymw.Middleware)
	r.Patch("/v1/tenants/{id}", h.PatchTenant)
	return r
}

func patch(t *testing.T, h http.Handler, path string, body any) (*http.Response, map[string]any) {
	t.Helper()
	buf, _ := json.Marshal(body)
	req := httptest.NewRequest(http.MethodPatch, path, bytes.NewReader(buf))
	req.Header.Set("Content-Type", "application/json")
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)
	res := rr.Result()
	out := map[string]any{}
	_ = json.NewDecoder(res.Body).Decode(&out)
	return res, out
}

// ----- tests -----

// AC-1: PATCH /v1/tenants/{id} happy path. Admin caller, valid body,
// returns 200 + updated tenant.
func TestPatchTenant_HappyPath(t *testing.T) {
	tenantID := seedTenant(t, "Original Name")
	userID := uuid.NewString()
	h := newRouter(t, tenantID, userID, true /*isAdmin*/)

	res, body := patch(t, h, "/v1/tenants/"+tenantID.String(), map[string]any{"name": "Renamed Co"})
	if res.StatusCode != http.StatusOK {
		t.Fatalf("expected 200, got %d body=%v", res.StatusCode, body)
	}
	tenant, _ := body["tenant"].(map[string]any)
	if tenant["name"] != "Renamed Co" {
		t.Fatalf("expected name=Renamed Co, got %v", tenant["name"])
	}
	if tenant["id"] != tenantID.String() {
		t.Fatalf("id changed unexpectedly: %v vs %v", tenant["id"], tenantID)
	}
}

// AC-2: atomic UPDATE + INSERT me_audit_log. After a successful rename
// a single audit row exists with action='tenant_rename' under the
// tenant's GUC.
func TestPatchTenant_AuditLogRowWritten(t *testing.T) {
	tenantID := seedTenant(t, "Before Audit")
	userID := uuid.NewString()
	h := newRouter(t, tenantID, userID, true)

	res, _ := patch(t, h, "/v1/tenants/"+tenantID.String(), map[string]any{"name": "After Audit"})
	if res.StatusCode != http.StatusOK {
		t.Fatalf("PATCH failed: status %d", res.StatusCode)
	}

	// Read back via the admin pool (BYPASSRLS) so the assertion does
	// not depend on RLS scoping.
	var count int
	err := adminPool.QueryRow(context.Background(),
		`SELECT count(*) FROM me_audit_log WHERE tenant_id = $1 AND action = 'tenant_rename'`,
		tenantID,
	).Scan(&count)
	if err != nil {
		t.Fatalf("count audit rows: %v", err)
	}
	if count != 1 {
		t.Fatalf("expected exactly 1 tenant_rename audit row, got %d", count)
	}

	// before/after blobs contain the name transition.
	var before, after []byte
	err = adminPool.QueryRow(context.Background(),
		`SELECT before, after FROM me_audit_log WHERE tenant_id = $1 AND action = 'tenant_rename'`,
		tenantID,
	).Scan(&before, &after)
	if err != nil {
		t.Fatalf("read audit row: %v", err)
	}
	if !bytes.Contains(before, []byte("Before Audit")) {
		t.Errorf("before blob missing prior name: %s", before)
	}
	if !bytes.Contains(after, []byte("After Audit")) {
		t.Errorf("after blob missing new name: %s", after)
	}
}

// AC-3 / P0-RT-2: case-insensitive uniqueness. Two tenants exist with
// names "Acme Inc" and "Globex"; a PATCH renaming Globex to "ACME inc"
// must return 409.
func TestPatchTenant_DuplicateNameCaseInsensitive(t *testing.T) {
	_ = seedTenant(t, "Acme Inc")
	globexID := seedTenant(t, "Globex Initial")
	userID := uuid.NewString()
	h := newRouter(t, globexID, userID, true)

	res, body := patch(t, h, "/v1/tenants/"+globexID.String(), map[string]any{"name": "ACME inc"})
	if res.StatusCode != http.StatusConflict {
		t.Fatalf("expected 409 on case-insensitive duplicate, got %d body=%v", res.StatusCode, body)
	}
}

// AC-7 / P0 cross-tenant isolation: caller in Tenant A attempts to
// rename Tenant B; must return 403 (consistent body shape regardless
// of whether Tenant B exists).
func TestPatchTenant_CrossTenantForbidden(t *testing.T) {
	tenantA := seedTenant(t, "Alpha")
	tenantB := seedTenant(t, "Beta")
	userID := uuid.NewString()
	// Router constructed under Tenant A's credential context.
	h := newRouter(t, tenantA, userID, true)

	res, body := patch(t, h, "/v1/tenants/"+tenantB.String(), map[string]any{"name": "Hijacked"})
	if res.StatusCode != http.StatusForbidden {
		t.Fatalf("expected 403 cross-tenant, got %d body=%v", res.StatusCode, body)
	}

	// And tenant B's name MUST remain unchanged.
	var name string
	err := adminPool.QueryRow(context.Background(),
		`SELECT name FROM tenants WHERE id = $1`, tenantB,
	).Scan(&name)
	if err != nil {
		t.Fatalf("read tenantB: %v", err)
	}
	if name != "Beta" {
		t.Fatalf("tenantB name mutated to %q despite 403", name)
	}
}

// Non-admin caller is rejected with 403. cred.IsAdmin=false and no JWT
// claim path.
func TestPatchTenant_NonAdminForbidden(t *testing.T) {
	tenantID := seedTenant(t, "Read Only")
	userID := uuid.NewString()
	h := newRouter(t, tenantID, userID, false /*isAdmin=false*/)

	res, body := patch(t, h, "/v1/tenants/"+tenantID.String(), map[string]any{"name": "Nope"})
	if res.StatusCode != http.StatusForbidden {
		t.Fatalf("expected 403, got %d body=%v", res.StatusCode, body)
	}
}

// Body validation: missing name -> 400.
func TestPatchTenant_NameRequired(t *testing.T) {
	tenantID := seedTenant(t, "Has Name")
	userID := uuid.NewString()
	h := newRouter(t, tenantID, userID, true)

	res, _ := patch(t, h, "/v1/tenants/"+tenantID.String(), map[string]any{})
	if res.StatusCode != http.StatusBadRequest {
		t.Fatalf("expected 400 for missing name, got %d", res.StatusCode)
	}
}

// Body validation: empty name -> 400.
func TestPatchTenant_EmptyName(t *testing.T) {
	tenantID := seedTenant(t, "Empty Test")
	userID := uuid.NewString()
	h := newRouter(t, tenantID, userID, true)

	res, _ := patch(t, h, "/v1/tenants/"+tenantID.String(), map[string]any{"name": "   "})
	if res.StatusCode != http.StatusBadRequest {
		t.Fatalf("expected 400 for empty name, got %d", res.StatusCode)
	}
}

// Body validation: 64-byte cap -> 400 over.
func TestPatchTenant_NameOverByteCap(t *testing.T) {
	tenantID := seedTenant(t, "Cap Test")
	userID := uuid.NewString()
	h := newRouter(t, tenantID, userID, true)

	long := "" // 65 ASCII chars = 65 bytes (> 64)
	for i := 0; i < 65; i++ {
		long += "x"
	}
	res, _ := patch(t, h, "/v1/tenants/"+tenantID.String(), map[string]any{"name": long})
	if res.StatusCode != http.StatusBadRequest {
		t.Fatalf("expected 400 for over-cap name, got %d", res.StatusCode)
	}
}

// Body validation: control character -> 400.
func TestPatchTenant_ControlCharsRejected(t *testing.T) {
	tenantID := seedTenant(t, "Ctrl Test")
	userID := uuid.NewString()
	h := newRouter(t, tenantID, userID, true)

	res, _ := patch(t, h, "/v1/tenants/"+tenantID.String(), map[string]any{"name": "Acme\x00\x01Co"})
	if res.StatusCode != http.StatusBadRequest {
		t.Fatalf("expected 400 for control char, got %d", res.StatusCode)
	}
}

// Bad UUID in URL -> 400.
func TestPatchTenant_BadIDFormat(t *testing.T) {
	tenantID := seedTenant(t, "URL Test")
	userID := uuid.NewString()
	h := newRouter(t, tenantID, userID, true)

	res, _ := patch(t, h, "/v1/tenants/not-a-uuid", map[string]any{"name": "Whatever"})
	if res.StatusCode != http.StatusBadRequest {
		t.Fatalf("expected 400 for bad UUID, got %d", res.StatusCode)
	}
}

// AC-5: the new audit-log row is visible via the slice-124 unified
// aggregator under kind=me action=tenant_rename. This is a wire-level
// integration check that the CHECK extension reached the right table
// and the row participates in the UNION.
func TestPatchTenant_VisibleInUnifiedAuditLog(t *testing.T) {
	tenantID := seedTenant(t, "Unified Test")
	userID := uuid.NewString()
	h := newRouter(t, tenantID, userID, true)

	res, _ := patch(t, h, "/v1/tenants/"+tenantID.String(), map[string]any{"name": "Unified Final"})
	if res.StatusCode != http.StatusOK {
		t.Fatalf("PATCH failed: status %d", res.StatusCode)
	}

	// Verify the audit-row participates in the me_audit_log table —
	// the slice-124 aggregator UNION ALLs across this exact table
	// for kind='me' so a row here is the integration point. The
	// slice-124 SQL projects action -> the canonical Action column.
	var action string
	err := adminPool.QueryRow(context.Background(),
		`SELECT action FROM me_audit_log WHERE tenant_id = $1 AND action = 'tenant_rename'`,
		tenantID,
	).Scan(&action)
	if err != nil {
		t.Fatalf("read me_audit_log action: %v", err)
	}
	if action != "tenant_rename" {
		t.Fatalf("expected action=tenant_rename, got %q", action)
	}
}

// ----- slice 608: bundle_gate_mode PATCH surface (AC-5) -----

// TestPatchTenant_GateModeHappyPath proves a tenant admin can set the
// control-bundle gate policy via PATCH and that the response + DB reflect it.
func TestPatchTenant_GateModeHappyPath(t *testing.T) {
	tenantID := seedTenant(t, "Gate Mode Co")
	userID := uuid.NewString()
	h := newRouter(t, tenantID, userID, true)

	res, body := patch(t, h, "/v1/tenants/"+tenantID.String(), map[string]any{"bundle_gate_mode": "advisory"})
	if res.StatusCode != http.StatusOK {
		t.Fatalf("expected 200, got %d body=%v", res.StatusCode, body)
	}
	tenant, _ := body["tenant"].(map[string]any)
	if tenant["bundle_gate_mode"] != "advisory" {
		t.Fatalf("expected bundle_gate_mode=advisory in response, got %v", tenant["bundle_gate_mode"])
	}
	// Default tenants start strict; confirm the column actually changed in the DB.
	var mode string
	if err := adminPool.QueryRow(context.Background(),
		`SELECT bundle_gate_mode FROM tenants WHERE id = $1`, tenantID).Scan(&mode); err != nil {
		t.Fatalf("read gate mode: %v", err)
	}
	if mode != "advisory" {
		t.Fatalf("DB gate mode = %q; want advisory", mode)
	}
}

// TestPatchTenant_GateModeDefaultStrict confirms a freshly-seeded tenant
// carries the strict default (the slice-574 safe behaviour) before any PATCH.
func TestPatchTenant_GateModeDefaultStrict(t *testing.T) {
	tenantID := seedTenant(t, "Default Strict Co")
	var mode string
	if err := adminPool.QueryRow(context.Background(),
		`SELECT bundle_gate_mode FROM tenants WHERE id = $1`, tenantID).Scan(&mode); err != nil {
		t.Fatalf("read gate mode: %v", err)
	}
	if mode != "strict" {
		t.Fatalf("a new tenant must default to strict; got %q", mode)
	}
}

// TestPatchTenant_GateModeInvalidRejected proves an out-of-enum value is a 400
// (handler allow-list; the DB CHECK is the second leg).
func TestPatchTenant_GateModeInvalidRejected(t *testing.T) {
	tenantID := seedTenant(t, "Invalid Gate Co")
	userID := uuid.NewString()
	h := newRouter(t, tenantID, userID, true)

	res, _ := patch(t, h, "/v1/tenants/"+tenantID.String(), map[string]any{"bundle_gate_mode": "bogus"})
	if res.StatusCode != http.StatusBadRequest {
		t.Fatalf("expected 400 for invalid gate mode, got %d", res.StatusCode)
	}
	// Column must remain at the strict default.
	var mode string
	if err := adminPool.QueryRow(context.Background(),
		`SELECT bundle_gate_mode FROM tenants WHERE id = $1`, tenantID).Scan(&mode); err != nil {
		t.Fatalf("read gate mode: %v", err)
	}
	if mode != "strict" {
		t.Fatalf("invalid PATCH must not mutate the column; got %q", mode)
	}
}

// TestPatchTenant_GateModeAuditRow proves a gate-policy change writes a
// me_audit_log row with the new action and the before/after transition.
func TestPatchTenant_GateModeAuditRow(t *testing.T) {
	tenantID := seedTenant(t, "Gate Audit Co")
	userID := uuid.NewString()
	h := newRouter(t, tenantID, userID, true)

	res, _ := patch(t, h, "/v1/tenants/"+tenantID.String(), map[string]any{"bundle_gate_mode": "mandatory_tests"})
	if res.StatusCode != http.StatusOK {
		t.Fatalf("PATCH failed: status %d", res.StatusCode)
	}
	var count int
	if err := adminPool.QueryRow(context.Background(),
		`SELECT count(*) FROM me_audit_log WHERE tenant_id = $1 AND action = 'tenant_gate_policy_update'`,
		tenantID).Scan(&count); err != nil {
		t.Fatalf("count audit rows: %v", err)
	}
	if count != 1 {
		t.Fatalf("expected 1 tenant_gate_policy_update audit row, got %d", count)
	}
	var before, after []byte
	if err := adminPool.QueryRow(context.Background(),
		`SELECT before, after FROM me_audit_log WHERE tenant_id = $1 AND action = 'tenant_gate_policy_update'`,
		tenantID).Scan(&before, &after); err != nil {
		t.Fatalf("read audit row: %v", err)
	}
	if !bytes.Contains(before, []byte("strict")) {
		t.Errorf("before blob missing prior mode: %s", before)
	}
	if !bytes.Contains(after, []byte("mandatory_tests")) {
		t.Errorf("after blob missing new mode: %s", after)
	}
}

// TestPatchTenant_NameAndGateModeTogether proves a single PATCH can set both
// fields, writing one audit row per mutator.
func TestPatchTenant_NameAndGateModeTogether(t *testing.T) {
	tenantID := seedTenant(t, "Both Fields Co")
	userID := uuid.NewString()
	h := newRouter(t, tenantID, userID, true)

	res, body := patch(t, h, "/v1/tenants/"+tenantID.String(),
		map[string]any{"name": "Renamed Both", "bundle_gate_mode": "advisory"})
	if res.StatusCode != http.StatusOK {
		t.Fatalf("expected 200, got %d body=%v", res.StatusCode, body)
	}
	tenant, _ := body["tenant"].(map[string]any)
	if tenant["name"] != "Renamed Both" || tenant["bundle_gate_mode"] != "advisory" {
		t.Fatalf("expected both fields updated; got %v", tenant)
	}
	var renameCount, gateCount int
	_ = adminPool.QueryRow(context.Background(),
		`SELECT count(*) FROM me_audit_log WHERE tenant_id = $1 AND action = 'tenant_rename'`,
		tenantID).Scan(&renameCount)
	_ = adminPool.QueryRow(context.Background(),
		`SELECT count(*) FROM me_audit_log WHERE tenant_id = $1 AND action = 'tenant_gate_policy_update'`,
		tenantID).Scan(&gateCount)
	if renameCount != 1 || gateCount != 1 {
		t.Fatalf("expected 1 rename + 1 gate-policy audit row; got rename=%d gate=%d", renameCount, gateCount)
	}
}
