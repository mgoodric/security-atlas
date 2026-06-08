//go:build integration

// Slice 589 — integration tests for the OSCAL vendor-claim read API + the
// operator accept/reject/needs-info disposition. Real Postgres + the
// assembled platform router, so the test exercises the full request path:
// tenancy middleware, RLS, the sqlc query layer. The reads + disposition
// write are over persisted rows — this NEVER touches the compliance-trestle
// bridge, so the suite runs in CI without the Python oscal-bridge.
//
// Run with:
//
//	go test -tags=integration -race ./internal/api/oscalcomponents/...
//
// Requires DATABASE_URL_APP (atlas_app role) and DATABASE_URL (admin role).
//
// Coverage:
//
//	AC-1  list returns a tenant's imported component-definitions
//	AC-2  get returns one import's components + their vendor claims, with the
//	      SCF-anchor mapping + the unmapped flag
//	AC-3  accept moves a claim asserted -> accepted, sets disposition metadata,
//	      and appends an append-only audit row; is_vendor_claim stays TRUE and
//	      NOTHING is written to control_evaluations (the claim-is-assertion
//	      boundary)
//	AC-4  reject + needs-info map to their statuses
//	AC-5  the read + the disposition are RLS-isolated: tenant A cannot read or
//	      disposition tenant B's claims
//	(plus) an unknown id 404s; a no-role credential 403s; a non-approver 403s
package oscalcomponents_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/mgoodric/security-atlas/internal/api"
	"github.com/mgoodric/security-atlas/internal/api/testjwt"
	"github.com/mgoodric/security-atlas/internal/auth/jwt"
)

// ----- harness -----

func appDSN(t *testing.T) string {
	t.Helper()
	v := os.Getenv("DATABASE_URL_APP")
	if v == "" {
		t.Skip("DATABASE_URL_APP not set; skipping integration test")
	}
	return v
}

func adminDSN(t *testing.T) string {
	t.Helper()
	v := os.Getenv("DATABASE_URL")
	if v == "" {
		t.Skip("DATABASE_URL not set; skipping integration test")
	}
	return v
}

func openPool(t *testing.T, dsn string) *pgxpool.Pool {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	pool, err := pgxpool.New(ctx, dsn)
	if err != nil {
		t.Fatalf("pgxpool.New: %v", err)
	}
	t.Cleanup(func() { pool.Close() })
	return pool
}

// freshTenant returns a new tenant id and registers a cleanup that deletes
// every row this slice's tests can create under it.
func freshTenant(t *testing.T, admin *pgxpool.Pool) string {
	t.Helper()
	tenant := uuid.NewString()
	t.Cleanup(func() {
		ctx := context.Background()
		for _, stmt := range []string{
			`DELETE FROM imported_component_claim_dispositions WHERE tenant_id = $1`,
			`DELETE FROM imported_component_claims WHERE tenant_id = $1`,
			`DELETE FROM imported_components WHERE tenant_id = $1`,
			`DELETE FROM imported_catalogs WHERE tenant_id = $1`,
		} {
			if _, err := admin.Exec(ctx, stmt, tenant); err != nil {
				t.Logf("cleanup %s: %v", stmt, err)
			}
		}
	})
	return tenant
}

const sha = "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"

type seededClaim struct {
	claimID   uuid.UUID
	controlID string
	scfAnchor *string
}

