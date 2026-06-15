//go:build integration

package schemaregistry_test

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/mgoodric/security-atlas/internal/api"
	"github.com/mgoodric/security-atlas/internal/api/schemaregistry"
	"github.com/mgoodric/security-atlas/internal/api/testjwt"
	"github.com/mgoodric/security-atlas/internal/dbtest"
)

const (
	tenantA = "11111111-1111-1111-1111-111111111111"
	tenantB = "22222222-2222-2222-2222-222222222222"
)

// wipeAndImport removes any pre-existing schema rows and re-imports the
// platform bundle. Tests are unit-isolated by always running against a
// clean state for evidence_kind_schemas.
func wipeAndImport(t *testing.T) (*schemaregistry.Service, *pgxpool.Pool) {
	t.Helper()
	admin := dbtest.NewMigratePool(t)
	if _, err := admin.Exec(context.Background(), "DELETE FROM evidence_kind_schemas"); err != nil {
		t.Fatalf("wipe: %v", err)
	}
	app := dbtest.NewAppPool(t)
	svc := schemaregistry.NewService(app)
	// Run the import via the admin pool (BYPASSRLS) because global rows
	// have tenant_id NULL.
	importSvc := schemaregistry.NewService(admin)
	if _, _, err := importSvc.ImportPlatformSchemas(context.Background(), schemaregistry.PlatformSchemasFS()); err != nil {
		t.Fatalf("ImportPlatformSchemas: %v", err)
	}
	if err := svc.LoadFromDB(context.Background()); err != nil {
		t.Fatalf("LoadFromDB: %v", err)
	}
	return svc, app
}

func setupHTTP(t *testing.T, tenant string, admin bool) (*httptest.Server, string, *schemaregistry.Service) {
	t.Helper()
	svc, appPool := wipeAndImport(t)
	srv := api.New(api.Config{
		RotationGrace:  time.Hour,
		SchemaRegistry: svc,
	})
	srv.AttachDB(appPool)
	// Slice 197: JWT bearer via slice 190 path. AdminFor matches the
	// legacy IssueBootstrapAdminCredential (SuperAdmin=true → jwtmw
	// synthesizes IsAdmin=true on the credential); ViewerFor matches
	// IssueBootstrapCredential (no elevation).
	var bearer string
	if admin {
		bearer = srv.IssueTestJWT(t, testjwt.AdminFor(uuid.MustParse(tenant)))
	} else {
		bearer = srv.IssueTestJWT(t, testjwt.ViewerFor(uuid.MustParse(tenant)))
	}
	handler := srv.HTTPHandlerForTests()
	if handler == nil {
		t.Fatal("HTTPHandlerForTests returned nil")
	}
	ts := httptest.NewServer(handler)
	t.Cleanup(ts.Close)
	return ts, bearer, svc
}

func reqJSON(t *testing.T, ts *httptest.Server, method, path, bearer string, body any) (*http.Response, []byte) {
	t.Helper()
	var rdr io.Reader
	if body != nil {
		buf, err := json.Marshal(body)
		if err != nil {
			t.Fatalf("marshal: %v", err)
		}
		rdr = bytes.NewReader(buf)
	}
	req, err := http.NewRequestWithContext(context.Background(), method, ts.URL+path, rdr)
	if err != nil {
		t.Fatalf("NewRequest: %v", err)
	}
	if bearer != "" {
		req.Header.Set("Authorization", "Bearer "+bearer)
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("Do %s %s: %v", method, path, err)
	}
	defer func() { _ = resp.Body.Close() }()
	bb, _ := io.ReadAll(resp.Body)
	return resp, bb
}

