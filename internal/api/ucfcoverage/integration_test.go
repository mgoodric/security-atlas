//go:build integration

package ucfcoverage_test

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/mgoodric/security-atlas/internal/api"
	"github.com/mgoodric/security-atlas/internal/api/scfimport"
	"github.com/mgoodric/security-atlas/internal/api/soc2import"
)

const (
	tenantA = "11111111-1111-1111-1111-111111111111"
	tenantB = "22222222-2222-2222-2222-222222222222"
)

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

// openPool opens a pgx pool against dsn with a 10s timeout. Tests call
// Close themselves via t.Cleanup to keep teardown deterministic.
func openPool(t *testing.T, dsn string) *pgxpool.Pool {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	pool, err := pgxpool.New(ctx, dsn)
	if err != nil {
		t.Fatalf("pgxpool.New: %v", err)
	}
	return pool
}

// ensureCatalog seeds the global catalog tables (SCF anchors + SOC 2
// crosswalk) idempotently via the admin pool. Re-running is a no-op.
func ensureCatalog(t *testing.T) {
	t.Helper()
	pool := openPool(t, adminDSN(t))
	defer pool.Close()

	var anchorCount int
	if err := pool.QueryRow(context.Background(), `SELECT count(*) FROM scf_anchors`).Scan(&anchorCount); err != nil {
		t.Fatalf("count scf_anchors: %v", err)
	}
	if anchorCount == 0 {
		cat, err := scfimport.Load(filepath.Join("..", "..", "..", "migrations", "fixtures", "scf-sample.json"))
		if err != nil {
			t.Fatalf("scfimport.Load: %v", err)
		}
		if _, err := scfimport.Import(context.Background(), pool, cat); err != nil {
			t.Fatalf("scfimport.Import: %v", err)
		}
	}

	var edgeCount int
	if err := pool.QueryRow(context.Background(), `SELECT count(*) FROM fw_to_scf_edges`).Scan(&edgeCount); err != nil {
		t.Fatalf("count fw_to_scf_edges: %v", err)
	}
	if edgeCount == 0 {
		cw, err := soc2import.Load(filepath.Join("..", "..", "..", "data", "crosswalks", "soc2-tsc-2017.yaml"))
		if err != nil {
			t.Fatalf("soc2import.Load: %v", err)
		}
		if _, err := soc2import.Import(context.Background(), pool, cw); err != nil {
			t.Fatalf("soc2import.Import: %v", err)
		}
	}
}

// wipeTenantControls clears every controls row from prior test runs.
// Uses the admin pool to bypass RLS. We don't restrict by tenant
// because the cross-tenant tests need both tenants' controls cleared.
func wipeTenantControls(t *testing.T) {
	t.Helper()
	pool := openPool(t, adminDSN(t))
	defer pool.Close()
	// CASCADE order: any FK from evidence_records (slice 002+) into
	// controls must already be cleared; the test suite never creates
	// those. If a future slice writes evidence here, this helper will
	// need to clear that table first.
	if _, err := pool.Exec(context.Background(), `DELETE FROM controls`); err != nil {
		t.Fatalf("wipe controls: %v", err)
	}
}

// seedControl inserts one row into `controls` for the given tenant +
// SCF anchor. Returns the new control id. Uses the admin pool so RLS
// does not block the test setup.
func seedControl(t *testing.T, tenantID string, scfAnchorScfID, bundleID, title string) uuid.UUID {
	t.Helper()
	pool := openPool(t, adminDSN(t))
	defer pool.Close()

	var anchorID uuid.UUID
	if err := pool.QueryRow(context.Background(),
		`SELECT id FROM scf_anchors WHERE scf_id = $1`, scfAnchorScfID,
	).Scan(&anchorID); err != nil {
		t.Fatalf("lookup anchor %s: %v", scfAnchorScfID, err)
	}

	id := uuid.New()
	if _, err := pool.Exec(context.Background(), `
        INSERT INTO controls (
            id, tenant_id, bundle_id, version,
            scf_id, scf_anchor_id, title, description,
            control_family, implementation_type, owner_role,
            lifecycle_state, applicability_expr,
            evidence_queries, manual_evidence_schema, linked_policy_ids,
            freshness_class, bundle_manifest_yaml, bundle_manifest_hash,
            bundle_uploaded_at, bundle_uploaded_by
        ) VALUES (
            $1, $2, $3, 1,
            $4, $5, $6, '',
            'access_control', 'automated', 'security',
            'active', '',
            '[]'::jsonb, NULL, ARRAY[]::TEXT[],
            NULL, '', '',
            now(), 'test'
        )`,
		id, tenantID, bundleID,
		scfAnchorScfID, anchorID, title,
	); err != nil {
		t.Fatalf("seed control: %v", err)
	}
	return id
}

