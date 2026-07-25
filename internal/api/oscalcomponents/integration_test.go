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
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/mgoodric/security-atlas/internal/api"
	"github.com/mgoodric/security-atlas/internal/api/testjwt"
	"github.com/mgoodric/security-atlas/internal/auth/jwt"
	"github.com/mgoodric/security-atlas/internal/dbtest"
)

// ----- harness -----

// freshTenant returns a new tenant id and registers a cleanup that deletes
// every row this slice's tests can create under it.
// enableFeatureFlag turns a gating feature flag ON for the test tenant.
// Slice 660 wrapped the OSCAL component-definition routes in
// featureflag.Gate("oscal.export"), which DEFAULTS OFF — so without an
// explicit override every OSCAL route returns 404 for a fresh test tenant.
// We upsert the override via the admin (BYPASSRLS) pool, the same effect as
// an operator toggling the flag ON. Cleanup removes the row.
func enableFeatureFlag(t *testing.T, admin *pgxpool.Pool, tenant, key, category string) {
	t.Helper()
	ctx := context.Background()
	if _, err := admin.Exec(ctx, `
		INSERT INTO feature_flags (tenant_id, flag_key, enabled, description, category, last_changed_by, last_changed_at)
		VALUES ($1, $2, TRUE, '', $3, 'integration-test', now())
		ON CONFLICT (tenant_id, flag_key) DO UPDATE SET enabled = TRUE`,
		tenant, key, category); err != nil {
		t.Fatalf("enable feature flag %s: %v", key, err)
	}
	t.Cleanup(func() {
		_, _ = admin.Exec(context.Background(),
			`DELETE FROM feature_flags WHERE tenant_id = $1`, tenant)
		_, _ = admin.Exec(context.Background(),
			`DELETE FROM feature_flag_audit_log WHERE tenant_id = $1`, tenant)
	})
}