// AC-3: ten v1 platform schemas ship in the bundle.
func TestImport_TenPlatformSchemas(t *testing.T) {
	_, _ = wipeAndImport(t)
	app := dbtest.NewAppPool(t)
	// Set the GUC so RLS lets us see tenant_id NULL rows (the policy is
	// tenant_id IS NULL OR current_tenant_matches; the NULL branch is
	// always visible regardless of the GUC, but the test sets it anyway
	// for parity with real traffic).
	conn, err := app.Acquire(context.Background())
	if err != nil {
		t.Fatalf("acquire: %v", err)
	}
	defer conn.Release()
	_, _ = conn.Exec(context.Background(), "SELECT set_config('app.current_tenant', $1, false)", tenantA)

	var count int
	if err := conn.QueryRow(context.Background(),
		"SELECT count(*) FROM evidence_kind_schemas WHERE tenant_id IS NULL").Scan(&count); err != nil {
		t.Fatalf("count: %v", err)
	}
	// The slice-014 platform bundle ships >=10 schemas. Later slices (044, ...)
	// add more under the same embedded directory. Assert a floor and let the
	// presence checks below catch any specific kind that was dropped.
	if count < 10 {
		t.Fatalf("expected at least 10 bundled schemas, got %d", count)
	}
	// Spot-check expected kinds.
	expectKinds := []string{
		"sast.scan_result.v1",
		"access_review.completion.v1",
		"manual.attestation.v1",
		"aws.s3.bucket_encryption_state.v1",
		"github.repo_protection.v1",
		"okta.mfa_policy.v1",
		"1password.org_policy.v1",
		"osquery.host_posture.v1",
		"jira.ticket_evidence.v1",
		"manual.upload.v1",
	}
	for _, k := range expectKinds {
		var n int
		if err := conn.QueryRow(context.Background(),
			"SELECT count(*) FROM evidence_kind_schemas WHERE kind=$1", k).Scan(&n); err != nil {
			t.Fatalf("count %s: %v", k, err)
		}
		if n == 0 {
			t.Errorf("expected at least one row for %s", k)
		}
	}
}

// AC-1: GET /v1/schemas returns global + tenant private.
func TestList_GlobalAndTenant(t *testing.T) {
	ts, bearer, svc := setupHTTP(t, tenantA, true)
	// Register a private kind first.
	body := map[string]any{
		"kind":   "internal.acl_summary.v1",
		"semver": "1.0.0",
		"owner":  "platform-team@example.com",
		"schema": map[string]any{
			"$schema": "https://json-schema.org/draft/2020-12/schema",
			"type":    "object",
			"properties": map[string]any{
				"summary": map[string]any{"type": "string"},
			},
			"required": []string{"summary"},
		},
	}
	resp, _ := reqJSON(t, ts, http.MethodPost, "/v1/schemas", bearer, body)
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("POST private kind = %d; want 201", resp.StatusCode)
	}
	svc.InvalidateTenant(tenantA)

	// Now list.
	resp, raw := reqJSON(t, ts, http.MethodGet, "/v1/schemas", bearer, nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("GET list = %d; body=%s", resp.StatusCode, raw)
	}
	var got struct {
		Schemas []map[string]any `json:"schemas"`
	}
	if err := json.Unmarshal(raw, &got); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	foundGlobal := false
	foundPrivate := false
	for _, s := range got.Schemas {
		if s["kind"] == "sast.scan_result.v1" {
			foundGlobal = true
			if s["scope"] != "global" {
				t.Errorf("bundled kind should be scope=global; got %v", s["scope"])
			}
		}
		if s["kind"] == "internal.acl_summary.v1" {
			foundPrivate = true
			if s["scope"] != "tenant" {
				t.Errorf("registered kind should be scope=tenant; got %v", s["scope"])
			}
		}
	}
	if !foundGlobal {
		t.Error("global kind not in list")
	}
	if !foundPrivate {
		t.Error("tenant-private kind not in list")
	}
}

// AC-2: GET /v1/schemas/{kind}/{semver}.
func TestGet_OneSchema(t *testing.T) {
	ts, bearer, _ := setupHTTP(t, tenantA, false)
	resp, raw := reqJSON(t, ts, http.MethodGet, "/v1/schemas/sast.scan_result.v1/1.0.0", bearer, nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("GET = %d; body=%s", resp.StatusCode, raw)
	}
	var got struct {
		Schema map[string]any `json:"schema"`
	}
	if err := json.Unmarshal(raw, &got); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if got.Schema["kind"] != "sast.scan_result.v1" || got.Schema["semver"] != "1.0.0" {
		t.Fatalf("unexpected payload: %+v", got.Schema)
	}
	if got.Schema["scope"] != "global" {
		t.Errorf("bundled scope should be global, got %v", got.Schema["scope"])
	}
	// 404 path.
	resp, _ = reqJSON(t, ts, http.MethodGet, "/v1/schemas/no.such.kind/1.0.0", bearer, nil)
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("unknown kind: status=%d; want 404", resp.StatusCode)
	}
}