// setupHTTPServer wires the platform Server with an app-pool DB and
// issues a bearer credential for tenantID. ensureCatalog must have been
// called prior. The returned httptest.Server has slice-008's routes
// (and every prior slice's) registered.
func setupHTTPServer(t *testing.T, tenantID string) (*httptest.Server, string) {
	t.Helper()
	appPool := openPool(t, appDSN(t))
	srv := api.New(api.Config{RotationGrace: time.Hour})
	srv.AttachDB(appPool)
	_, bearer, err := srv.IssueBootstrapCredential(tenantID)
	if err != nil {
		t.Fatalf("IssueBootstrapCredential: %v", err)
	}
	handler := srv.HTTPHandlerForTests()
	if handler == nil {
		t.Fatal("HTTPHandlerForTests returned nil; AttachDB did not take effect")
	}
	ts := httptest.NewServer(handler)
	t.Cleanup(func() {
		ts.Close()
		appPool.Close()
	})
	return ts, bearer
}

func get(t *testing.T, ts *httptest.Server, path, bearer string) (*http.Response, []byte) {
	t.Helper()
	req, err := http.NewRequestWithContext(context.Background(), http.MethodGet, ts.URL+path, nil)
	if err != nil {
		t.Fatalf("NewRequest: %v", err)
	}
	if bearer != "" {
		req.Header.Set("Authorization", "Bearer "+bearer)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("Do %s: %v", path, err)
	}
	defer func() { _ = resp.Body.Close() }()
	body, _ := io.ReadAll(resp.Body)
	return resp, body
}

// AC-1: forward traversal — requirement → anchors → controls. Given
// SOC2:2017:CC6.6 (which slice-007's crosswalk maps to NET-04 +
// IAC-06), the handler must return:
//   - requirement.{id, code, title}
//   - anchors[] each with scf_id, relationship_type, strength
//   - controls[] containing the tenant's MFA control anchored on IAC-06
func TestRequirementCoverage_ReturnsAnchorsAndControls(t *testing.T) {
	ensureCatalog(t)
	wipeTenantControls(t)
	_ = seedControl(t, tenantA, "IAC-06", "test-mfa-enforcement", "MFA Enforcement")

	ts, bearer := setupHTTPServer(t, tenantA)
	resp, body := get(t, ts, "/v1/requirements/soc2:2017:CC6.6/coverage", bearer)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d; want 200; body=%s", resp.StatusCode, body)
	}

	var got struct {
		Requirement struct {
			ID    string `json:"id"`
			Code  string `json:"code"`
			Title string `json:"title"`
		} `json:"requirement"`
		Anchors []struct {
			SCFID            string  `json:"scf_id"`
			RelationshipType string  `json:"relationship_type"`
			Strength         float64 `json:"strength"`
		} `json:"anchors"`
		Controls []struct {
			ID       string `json:"id"`
			BundleID string `json:"bundle_id"`
			Title    string `json:"title"`
			SCFID    string `json:"scf_id"`
		} `json:"controls"`
	}
	if err := json.Unmarshal(body, &got); err != nil {
		t.Fatalf("unmarshal: %v\nbody=%s", err, body)
	}
	if got.Requirement.Code != "CC6.6" {
		t.Fatalf("requirement.code = %q; want CC6.6", got.Requirement.Code)
	}
	if len(got.Anchors) < 2 {
		t.Fatalf("expected >=2 anchors for CC6.6 (NET-04 + IAC-06), got %d", len(got.Anchors))
	}
	sawIAC06 := false
	for _, a := range got.Anchors {
		if a.SCFID == "IAC-06" {
			sawIAC06 = true
		}
		if a.RelationshipType == "" {
			t.Fatalf("anchor missing relationship_type: %+v", a)
		}
		if a.Strength <= 0.0 || a.Strength > 1.0 {
			t.Fatalf("anchor strength %v out of (0,1]", a.Strength)
		}
	}
	if !sawIAC06 {
		t.Fatalf("expected IAC-06 in anchors, got %+v", got.Anchors)
	}

	// Tenant A's seeded MFA control should appear; bundle_id is the
	// natural key we can verify against.
	sawSeeded := false
	for _, c := range got.Controls {
		if c.BundleID == "test-mfa-enforcement" {
			sawSeeded = true
			if c.SCFID != "IAC-06" {
				t.Fatalf("control.scf_id = %q; want IAC-06", c.SCFID)
			}
			if c.Title != "MFA Enforcement" {
				t.Fatalf("control.title = %q; want MFA Enforcement", c.Title)
			}
		}
	}
	if !sawSeeded {
		t.Fatalf("expected tenant-A control test-mfa-enforcement in controls, got %+v", got.Controls)
	}
}