func freshTenant(t *testing.T, admin *pgxpool.Pool) string {
	t.Helper()
	tenant := uuid.NewString()
	// Slice 660: the OSCAL routes are gate-wrapped on oscal.export (default
	// OFF). Enable it for the test tenant so the pre-slice-660 route tests
	// reach the real handler instead of the gate's 404.
	enableFeatureFlag(t, admin, tenant, "oscal.export", "integrations")
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

// ensureCurrentSCFAnchor guarantees the bundled SCF catalog has at least one
// anchor in its current framework_version and returns a valid scf_id the
// mapping endpoint can target (the GetSCFAnchorBySCFID validation joins
// slug='scf' AND status='current'). If the test DB already carries a current
// SCF version (the normal case once the catalog is imported), it reuses a real
// anchor; otherwise it seeds a minimal SCF framework + current version + one
// anchor. The seeded scf_id is NEUTRAL ('TST-01'), not a real-looking SCF code.
func ensureCurrentSCFAnchor(t *testing.T, admin *pgxpool.Pool) string {
	t.Helper()
	ctx := context.Background()

	var scfID string
	err := admin.QueryRow(ctx, `
		SELECT a.scf_id
		FROM scf_anchors a
		JOIN framework_versions fv ON fv.id = a.framework_version_id
		JOIN frameworks f ON f.id = fv.framework_id
		WHERE f.slug = 'scf' AND fv.status = 'current' AND f.tenant_id IS NULL
		ORDER BY a.scf_id
		LIMIT 1
	`).Scan(&scfID)
	if err == nil && scfID != "" {
		return scfID
	}

	// No current SCF anchor present — seed a minimal one. Use the existing SCF
	// framework row if present (slug is globally unique among tenant-null
	// frameworks); else create it.
	var fwID string
	if qerr := admin.QueryRow(ctx,
		`SELECT id::text FROM frameworks WHERE slug = 'scf' AND tenant_id IS NULL LIMIT 1`).Scan(&fwID); qerr != nil {
		fwID = uuid.NewString()
		if _, eerr := admin.Exec(ctx,
			`INSERT INTO frameworks (id, tenant_id, name, slug, issuer)
			 VALUES ($1, NULL, 'Secure Controls Framework', 'scf', 'SCF Council')`, fwID); eerr != nil {
			t.Fatalf("seed scf framework: %v", eerr)
		}
	}
	verID := uuid.NewString()
	if _, eerr := admin.Exec(ctx,
		`INSERT INTO framework_versions (id, tenant_id, framework_id, version, status)
		 VALUES ($1, NULL, $2, 'test-2026', 'current')`, verID, fwID); eerr != nil {
		t.Fatalf("seed scf framework_version: %v", eerr)
	}
	scfID = "TST-01"
	if _, eerr := admin.Exec(ctx,
		`INSERT INTO scf_anchors (id, framework_version_id, scf_id, family, title)
		 VALUES ($1, $2, $3, 'TST', 'Test Anchor')
		 ON CONFLICT (framework_version_id, scf_id) DO NOTHING`,
		uuid.NewString(), verID, scfID); eerr != nil {
		t.Fatalf("seed scf anchor: %v", eerr)
	}
	return scfID
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
	admin := dbtest.NewMigratePool(t)
	app := dbtest.NewAppPool(t)
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

// ===== slice 659: empty-tenant regression guard =====
//
// TestList_EmptyTenantReturns200EmptyList is the slice-659 regression guard. The
// Vendor Claims page's GET /v1/oscal/component-definitions 500'd in the
// EMPTY/default tenant on an edge deploy. The reproduce (slice 659 decisions-log
// D1) showed the LIST QUERY itself is correct on a fully-migrated DB: an empty
// tenant returns 200 + {"component_definitions":[],"count":0}, never a 500. The
// edge 500 was migration-lag — the edge `imported_catalogs` table was missing
// the `kind` / `profile_title` columns added by migration 20260608000000, so the
// generated query (which sqlc expands to reference `kind` in both the SELECT
// list and the `WHERE kind = 'component_definition'` predicate) failed at parse
// time with "column \"kind\" does not exist" REGARDLESS of row count, which the
// handler maps to a generic 500.
//
// This test runs against the fully-migrated harness DB (the integration tier
// applies every migrations/sql/*.sql), so it locks in: a future query/migration
// mismatch that reintroduces the parse-time failure is caught in CI rather than
// in a deploy. It asserts the exact symptom-page wire shape: 200, an empty
// list, and count=0.
func TestList_EmptyTenantReturns200EmptyList(t *testing.T) {
	admin := dbtest.NewMigratePool(t)
	app := dbtest.NewAppPool(t)
	// A brand-new tenant with NO imported_catalogs rows — the EMPTY/default
	// tenant shape that 500'd on edge. freshTenant registers the cleanup;
	// nothing is seeded.
	tenant := freshTenant(t, admin)
	env := ownerServer(t, app, tenant)

	resp, body := do(t, env, http.MethodGet, "/v1/oscal/component-definitions", "")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("empty-tenant list status = %d, want 200 (a 500 here is the slice-659 regression)", resp.StatusCode)
	}
	defs, ok := body["component_definitions"].([]any)
	if !ok {
		t.Fatalf("component_definitions missing or not an array: body=%v", body)
	}
	if len(defs) != 0 {
		t.Fatalf("empty-tenant definitions = %d, want 0", len(defs))
	}
	// count must be the JSON number 0 (the list length), not absent/null.
	count, ok := body["count"].(float64)
	if !ok || count != 0 {
		t.Fatalf("count = %v (%T), want 0", body["count"], body["count"])
	}
}

// ===== AC-2: get detail + unmapped flag =====

func TestGet_ReturnsClaimsWithUnmappedFlag(t *testing.T) {
	admin := dbtest.NewMigratePool(t)
	app := dbtest.NewAppPool(t)
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
	admin := dbtest.NewMigratePool(t)
	app := dbtest.NewAppPool(t)
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
	admin := dbtest.NewMigratePool(t)
	app := dbtest.NewAppPool(t)
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
	admin := dbtest.NewMigratePool(t)
	app := dbtest.NewAppPool(t)
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
	admin := dbtest.NewMigratePool(t)
	app := dbtest.NewAppPool(t)
	tenant := freshTenant(t, admin)
	// A bare push credential (no owner/approver/admin flag) cannot read.
	env := newServer(t, app, testjwt.ViewerFor(uuid.MustParse(tenant)))
	resp, _ := do(t, env, http.MethodGet, "/v1/oscal/component-definitions", "")
	if resp.StatusCode != http.StatusForbidden {
		t.Fatalf("status = %d, want 403", resp.StatusCode)
	}
}

func TestAuthz_NonApproverForbiddenDisposition(t *testing.T) {
	admin := dbtest.NewMigratePool(t)
	app := dbtest.NewAppPool(t)
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
	admin := dbtest.NewMigratePool(t)
	app := dbtest.NewAppPool(t)
	tenant := freshTenant(t, admin)
	env := ownerServer(t, app, tenant)
	resp, _ := do(t, env, http.MethodGet, "/v1/oscal/component-definitions/"+uuid.New().String(), "")
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("status = %d, want 404", resp.StatusCode)
	}
}