// AC-4 (admin-only) + anti-criterion (no anonymous, no non-admin).
func TestRegister_AdminOnly(t *testing.T) {
	ts, nonAdminBearer, _ := setupHTTP(t, tenantA, false)
	body := map[string]any{
		"kind":   "internal.foo.v1",
		"semver": "1.0.0",
		"owner":  "x@example.com",
		"schema": map[string]any{"type": "object"},
	}
	// Anonymous → 401.
	resp, _ := reqJSON(t, ts, http.MethodPost, "/v1/schemas", "", body)
	if resp.StatusCode != http.StatusUnauthorized {
		t.Errorf("anon POST = %d; want 401", resp.StatusCode)
	}
	// Non-admin → 403.
	resp, raw := reqJSON(t, ts, http.MethodPost, "/v1/schemas", nonAdminBearer, body)
	if resp.StatusCode != http.StatusForbidden {
		t.Fatalf("non-admin POST = %d; want 403; body=%s", resp.StatusCode, raw)
	}
}

// AC-4 owner required.
func TestRegister_OwnerRequired(t *testing.T) {
	ts, bearer, _ := setupHTTP(t, tenantA, true)
	body := map[string]any{
		"kind":   "internal.no_owner.v1",
		"semver": "1.0.0",
		"owner":  "",
		"schema": map[string]any{"type": "object"},
	}
	resp, raw := reqJSON(t, ts, http.MethodPost, "/v1/schemas", bearer, body)
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("missing owner = %d; want 400; body=%s", resp.StatusCode, raw)
	}
	if !bytes.Contains(raw, []byte("owner")) {
		t.Errorf("error message should mention owner; got %s", raw)
	}
}

// AC-5 silent-major-bump rejected.
func TestRegister_SilentMajorBumpRejected(t *testing.T) {
	ts, bearer, _ := setupHTTP(t, tenantA, true)
	bodyV1 := map[string]any{
		"kind":   "internal.semver_test.v1",
		"semver": "1.0.0",
		"owner":  "x@example.com",
		"schema": map[string]any{"type": "object"},
	}
	resp, raw := reqJSON(t, ts, http.MethodPost, "/v1/schemas", bearer, bodyV1)
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("v1 register = %d; body=%s", resp.StatusCode, raw)
	}
	bodyJump := map[string]any{
		"kind":   "internal.semver_test.v1",
		"semver": "3.0.0",
		"owner":  "x@example.com",
		"schema": map[string]any{"type": "object"},
	}
	resp, raw = reqJSON(t, ts, http.MethodPost, "/v1/schemas", bearer, bodyJump)
	if resp.StatusCode != http.StatusConflict {
		t.Fatalf("silent major-bump = %d; want 409; body=%s", resp.StatusCode, raw)
	}
}

// AC-5 minor bump must be additive (no field removal).
func TestRegister_MinorBumpAdditive(t *testing.T) {
	ts, bearer, _ := setupHTTP(t, tenantA, true)
	v1 := map[string]any{
		"kind":   "internal.additive_test.v1",
		"semver": "1.0.0",
		"owner":  "x@example.com",
		"schema": map[string]any{
			"type": "object",
			"properties": map[string]any{
				"a": map[string]any{"type": "string"},
				"b": map[string]any{"type": "integer"},
			},
		},
	}
	resp, raw := reqJSON(t, ts, http.MethodPost, "/v1/schemas", bearer, v1)
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("v1 = %d; body=%s", resp.StatusCode, raw)
	}
	// Minor bump that REMOVES field b — not additive.
	v1m := map[string]any{
		"kind":   "internal.additive_test.v1",
		"semver": "1.1.0",
		"owner":  "x@example.com",
		"schema": map[string]any{
			"type": "object",
			"properties": map[string]any{
				"a": map[string]any{"type": "string"},
			},
		},
	}
	resp, raw = reqJSON(t, ts, http.MethodPost, "/v1/schemas", bearer, v1m)
	if resp.StatusCode != http.StatusBadRequest && resp.StatusCode != http.StatusConflict {
		t.Fatalf("non-additive minor bump = %d; want 400 or 409; body=%s", resp.StatusCode, raw)
	}
	if !bytes.Contains(raw, []byte("additive")) {
		t.Errorf("error message should mention additive; got %s", raw)
	}
	// Minor bump that ADDS field c — additive.
	v1mAdd := map[string]any{
		"kind":   "internal.additive_test.v1",
		"semver": "1.1.0",
		"owner":  "x@example.com",
		"schema": map[string]any{
			"type": "object",
			"properties": map[string]any{
				"a": map[string]any{"type": "string"},
				"b": map[string]any{"type": "integer"},
				"c": map[string]any{"type": "boolean"},
			},
		},
	}
	resp, raw = reqJSON(t, ts, http.MethodPost, "/v1/schemas", bearer, v1mAdd)
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("additive minor bump = %d; want 201; body=%s", resp.StatusCode, raw)
	}
}