// AC-2: reverse traversal by SCF id. /v1/anchors/IAC-06/requirements
// returns every framework requirement that maps to IAC-06 with strength
// and relationship_type. Slice-007's CC6.6 is the canonical example.
func TestAnchorRequirements_BySCFID(t *testing.T) {
	ensureCatalog(t)
	wipeTenantControls(t)
	ts, bearer := setupHTTPServer(t, tenantA)

	resp, body := get(t, ts, "/v1/anchors/IAC-06/requirements", bearer)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d; want 200; body=%s", resp.StatusCode, body)
	}
	var got struct {
		Anchor struct {
			SCFID string `json:"scf_id"`
		} `json:"anchor"`
		Requirements []struct {
			Code              string  `json:"code"`
			RelationshipType  string  `json:"relationship_type"`
			Strength          float64 `json:"strength"`
			FrameworkSlug     string  `json:"framework_slug"`
			FrameworkVersion  string  `json:"framework_version"`
			SourceAttribution string  `json:"source_attribution"`
		} `json:"requirements"`
	}
	if err := json.Unmarshal(body, &got); err != nil {
		t.Fatalf("unmarshal: %v\nbody=%s", err, body)
	}
	if got.Anchor.SCFID != "IAC-06" {
		t.Fatalf("anchor.scf_id = %q; want IAC-06", got.Anchor.SCFID)
	}
	sawCC66 := false
	for _, r := range got.Requirements {
		if r.Code == "CC6.6" && r.FrameworkSlug == "soc2" && r.FrameworkVersion == "2017" {
			sawCC66 = true
			if r.SourceAttribution != "community_draft" {
				t.Fatalf("CC6.6 source_attribution = %q; want community_draft", r.SourceAttribution)
			}
			if r.RelationshipType == "" {
				t.Fatal("CC6.6 missing relationship_type")
			}
			if r.Strength <= 0.0 || r.Strength > 1.0 {
				t.Fatalf("CC6.6 strength %v out of (0,1]", r.Strength)
			}
		}
	}
	if !sawCC66 {
		t.Fatalf("expected SOC2:2017:CC6.6 in IAC-06 reverse traversal, got %+v", got.Requirements)
	}
}

// AC-2: reverse traversal with framework_version pin. Filtering to
// soc2:2017 must still include CC6.6; filtering to a non-existent
// version must yield an empty list (not 404 — the anchor exists).
func TestAnchorRequirements_FrameworkVersionPin(t *testing.T) {
	ensureCatalog(t)
	wipeTenantControls(t)
	ts, bearer := setupHTTPServer(t, tenantA)

	// Pin to soc2:2017 — should still see CC6.6.
	resp, body := get(t, ts, "/v1/anchors/IAC-06/requirements?framework_version=soc2:2017", bearer)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d; want 200; body=%s", resp.StatusCode, body)
	}
	var got struct {
		Requirements []struct {
			Code             string `json:"code"`
			FrameworkVersion string `json:"framework_version"`
		} `json:"requirements"`
	}
	if err := json.Unmarshal(body, &got); err != nil {
		t.Fatalf("unmarshal: %v\nbody=%s", err, body)
	}
	if len(got.Requirements) == 0 {
		t.Fatal("expected at least one requirement under soc2:2017 pin")
	}
	for _, r := range got.Requirements {
		if r.FrameworkVersion != "2017" {
			t.Fatalf("expected only 2017 rows, saw %q", r.FrameworkVersion)
		}
	}

	// Pin to a non-existent version → empty list, 200.
	resp2, body2 := get(t, ts, "/v1/anchors/IAC-06/requirements?framework_version=soc2:9999", bearer)
	if resp2.StatusCode != http.StatusOK {
		t.Fatalf("nonexistent pin status = %d; want 200; body=%s", resp2.StatusCode, body2)
	}
	var got2 struct {
		Requirements []map[string]any `json:"requirements"`
	}
	if err := json.Unmarshal(body2, &got2); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if len(got2.Requirements) != 0 {
		t.Fatalf("expected empty list under bogus pin, got %d rows", len(got2.Requirements))
	}
}