// ===== slice 620: map an unmapped claim to an SCF anchor =====

// TestMapScfAnchor_MapsClaimAndAudits exercises the full PATCH path: an
// unmapped claim is mapped to a bundled SCF anchor; the claim's scf_anchor_id
// is set, the unmapped flag clears, an append-only mapping-audit row is
// written, and — the load-bearing boundary — NOTHING is written to
// control_evaluations.
func TestMapScfAnchor_MapsClaimAndAudits(t *testing.T) {
	admin := dbtest.NewMigratePool(t)
	app := dbtest.NewAppPool(t)
	tenant := freshTenant(t, admin)
	_, claims := seedComponentDef(t, admin, tenant)
	scfID := ensureCurrentSCFAnchor(t, admin)
	// claims[1] (ac-3) was seeded with a NULL scf_anchor_id — the unmapped one.
	unmapped := claims[1]
	if unmapped.scfAnchor != nil {
		t.Fatalf("precondition: claims[1] should be unmapped")
	}
	env := approverServer(t, app, tenant)

	resp, body := do(t, env, http.MethodPatch,
		"/v1/oscal/component-claims/"+unmapped.claimID.String()+"/scf-anchor",
		`{"scf_anchor_id":"`+scfID+`"}`)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%v", resp.StatusCode, body)
	}
	if body["scf_anchor_id"] != scfID {
		t.Fatalf("scf_anchor_id = %v, want %s", body["scf_anchor_id"], scfID)
	}
	if body["unmapped"] != false {
		t.Fatalf("unmapped = %v, want false", body["unmapped"])
	}
	if body["is_vendor_claim"] != true {
		t.Fatalf("is_vendor_claim = %v, want true (claim stays a claim)", body["is_vendor_claim"])
	}
	// claim_status is untouched (mapping is not a disposition).
	if body["claim_status"] != "asserted" {
		t.Fatalf("claim_status = %v, want asserted", body["claim_status"])
	}

	ctx := context.Background()
	// The claim row now carries the crosswalk.
	var gotAnchor *string
	var status string
	var isClaim bool
	if err := admin.QueryRow(ctx,
		`SELECT scf_anchor_id, claim_status, is_vendor_claim FROM imported_component_claims WHERE id = $1`,
		unmapped.claimID).Scan(&gotAnchor, &status, &isClaim); err != nil {
		t.Fatalf("read claim: %v", err)
	}
	if gotAnchor == nil || *gotAnchor != scfID {
		t.Fatalf("claim scf_anchor_id = %v, want %s", gotAnchor, scfID)
	}
	if status != "asserted" {
		t.Fatalf("claim_status mutated: %s — mapping must not disposition", status)
	}
	if !isClaim {
		t.Fatal("is_vendor_claim flipped — a claim must always be a claim")
	}

	// An append-only mapping-audit row records the from(NULL) -> to(scfID)
	// transition with event_kind='scf_mapping'.
	var auditCount int
	var eventKind string
	var fromAnchor, toAnchor *string
	if err := admin.QueryRow(ctx,
		`SELECT count(*), max(event_kind), max(from_scf_anchor_id), max(to_scf_anchor_id)
		 FROM imported_component_claim_dispositions
		 WHERE claim_id = $1 AND event_kind = 'scf_mapping'`,
		unmapped.claimID).Scan(&auditCount, &eventKind, &fromAnchor, &toAnchor); err != nil {
		t.Fatalf("read mapping audit: %v", err)
	}
	if auditCount != 1 || eventKind != "scf_mapping" {
		t.Fatalf("mapping audit wrong: count=%d kind=%s", auditCount, eventKind)
	}
	if fromAnchor != nil {
		t.Fatalf("from_scf_anchor_id = %v, want NULL (claim was unmapped)", *fromAnchor)
	}
	if toAnchor == nil || *toAnchor != scfID {
		t.Fatalf("to_scf_anchor_id = %v, want %s", toAnchor, scfID)
	}

	// THE LOAD-BEARING BOUNDARY: mapping wrote NOTHING to control_evaluations.
	// Setting a crosswalk does not manufacture control coverage.
	var evalCount int
	if err := admin.QueryRow(ctx,
		`SELECT count(*) FROM control_evaluations WHERE tenant_id = $1`, tenant).Scan(&evalCount); err != nil {
		t.Fatalf("read control_evaluations: %v", err)
	}
	if evalCount != 0 {
		t.Fatalf("control_evaluations rows = %d, want 0 — mapping must not satisfy a control", evalCount)
	}
}