// seedComponentDef inserts one imported component-definition + one component +
// two vendor claims (one mapped, one unmapped). It returns the definition id
// and the seeded claims. The rows are inserted exactly as slice 512's importer
// would (claim_status='asserted', is_vendor_claim=TRUE).
func seedComponentDef(t *testing.T, admin *pgxpool.Pool, tenant string) (uuid.UUID, []seededClaim) {
	t.Helper()
	ctx := context.Background()
	defID := uuid.New()
	if _, err := admin.Exec(ctx, `
		INSERT INTO imported_catalogs (
			id, tenant_id, source, kind, imported_by, source_sha256,
			source_label, oscal_version, catalog_title, control_count
		)
		VALUES ($1, $2, 'oscal-component-import', 'component_definition',
		        'test-importer', $3, 'Acme SaaS', '1.1.2',
		        'Acme Component Definition', 2)
	`, defID, tenant, sha); err != nil {
		t.Fatalf("seed component-def: %v", err)
	}

	compID := uuid.New()
	if _, err := admin.Exec(ctx, `
		INSERT INTO imported_components (
			id, tenant_id, imported_catalog_id, component_uuid, component_type,
			title, description
		)
		VALUES ($1, $2, $3, $4, 'service', 'Acme API', 'the api')
	`, compID, tenant, defID, uuid.NewString()); err != nil {
		t.Fatalf("seed component: %v", err)
	}

	anchor := "SCF-IAC-06"
	claims := []seededClaim{
		{claimID: uuid.New(), controlID: "ac-2", scfAnchor: &anchor},
		{claimID: uuid.New(), controlID: "ac-3", scfAnchor: nil},
	}
	for _, c := range claims {
		if _, err := admin.Exec(ctx, `
			INSERT INTO imported_component_claims (
				id, tenant_id, imported_component_id, control_id, statement,
				requirement_uuid, scf_anchor_id
			)
			VALUES ($1, $2, $3, $4, 'vendor says it does X', $5, $6)
		`, c.claimID, tenant, compID, c.controlID, uuid.NewString(), c.scfAnchor); err != nil {
			t.Fatalf("seed claim: %v", err)
		}
	}
	return defID, claims
}

type testEnv struct {
	server *httptest.Server
	bearer string
}

// ownerServer assembles the router with an owner credential (read-capable,
// not approver). Used to assert reads work + disposition is forbidden.
func ownerServer(t *testing.T, app *pgxpool.Pool, tenant string) testEnv {
	return newServer(t, app, testjwt.OwnerFor(uuid.MustParse(tenant), []string{"control_owner"}))
}

// approverServer assembles the router with an approver (grc_engineer)
// credential. Used to assert disposition works.
func approverServer(t *testing.T, app *pgxpool.Pool, tenant string) testEnv {
	return newServer(t, app, testjwt.ApproverFor(uuid.MustParse(tenant)))
}

func newServer(t *testing.T, app *pgxpool.Pool, claims jwt.AtlasClaims) testEnv {
	t.Helper()
	srv := api.New(api.Config{})
	srv.AttachDB(app)
	bearer := srv.IssueTestJWT(t, claims)
	h := srv.HTTPHandlerForTests()
	if h == nil {
		t.Fatal("HTTPHandlerForTests returned nil — DB pool not attached")
	}
	ts := httptest.NewServer(h)
	t.Cleanup(ts.Close)
	return testEnv{server: ts, bearer: bearer}
}

func do(t *testing.T, env testEnv, method, path, body string) (*http.Response, map[string]any) {
	t.Helper()
	var rdr *strings.Reader
	if body == "" {
		rdr = strings.NewReader("")
	} else {
		rdr = strings.NewReader(body)
	}
	req, err := http.NewRequest(method, env.server.URL+path, rdr)
	if err != nil {
		t.Fatalf("new request: %v", err)
	}
	req.Header.Set("Authorization", "Bearer "+env.bearer)
	if body != "" {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("%s %s: %v", method, path, err)
	}
	var decoded map[string]any
	_ = json.NewDecoder(resp.Body).Decode(&decoded)
	_ = resp.Body.Close()
	return resp, decoded
}

// ===== AC-1: list =====

func TestList_ReturnsDefinitions(t *testing.T) {
	admin := openPool(t, adminDSN(t))
	app := openPool(t, appDSN(t))
	tenant := freshTenant(t, admin)
	seedComponentDef(t, admin, tenant)
	env := ownerServer(t, app, tenant)

	resp, body := do(t, env, http.MethodGet, "/v1/oscal/component-definitions", "")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}
	defs, _ := body["component_definitions"].([]any)
	if len(defs) != 1 {
		t.Fatalf("definitions = %d, want 1", len(defs))
	}
	first, _ := defs[0].(map[string]any)
	if first["source_label"] != "Acme SaaS" {
		t.Fatalf("source_label = %v", first["source_label"])
	}
}

// ===== AC-2: get detail + unmapped flag =====