// AC-2: 404 for unknown anchor.
func TestAnchorRequirements_404OnUnknownAnchor(t *testing.T) {
	ensureCatalog(t)
	ts, bearer := setupHTTPServer(t, tenantA)
	resp, _ := get(t, ts, "/v1/anchors/ZZZ-99/requirements", bearer)
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("status = %d; want 404", resp.StatusCode)
	}
}

// AC-3: GET /v1/controls/{id}/coverage returns control + anchor +
// satisfied requirements. The seeded MFA-Enforcement control on
// IAC-06 should report at least SOC 2 CC6.6.
func TestControlCoverage_ReturnsAnchorAndRequirements(t *testing.T) {
	ensureCatalog(t)
	wipeTenantControls(t)
	cid := seedControl(t, tenantA, "IAC-06", "test-mfa-enforcement", "MFA Enforcement")
	ts, bearer := setupHTTPServer(t, tenantA)

	resp, body := get(t, ts, "/v1/controls/"+cid.String()+"/coverage", bearer)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d; want 200; body=%s", resp.StatusCode, body)
	}
	var got struct {
		Control struct {
			ID       string `json:"id"`
			BundleID string `json:"bundle_id"`
		} `json:"control"`
		Anchor *struct {
			SCFID string `json:"scf_id"`
		} `json:"anchor"`
		Requirements []struct {
			Code             string `json:"code"`
			FrameworkSlug    string `json:"framework_slug"`
			FrameworkVersion string `json:"framework_version"`
		} `json:"requirements"`
	}
	if err := json.Unmarshal(body, &got); err != nil {
		t.Fatalf("unmarshal: %v\nbody=%s", err, body)
	}
	if got.Control.ID != cid.String() {
		t.Fatalf("control.id = %q; want %s", got.Control.ID, cid)
	}
	if got.Control.BundleID != "test-mfa-enforcement" {
		t.Fatalf("control.bundle_id = %q", got.Control.BundleID)
	}
	if got.Anchor == nil || got.Anchor.SCFID != "IAC-06" {
		t.Fatalf("anchor.scf_id = %+v; want IAC-06", got.Anchor)
	}
	sawCC66 := false
	for _, r := range got.Requirements {
		if r.Code == "CC6.6" && r.FrameworkSlug == "soc2" {
			sawCC66 = true
		}
	}
	if !sawCC66 {
		t.Fatalf("expected CC6.6 in requirements for MFA control, got %+v", got.Requirements)
	}
}

// AC-3: 404 for unknown control id.
func TestControlCoverage_404OnUnknownControl(t *testing.T) {
	ensureCatalog(t)
	wipeTenantControls(t)
	ts, bearer := setupHTTPServer(t, tenantA)
	bogus := uuid.New().String()
	resp, _ := get(t, ts, "/v1/controls/"+bogus+"/coverage", bearer)
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("status = %d; want 404", resp.StatusCode)
	}
}

// AC-3: 400 for non-UUID control id.
func TestControlCoverage_400OnNonUUID(t *testing.T) {
	ensureCatalog(t)
	ts, bearer := setupHTTPServer(t, tenantA)
	resp, _ := get(t, ts, "/v1/controls/not-a-uuid/coverage", bearer)
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("status = %d; want 400", resp.StatusCode)
	}
}