// Anti-criterion: no cross-tenant leak. Tenant B must not see Tenant A's
// private kind.
func TestRegister_TenantIsolated(t *testing.T) {
	tsA, bearerA, _ := setupHTTP(t, tenantA, true)
	private := map[string]any{
		"kind":   "internal.private_kind.v1",
		"semver": "1.0.0",
		"owner":  "a@example.com",
		"schema": map[string]any{"type": "object"},
	}
	resp, raw := reqJSON(t, tsA, http.MethodPost, "/v1/schemas", bearerA, private)
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("A register = %d; body=%s", resp.StatusCode, raw)
	}
	tsA.Close()

	// Independent server bound to tenant B's bearer.
	app := dbtest.NewAppPool(t)
	t.Cleanup(app.Close)
	svcB := schemaregistry.NewService(app)
	if err := svcB.LoadFromDB(context.Background()); err != nil {
		t.Fatalf("LoadFromDB: %v", err)
	}
	srvB := api.New(api.Config{RotationGrace: time.Hour, SchemaRegistry: svcB})
	srvB.AttachDB(app)
	bearerB := srvB.IssueTestJWT(t, testjwt.AdminFor(uuid.MustParse(tenantB)))
	handlerB := srvB.HTTPHandlerForTests()
	tsB := httptest.NewServer(handlerB)
	t.Cleanup(tsB.Close)

	// GET tenant A's private kind from tenant B → 404.
	resp, raw = reqJSON(t, tsB, http.MethodGet, "/v1/schemas/internal.private_kind.v1/1.0.0", bearerB, nil)
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("B GET of A's private kind = %d; want 404; body=%s", resp.StatusCode, raw)
	}
	// LIST from tenant B — must not contain the private kind.
	resp, raw = reqJSON(t, tsB, http.MethodGet, "/v1/schemas", bearerB, nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("B list = %d; body=%s", resp.StatusCode, raw)
	}
	if bytes.Contains(raw, []byte("internal.private_kind.v1")) {
		t.Errorf("tenant B leak: list contains A's private kind: %s", raw)
	}
}

// AC-4 + anti-criterion: owner attribution is enforced. Empty owner is a
// 400; the schema body's owner is what gets stored.
func TestRegister_OwnerStored(t *testing.T) {
	ts, bearer, _ := setupHTTP(t, tenantA, true)
	body := map[string]any{
		"kind":   "internal.owner_test.v1",
		"semver": "1.0.0",
		"owner":  "alice@example.com",
		"schema": map[string]any{"type": "object"},
	}
	resp, raw := reqJSON(t, ts, http.MethodPost, "/v1/schemas", bearer, body)
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("register = %d; body=%s", resp.StatusCode, raw)
	}
	var got struct {
		Schema map[string]any `json:"schema"`
	}
	if err := json.Unmarshal(raw, &got); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if got.Schema["owner"] != "alice@example.com" {
		t.Fatalf("owner = %v; want alice@example.com", got.Schema["owner"])
	}
}

// AC-6 — the validation hook integrates with the slice 013 push path. We
// can't drive the gRPC push end-to-end here without slice 013, but we
// can verify the hook itself: a registered schema rejects a malformed
// payload and accepts a conforming one.
func TestValidatePayload_Hook(t *testing.T) {
	svc, _ := wipeAndImport(t)
	good := []byte(`{"tool":"semgrep","findings_count":0}`)
	bad := []byte(`{"findings_count":-1}`)
	if err := svc.ValidatePayload(context.Background(), tenantA, "sast.scan_result.v1", "1.0.0", good); err != nil {
		t.Errorf("good payload should validate: %v", err)
	}
	if err := svc.ValidatePayload(context.Background(), tenantA, "sast.scan_result.v1", "1.0.0", bad); err == nil {
		t.Error("bad payload (missing required tool, negative findings_count) should fail validation")
	}
	if err := svc.ValidatePayload(context.Background(), tenantA, "no.such.kind", "1.0.0", good); err == nil {
		t.Error("unknown kind should fail")
	}
}