func TestGet_ReturnsClaimsWithUnmappedFlag(t *testing.T) {
	admin := openPool(t, adminDSN(t))
	app := openPool(t, appDSN(t))
	tenant := freshTenant(t, admin)
	defID, _ := seedComponentDef(t, admin, tenant)
	env := ownerServer(t, app, tenant)

	resp, body := do(t, env, http.MethodGet, "/v1/oscal/component-definitions/"+defID.String(), "")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}
	claims, _ := body["claims"].([]any)
	if len(claims) != 2 {
		t.Fatalf("claims = %d, want 2", len(claims))
	}
	var sawMapped, sawUnmapped bool
	for _, raw := range claims {
		c, _ := raw.(map[string]any)
		if c["is_vendor_claim"] != true {
			t.Fatalf("claim is_vendor_claim must be true, got %v", c["is_vendor_claim"])
		}
		if c["control_id"] == "ac-2" {
			if c["unmapped"] != false {
				t.Fatal("ac-2 (mapped) should be unmapped=false")
			}
			sawMapped = true
		}
		if c["control_id"] == "ac-3" {
			if c["unmapped"] != true {
				t.Fatal("ac-3 (nil anchor) should be unmapped=true")
			}
			sawUnmapped = true
		}
	}
	if !sawMapped || !sawUnmapped {
		t.Fatal("did not see both mapped + unmapped claims")
	}
}

// ===== AC-3: accept disposition + audit + assertion boundary =====

func TestAccept_DispositionsClaimAndAudits(t *testing.T) {
	admin := openPool(t, adminDSN(t))
	app := openPool(t, appDSN(t))
	tenant := freshTenant(t, admin)
	_, claims := seedComponentDef(t, admin, tenant)
	claimID := claims[0].claimID
	env := approverServer(t, app, tenant)

	resp, body := do(t, env, http.MethodPost,
		"/v1/oscal/component-claims/"+claimID.String()+":accept",
		`{"note":"vendor SOC2 covers this"}`)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%v", resp.StatusCode, body)
	}
	if body["claim_status"] != "accepted" {
		t.Fatalf("claim_status = %v, want accepted", body["claim_status"])
	}
	if body["is_vendor_claim"] != true {
		t.Fatalf("is_vendor_claim = %v, want true (claim stays a claim)", body["is_vendor_claim"])
	}

	ctx := context.Background()
	// The claim row now carries the disposition metadata.
	var status, note string
	var by *string
	if err := admin.QueryRow(ctx,
		`SELECT claim_status, dispositioned_by, disposition_note FROM imported_component_claims WHERE id = $1`,
		claimID).Scan(&status, &by, &note); err != nil {
		t.Fatalf("read claim: %v", err)
	}
	if status != "accepted" || by == nil || note != "vendor SOC2 covers this" {
		t.Fatalf("claim metadata wrong: status=%s by=%v note=%s", status, by, note)
	}
	// is_vendor_claim is still TRUE (the P0-512-1 schema CHECK is intact).
	var isClaim bool
	if err := admin.QueryRow(ctx,
		`SELECT is_vendor_claim FROM imported_component_claims WHERE id = $1`, claimID).Scan(&isClaim); err != nil {
		t.Fatalf("read is_vendor_claim: %v", err)
	}
	if !isClaim {
		t.Fatal("is_vendor_claim flipped — a claim must always be a claim")
	}
	// An append-only audit row records asserted -> accepted.
	var auditCount int
	var fromStatus, toStatus string
	if err := admin.QueryRow(ctx,
		`SELECT count(*), max(from_status), max(to_status) FROM imported_component_claim_dispositions WHERE claim_id = $1`,
		claimID).Scan(&auditCount, &fromStatus, &toStatus); err != nil {
		t.Fatalf("read audit: %v", err)
	}
	if auditCount != 1 || fromStatus != "asserted" || toStatus != "accepted" {
		t.Fatalf("audit wrong: count=%d from=%s to=%s", auditCount, fromStatus, toStatus)
	}
	// The claim-is-assertion boundary: NOTHING was written to
	// control_evaluations for this claim's control. Accepting credits the
	// assertion; it does not manufacture a passing evaluation.
	var evalCount int
	if err := admin.QueryRow(ctx,
		`SELECT count(*) FROM control_evaluations WHERE tenant_id = $1`, tenant).Scan(&evalCount); err != nil {
		t.Fatalf("read control_evaluations: %v", err)
	}
	if evalCount != 0 {
		t.Fatalf("control_evaluations rows = %d, want 0 — disposition must not satisfy a control", evalCount)
	}
}

// ===== AC-4: reject + needs-info =====