// AC-3: framework_version pin filters the requirements list. Pinning
// to soc2:2017 keeps CC6.6; pinning to bogus version returns empty.
func TestControlCoverage_FrameworkVersionPin(t *testing.T) {
	ensureCatalog(t)
	wipeTenantControls(t)
	cid := seedControl(t, tenantA, "IAC-06", "test-mfa-pin", "MFA Enforcement (pin)")
	ts, bearer := setupHTTPServer(t, tenantA)

	resp, body := get(t, ts, "/v1/controls/"+cid.String()+"/coverage?framework_version=soc2:2017", bearer)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d; want 200; body=%s", resp.StatusCode, body)
	}
	var got struct {
		Requirements []struct {
			Code             string `json:"code"`
			FrameworkVersion string `json:"framework_version"`
		} `json:"requirements"`
	}
	if err := json.Unmarshal(body, &got); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if len(got.Requirements) == 0 {
		t.Fatal("expected non-empty pinned list")
	}
	for _, r := range got.Requirements {
		if r.FrameworkVersion != "2017" {
			t.Fatalf("expected only 2017 rows under pin, saw %q", r.FrameworkVersion)
		}
	}

	resp2, body2 := get(t, ts, "/v1/controls/"+cid.String()+"/coverage?framework_version=soc2:9999", bearer)
	if resp2.StatusCode != http.StatusOK {
		t.Fatalf("bogus pin status = %d; body=%s", resp2.StatusCode, body2)
	}
	var got2 struct {
		Requirements []map[string]any `json:"requirements"`
	}
	if err := json.Unmarshal(body2, &got2); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if len(got2.Requirements) != 0 {
		t.Fatalf("expected empty list under bogus pin, got %d rows", len(got2.Requirements))
	}
}

// AC-3: control without anchor returns 200 + null anchor + empty
// requirements. The control still exists; it just isn't anchored to
// the canonical graph yet.
func TestControlCoverage_NullAnchorWhenUnanchored(t *testing.T) {
	ensureCatalog(t)
	wipeTenantControls(t)
	pool := openPool(t, adminDSN(t))
	defer pool.Close()
	id := uuid.New()
	if _, err := pool.Exec(context.Background(), `
        INSERT INTO controls (
            id, tenant_id, bundle_id, version,
            scf_id, scf_anchor_id, title, description,
            control_family, implementation_type, owner_role,
            lifecycle_state, applicability_expr,
            evidence_queries, manual_evidence_schema, linked_policy_ids,
            freshness_class, bundle_manifest_yaml, bundle_manifest_hash,
            bundle_uploaded_at, bundle_uploaded_by
        ) VALUES (
            $1, $2, 'unanchored-control', 1,
            NULL, NULL, 'Unanchored', '',
            'access_control', 'manual_attested', 'security',
            'active', '',
            '[]'::jsonb, NULL, ARRAY[]::TEXT[],
            NULL, '', '',
            now(), 'test'
        )`, id, tenantA); err != nil {
		t.Fatalf("seed unanchored control: %v", err)
	}

	ts, bearer := setupHTTPServer(t, tenantA)
	resp, body := get(t, ts, "/v1/controls/"+id.String()+"/coverage", bearer)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d; want 200; body=%s", resp.StatusCode, body)
	}
	var got struct {
		Anchor       any              `json:"anchor"`
		Requirements []map[string]any `json:"requirements"`
	}
	if err := json.Unmarshal(body, &got); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if got.Anchor != nil {
		t.Fatalf("anchor = %+v; want null for unanchored control", got.Anchor)
	}
	if len(got.Requirements) != 0 {
		t.Fatalf("requirements = %d; want 0 for unanchored control", len(got.Requirements))
	}
}