// Migration round-trip: down then up rebuilds the table cleanly.
func TestMigration_RoundTrip(t *testing.T) {
	if os.Getenv("DATABASE_URL") == "" {
		t.Skip("DATABASE_URL not set")
	}
	// Apply down then up via psql-equivalent SQL statements directly.
	admin := dbtest.NewMigratePool(t)
	// down
	if _, err := admin.Exec(context.Background(), "DROP TABLE IF EXISTS evidence_kind_schemas"); err != nil {
		t.Fatalf("down: %v", err)
	}
	// Read the forward migration and apply.
	up, err := os.ReadFile("../../../migrations/sql/20260511000002_schema_registry.sql")
	if err != nil {
		t.Fatalf("read forward: %v", err)
	}
	// Strip leading "--" comments to keep the test focused on DDL; psql
	// accepts the file as-is. pgx Exec handles multi-statement scripts.
	if _, err := admin.Exec(context.Background(), string(up)); err != nil {
		t.Fatalf("re-apply: %v", err)
	}
	// Sanity: insert one row through atlas_app's RLS path.
	app := dbtest.NewAppPool(t)
	ctx := context.Background()
	conn, err := app.Acquire(ctx)
	if err != nil {
		t.Fatalf("acquire: %v", err)
	}
	defer conn.Release()
	if _, err := conn.Exec(ctx, "SELECT set_config('app.current_tenant', $1, false)", tenantA); err != nil {
		t.Fatalf("guc: %v", err)
	}
	id := "33333333-3333-3333-3333-333333333333"
	_, err = conn.Exec(ctx, `
        INSERT INTO evidence_kind_schemas
            (id, tenant_id, kind, semver, major, minor, patch, schema_json, owner, created_by)
        VALUES ($1, $2, 'x.y.v1', '1.0.0', 1, 0, 0, '{}'::jsonb, 'tester', 'rls-test')
    `, id, tenantA)
	if err != nil {
		t.Fatalf("insert post-roundtrip: %v", err)
	}
	// Verify another tenant cannot read it.
	if _, err := conn.Exec(ctx, "SELECT set_config('app.current_tenant', $1, false)", tenantB); err != nil {
		t.Fatalf("guc: %v", err)
	}
	var n int
	if err := conn.QueryRow(ctx, "SELECT count(*) FROM evidence_kind_schemas WHERE id=$1", id).Scan(&n); err != nil {
		t.Fatalf("count: %v", err)
	}
	if n != 0 {
		t.Fatalf("RLS leak: tenant B sees %d rows; want 0", n)
	}
	// Cleanup.
	admPool := dbtest.NewMigratePool(t)
	defer admPool.Close()
	_, _ = admPool.Exec(ctx, "DELETE FROM evidence_kind_schemas WHERE id=$1", id)
}

// Sanity: the schema body returned by GET round-trips as valid JSON.
func TestGet_SchemaIsValidJSON(t *testing.T) {
	ts, bearer, _ := setupHTTP(t, tenantA, false)
	resp, raw := reqJSON(t, ts, http.MethodGet, "/v1/schemas/manual.upload.v1/1.0.0", bearer, nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("GET = %d; body=%s", resp.StatusCode, raw)
	}
	var env struct {
		Schema map[string]any `json:"schema"`
	}
	if err := json.Unmarshal(raw, &env); err != nil {
		t.Fatalf("decode envelope: %v", err)
	}
	body, ok := env.Schema["schema"]
	if !ok {
		t.Fatal("schema body missing")
	}
	jb, err := json.Marshal(body)
	if err != nil {
		t.Fatalf("re-marshal: %v", err)
	}
	if !strings.Contains(string(jb), "x-evidence-kind") {
		t.Errorf("schema body does not contain x-evidence-kind: %s", jb)
	}
}

// Sanity: list endpoint paginates.
func TestList_Pagination(t *testing.T) {
	ts, bearer, _ := setupHTTP(t, tenantA, false)
	resp, raw := reqJSON(t, ts, http.MethodGet, "/v1/schemas?limit=3", bearer, nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("GET = %d; body=%s", resp.StatusCode, raw)
	}
	var got struct {
		Schemas []map[string]any `json:"schemas"`
	}
	if err := json.Unmarshal(raw, &got); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if len(got.Schemas) != 3 {
		t.Fatalf("limit=3 returned %d schemas", len(got.Schemas))
	}
}

// (Helper to keep the file self-contained for go vet's "unused import" check.)
var _ = fmt.Sprintf