// TestMapScfAnchor_UnknownAnchor422 asserts a well-formed request that names an
// scf_id with no bundled anchor is rejected 422 — a mapping must target a real
// SCF anchor (invariant #7), never a free-form string.
func TestMapScfAnchor_UnknownAnchor422(t *testing.T) {
	admin := dbtest.NewMigratePool(t)
	app := dbtest.NewAppPool(t)
	tenant := freshTenant(t, admin)
	_, claims := seedComponentDef(t, admin, tenant)
	env := approverServer(t, app, tenant)

	resp, _ := do(t, env, http.MethodPatch,
		"/v1/oscal/component-claims/"+claims[1].claimID.String()+"/scf-anchor",
		`{"scf_anchor_id":"NO-SUCH-ANCHOR-99"}`)
	if resp.StatusCode != http.StatusUnprocessableEntity {
		t.Fatalf("status = %d, want 422", resp.StatusCode)
	}
	// The claim is untouched (still unmapped).
	var anchor *string
	if err := admin.QueryRow(context.Background(),
		`SELECT scf_anchor_id FROM imported_component_claims WHERE id = $1`, claims[1].claimID).Scan(&anchor); err != nil {
		t.Fatalf("read claim: %v", err)
	}
	if anchor != nil {
		t.Fatalf("claim was mapped to a non-existent anchor: %v", *anchor)
	}
}

// TestMapScfAnchor_UnknownClaim404 asserts an unknown claim id 404s.
func TestMapScfAnchor_UnknownClaim404(t *testing.T) {
	admin := dbtest.NewMigratePool(t)
	app := dbtest.NewAppPool(t)
	tenant := freshTenant(t, admin)
	scfID := ensureCurrentSCFAnchor(t, admin)
	env := approverServer(t, app, tenant)
	resp, _ := do(t, env, http.MethodPatch,
		"/v1/oscal/component-claims/"+uuid.New().String()+"/scf-anchor",
		`{"scf_anchor_id":"`+scfID+`"}`)
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("status = %d, want 404", resp.StatusCode)
	}
}

// TestMapScfAnchor_NonApproverForbidden asserts an owner (read role) cannot map.
func TestMapScfAnchor_NonApproverForbidden(t *testing.T) {
	admin := dbtest.NewMigratePool(t)
	app := dbtest.NewAppPool(t)
	tenant := freshTenant(t, admin)
	_, claims := seedComponentDef(t, admin, tenant)
	scfID := ensureCurrentSCFAnchor(t, admin)
	env := ownerServer(t, app, tenant)
	resp, _ := do(t, env, http.MethodPatch,
		"/v1/oscal/component-claims/"+claims[1].claimID.String()+"/scf-anchor",
		`{"scf_anchor_id":"`+scfID+`"}`)
	if resp.StatusCode != http.StatusForbidden {
		t.Fatalf("status = %d, want 403", resp.StatusCode)
	}
}

// TestMapScfAnchor_RLSCrossTenantDenied asserts tenant B (approver) cannot map
// tenant A's claim — the cross-tenant claim id resolves to 404 under RLS, and
// tenant A's claim stays unmapped.
func TestMapScfAnchor_RLSCrossTenantDenied(t *testing.T) {
	admin := dbtest.NewMigratePool(t)
	app := dbtest.NewAppPool(t)
	tenantA := freshTenant(t, admin)
	tenantB := freshTenant(t, admin)
	_, claimsA := seedComponentDef(t, admin, tenantA)
	scfID := ensureCurrentSCFAnchor(t, admin)

	envB := approverServer(t, app, tenantB)
	resp, _ := do(t, envB, http.MethodPatch,
		"/v1/oscal/component-claims/"+claimsA[1].claimID.String()+"/scf-anchor",
		`{"scf_anchor_id":"`+scfID+`"}`)
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("cross-tenant map: status = %d, want 404", resp.StatusCode)
	}
	// Tenant A's claim is untouched (still unmapped).
	var anchor *string
	if err := admin.QueryRow(context.Background(),
		`SELECT scf_anchor_id FROM imported_component_claims WHERE id = $1`, claimsA[1].claimID).Scan(&anchor); err != nil {
		t.Fatalf("read claim: %v", err)
	}
	if anchor != nil {
		t.Fatalf("tenant A claim mapped cross-tenant: %v", *anchor)
	}
}