// AC-6 (invariant 6 / RLS): tenant B traversing tenant A's requirement
// must see the global catalog row (requirement + anchors are global —
// canvas §3.5) AND an EMPTY controls list — tenant A's controls are
// invisible to tenant B at the database layer, not the application.
// The handler does NOT add `WHERE tenant_id = ?` — RLS does it.
func TestRequirementCoverage_RLSHidesForeignTenantControls(t *testing.T) {
	ensureCatalog(t)
	wipeTenantControls(t)
	// Tenant A seeds an MFA control on IAC-06.
	_ = seedControl(t, tenantA, "IAC-06", "tenantA-mfa", "Tenant A MFA")
	// Tenant B has no controls anchored on IAC-06.

	// Tenant B queries the same requirement.
	ts, bearer := setupHTTPServer(t, tenantB)
	resp, body := get(t, ts, "/v1/requirements/soc2:2017:CC6.6/coverage", bearer)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d; want 200; body=%s", resp.StatusCode, body)
	}
	var got struct {
		Requirement struct {
			Code string `json:"code"`
		} `json:"requirement"`
		Anchors []struct {
			SCFID string `json:"scf_id"`
		} `json:"anchors"`
		Controls []struct {
			BundleID string `json:"bundle_id"`
		} `json:"controls"`
	}
	if err := json.Unmarshal(body, &got); err != nil {
		t.Fatalf("unmarshal: %v\nbody=%s", err, body)
	}
	// Requirement is global — tenant B sees it.
	if got.Requirement.Code != "CC6.6" {
		t.Fatalf("tenant B should see global requirement CC6.6; got %q", got.Requirement.Code)
	}
	// Anchors are global — tenant B sees them.
	if len(got.Anchors) == 0 {
		t.Fatal("tenant B should see global anchors for CC6.6")
	}
	// Controls are tenant-scoped — tenant B sees NONE of tenant A's.
	for _, c := range got.Controls {
		if c.BundleID == "tenantA-mfa" {
			t.Fatalf("RLS VIOLATION: tenant B saw tenant A's control %q", c.BundleID)
		}
	}
	if len(got.Controls) != 0 {
		t.Fatalf("tenant B should see empty controls list, got %d rows: %+v", len(got.Controls), got.Controls)
	}
}

// AC-6: tenant B cannot resolve tenant A's control id at all — RLS
// makes the foreign row invisible, so the lookup returns 404 (not 403,
// not an empty body). No information about the existence of the
// foreign row leaks.
func TestControlCoverage_RLSHidesForeignControl(t *testing.T) {
	ensureCatalog(t)
	wipeTenantControls(t)
	cid := seedControl(t, tenantA, "IAC-06", "tenantA-only", "Tenant A only")

	ts, bearer := setupHTTPServer(t, tenantB)
	resp, body := get(t, ts, "/v1/controls/"+cid.String()+"/coverage", bearer)
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("tenant B looking up tenant A's control: status = %d; want 404; body=%s", resp.StatusCode, body)
	}
}

// Anti-criterion guard: the slice-007 reverse-traversal route
// (/v1/requirements/{id}/anchors) still works after slice 008 wires
// /v1/requirements/{id}/coverage on the same prefix. This catches
// accidental chi route shadowing.
func TestSlice007RouteStillWorks(t *testing.T) {
	ensureCatalog(t)
	ts, bearer := setupHTTPServer(t, tenantA)
	resp, body := get(t, ts, "/v1/requirements/soc2:2017:CC6.6/anchors", bearer)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("slice-007 route status = %d; want 200; body=%s", resp.StatusCode, body)
	}
}

// Missing bearer rejection (covers all three routes).
func TestUCFCoverage_RejectsMissingBearer(t *testing.T) {
	ensureCatalog(t)
	ts, _ := setupHTTPServer(t, tenantA)
	for _, p := range []string{
		"/v1/requirements/soc2:2017:CC6.6/coverage",
		"/v1/anchors/IAC-06/requirements",
		"/v1/controls/00000000-0000-0000-0000-000000000000/coverage",
	} {
		resp, _ := get(t, ts, p, "")
		if resp.StatusCode != http.StatusUnauthorized {
			t.Fatalf("%s: status = %d; want 401", p, resp.StatusCode)
		}
	}
}

// Anti-criterion guard: confirm at DDL level that no fw_to_fw_edges
// table exists. Slice 007 already had this; slice 008 doubles down
// because the slice's whole raison d'être is to NOT create such a
// table — if a future refactor adds one this test screams.
func TestNoFrameworkToFrameworkEdgeTable(t *testing.T) {
	ensureCatalog(t)
	pool := openPool(t, adminDSN(t))
	defer pool.Close()
	var n int
	if err := pool.QueryRow(context.Background(), `
        SELECT count(*) FROM information_schema.tables
        WHERE table_schema = 'public'
          AND table_name IN (
              'fw_to_fw_edges',
              'framework_requirement_edges',
              'requirement_to_requirement_edges',
              'cross_framework_edges'
          )`).Scan(&n); err != nil {
		t.Fatalf("query information_schema: %v", err)
	}
	if n != 0 {
		t.Fatalf("constitutional invariant 1 violation: a fw->fw table exists (%d hits)", n)
	}
}