func TestRejectAndNeedsInfo(t *testing.T) {
	admin := openPool(t, adminDSN(t))
	app := openPool(t, appDSN(t))
	tenant := freshTenant(t, admin)
	_, claims := seedComponentDef(t, admin, tenant)
	env := approverServer(t, app, tenant)

	resp, body := do(t, env, http.MethodPost, "/v1/oscal/component-claims/"+claims[0].claimID.String()+":reject", "")
	if resp.StatusCode != http.StatusOK || body["claim_status"] != "rejected" {
		t.Fatalf("reject: status=%d body=%v", resp.StatusCode, body)
	}
	resp, body = do(t, env, http.MethodPost, "/v1/oscal/component-claims/"+claims[1].claimID.String()+":needs-info", "")
	if resp.StatusCode != http.StatusOK || body["claim_status"] != "needs_info" {
		t.Fatalf("needs-info: status=%d body=%v", resp.StatusCode, body)
	}
}

// ===== AC-5: RLS isolation =====

func TestRLS_CrossTenantIsolation(t *testing.T) {
	admin := openPool(t, adminDSN(t))
	app := openPool(t, appDSN(t))
	tenantA := freshTenant(t, admin)
	tenantB := freshTenant(t, admin)
	defA, claimsA := seedComponentDef(t, admin, tenantA)

	// Tenant B cannot READ tenant A's definition (404).
	envB := ownerServer(t, app, tenantB)
	resp, _ := do(t, envB, http.MethodGet, "/v1/oscal/component-definitions/"+defA.String(), "")
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("cross-tenant get: status = %d, want 404", resp.StatusCode)
	}
	// Tenant B's list does not include A's definition.
	resp, body := do(t, envB, http.MethodGet, "/v1/oscal/component-definitions", "")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("list status = %d", resp.StatusCode)
	}
	if defs, _ := body["component_definitions"].([]any); len(defs) != 0 {
		t.Fatalf("tenant B saw %d of tenant A's definitions, want 0", len(defs))
	}
	// Tenant B (as approver) cannot DISPOSITION tenant A's claim (404).
	envBApprv := approverServer(t, app, tenantB)
	resp, _ = do(t, envBApprv, http.MethodPost, "/v1/oscal/component-claims/"+claimsA[0].claimID.String()+":accept", "")
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("cross-tenant disposition: status = %d, want 404", resp.StatusCode)
	}
	// And tenant A's claim is untouched (still asserted).
	var status string
	if err := admin.QueryRow(context.Background(),
		`SELECT claim_status FROM imported_component_claims WHERE id = $1`, claimsA[0].claimID).Scan(&status); err != nil {
		t.Fatalf("read claim: %v", err)
	}
	if status != "asserted" {
		t.Fatalf("tenant A claim mutated cross-tenant: status = %s", status)
	}
}

// ===== authz: read role + write role gates =====

func TestAuthz_BareCredForbiddenRead(t *testing.T) {
	admin := openPool(t, adminDSN(t))
	app := openPool(t, appDSN(t))
	tenant := freshTenant(t, admin)
	// A bare push credential (no owner/approver/admin flag) cannot read.
	env := newServer(t, app, testjwt.ViewerFor(uuid.MustParse(tenant)))
	resp, _ := do(t, env, http.MethodGet, "/v1/oscal/component-definitions", "")
	if resp.StatusCode != http.StatusForbidden {
		t.Fatalf("status = %d, want 403", resp.StatusCode)
	}
}

func TestAuthz_NonApproverForbiddenDisposition(t *testing.T) {
	admin := openPool(t, adminDSN(t))
	app := openPool(t, appDSN(t))
	tenant := freshTenant(t, admin)
	_, claims := seedComponentDef(t, admin, tenant)
	// An owner can READ but cannot DISPOSITION (write needs approver/admin).
	env := ownerServer(t, app, tenant)
	resp, _ := do(t, env, http.MethodPost, "/v1/oscal/component-claims/"+claims[0].claimID.String()+":accept", "")
	if resp.StatusCode != http.StatusForbidden {
		t.Fatalf("status = %d, want 403", resp.StatusCode)
	}
}

func TestGet_UnknownID404(t *testing.T) {
	admin := openPool(t, adminDSN(t))
	app := openPool(t, appDSN(t))
	tenant := freshTenant(t, admin)
	env := ownerServer(t, app, tenant)
	resp, _ := do(t, env, http.MethodGet, "/v1/oscal/component-definitions/"+uuid.New().String(), "")
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("status = %d, want 404", resp.StatusCode)
	}
}
