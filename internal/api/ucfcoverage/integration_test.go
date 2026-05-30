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
	"github.com/mgoodric/security-atlas/internal/api/testjwt"
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
	// FK order: clear the children that FK-reference controls BEFORE
	// deleting controls, else the global DELETE hits
	// evidence_records_tenant_id_control_id_fkey (and the
	// control_evaluations FK). This package does not itself write
	// evidence_records, but under CI's serial `-p 1` run the prior
	// package (internal/demoseed) leaves evidence_records rows under the
	// shared tenant — so the global wipe must clear them first.
	// (Slice 405: this latent FK-wipe-ordering bug was hidden because the
	// package never ran in CI alongside demoseed.)
	for _, q := range []string{
		`DELETE FROM evidence_records`,
		`DELETE FROM control_evaluations`,
		`DELETE FROM controls`,
	} {
		if _, err := pool.Exec(context.Background(), q); err != nil {
			t.Fatalf("wipe (%s): %v", q, err)
		}
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
	// Slice 197: JWT bearer via slice 190 path. ViewerFor mirrors
	// the legacy IssueBootstrapCredential default (no elevation).
	bearer := srv.IssueTestJWT(t, testjwt.ViewerFor(uuid.MustParse(tenantID)))
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

// ===== slice 256: per-row Coverage column =====
//
// AC-1 (backend): GET /v1/controls/{id}/coverage emits a `coverage`
// numeric per requirement row when in scope, `null` when out of scope
// or when the control has zero effectiveness data.
//
// AC-2 (backend): three branches covered below:
//   - in-scope row with evaluations returns numeric coverage
//   - out-of-scope row returns null (framework_scope predicate
//     intersects to empty)
//   - row with zero effectiveness data returns null (TotalCount == 0
//     must NOT degrade to coverage=0; "no data" is distinct from
//     "perfectly failing")

// seedActivatedFrameworkScope inserts an activated framework_scope row
// binding `tenant` to `frameworkVersionID` with a permissive
// `{"op":"true"}` predicate (matches every cell in the tenant's
// applicability universe — see frameworkscope.EffectiveScope). Mirrors
// the demoseed pattern at internal/demoseed/writers.go (slice 205).
func seedActivatedFrameworkScope(t *testing.T, tenant string, frameworkVersionID uuid.UUID, name string) uuid.UUID {
	t.Helper()
	pool := openPool(t, adminDSN(t))
	defer pool.Close()
	id := uuid.New()
	predicate := `{"op":"true"}`
	// predicate_hash matches the demoseed shape: hex(sha256(predicate)).
	if _, err := pool.Exec(context.Background(),
		`INSERT INTO framework_scopes
		 (id, tenant_id, framework_version_id, name, state, predicate, predicate_hash,
		  effective_from)
		 VALUES ($1, $2, $3, $4, 'activated', $5::jsonb,
		         encode(digest($5::text, 'sha256'), 'hex'),
		         now() - INTERVAL '6 months')`,
		id, tenant, frameworkVersionID, name, predicate,
	); err != nil {
		t.Fatalf("seed framework_scope: %v", err)
	}
	return id
}

// seedEvaluation inserts one row into control_evaluations. Mirrors
// internal/freshnessdrift/integration_test.go::seedEvaluation. Used
// here to give the slice-012 effectiveness rollup data to chew on so
// the per-row coverage isn't null for in-scope rows.
func seedEvaluation(t *testing.T, tenant string, controlID uuid.UUID, result string, evaluatedAt time.Time) {
	t.Helper()
	pool := openPool(t, adminDSN(t))
	defer pool.Close()
	id := uuid.New()
	runID := uuid.New()
	if _, err := pool.Exec(context.Background(), `
		INSERT INTO control_evaluations (
			id, tenant_id, control_id, scope_cell_id, eval_run_id,
			evaluated_at, result, freshness_status,
			evidence_count_in_window, trigger
		) VALUES ($1, $2, $3, NULL, $4, $5, $6, 'fresh', 1, 'manual')
	`, id, tenant, controlID, runID, evaluatedAt, result); err != nil {
		t.Fatalf("seed evaluation: %v", err)
	}
}

// seedScopeCell inserts one scope_cells row for the tenant so that a
// control with the legacy match-all applicability_expr ("") resolves to a
// non-empty applicability universe. Slice 256's in-scope coverage test
// needs at least one cell in the universe; without it the intersection in
// frameworkscope.EffectiveScope is empty and every row renders n/a. Uses
// the admin pool so RLS does not block setup.
func seedScopeCell(t *testing.T, tenant string) {
	t.Helper()
	pool := openPool(t, adminDSN(t))
	defer pool.Close()
	const dims = `{"env":"prod"}`
	if _, err := pool.Exec(context.Background(),
		`INSERT INTO scope_cells (id, tenant_id, label, dimensions, dimensions_hash)
		 VALUES ($1, $2, 'slice256-inscope', $3::jsonb,
		         encode(digest($3::text, 'sha256'), 'hex'))
		 ON CONFLICT DO NOTHING`,
		uuid.New(), tenant, dims,
	); err != nil {
		t.Fatalf("seed scope_cell: %v", err)
	}
}

// firstFrameworkVersionID returns the framework_version_id of the first
// SOC 2 row in the catalog. Slice 256's coverage tests need a known fv
// to activate a framework_scope against.
func soc2017FrameworkVersionID(t *testing.T) uuid.UUID {
	t.Helper()
	pool := openPool(t, adminDSN(t))
	defer pool.Close()
	var id uuid.UUID
	if err := pool.QueryRow(context.Background(),
		`SELECT fv.id FROM framework_versions fv
		   JOIN frameworks f ON f.id = fv.framework_id
		  WHERE f.slug = 'soc2' AND fv.version = '2017'
		  LIMIT 1`,
	).Scan(&id); err != nil {
		t.Fatalf("lookup soc2:2017 framework_version_id: %v", err)
	}
	return id
}

// wipeTenantState clears the per-tenant working set that slice 256
// tests rely on: evidence_records, evaluations, framework_scopes,
// controls, scope_cells. Catalog rows (scf_anchors / framework_versions
// / etc.) stay intact. FK order: the children that FK-reference controls
// (evidence_records, control_evaluations) are deleted BEFORE controls,
// else the global DELETE hits evidence_records_tenant_id_control_id_fkey
// under CI's serial `-p 1` run (the prior package internal/demoseed
// leaves evidence_records rows under the shared tenant). scope_cells is
// cleared last so the in-scope test's seeded cell does not leak into the
// out-of-scope / no-data tests within the same run (control_evaluations
// FK-references scope_cells ON DELETE CASCADE and is already cleared
// above). (Slice 405: latent FK-wipe-ordering bug hidden because the
// package never ran in CI alongside demoseed.)
func wipeTenantState(t *testing.T) {
	t.Helper()
	pool := openPool(t, adminDSN(t))
	defer pool.Close()
	for _, q := range []string{
		`DELETE FROM evidence_records`,
		`DELETE FROM control_evaluations`,
		`DELETE FROM framework_scopes`,
		`DELETE FROM controls`,
		`DELETE FROM scope_cells`,
	} {
		if _, err := pool.Exec(context.Background(), q); err != nil {
			t.Fatalf("wipe (%s): %v", q, err)
		}
	}
}

// AC-1 / AC-2 branch (a) — in-scope row with evaluation data returns a
// numeric `coverage` that equals strength × 30d pass_rate. We seed two
// pass evaluations and one fail in the last 30 days (pass_rate = 2/3 ≈
// 0.6667), activate SOC 2 with a permissive predicate (so every fv row
// is in scope), then verify the CC6.6 row's coverage is `strength *
// 0.6667`.
func TestControlCoverage_Slice256_InScopeRowReturnsNumeric(t *testing.T) {
	ensureCatalog(t)
	wipeTenantState(t)
	cid := seedControl(t, tenantA, "IAC-06", "test-mfa-256-inscope", "MFA Enforcement (256-inscope)")
	soc2FVID := soc2017FrameworkVersionID(t)
	seedActivatedFrameworkScope(t, tenantA, soc2FVID, "SOC 2 — Test Activated")

	// The control's applicability_expr is the legacy match-all (""), which
	// scope.ControlApplicability resolves against the tenant's scope-cell
	// universe. With zero cells the universe is empty, the intersection is
	// empty, and the row renders out-of-scope (coverage null) — the correct
	// product behaviour, but not what THIS test exercises. Seed one cell so
	// the match-all applicability has a non-empty universe and the row is
	// genuinely in scope. (Slice 405: this seed was missing, so the test
	// silently relied on a stray scope_cells row and never ran in CI.)
	seedScopeCell(t, tenantA)

	now := time.Now().UTC()
	seedEvaluation(t, tenantA, cid, "pass", now.Add(-1*time.Hour))
	seedEvaluation(t, tenantA, cid, "pass", now.Add(-2*time.Hour))
	seedEvaluation(t, tenantA, cid, "fail", now.Add(-3*time.Hour))

	ts, bearer := setupHTTPServer(t, tenantA)
	resp, body := get(t, ts, "/v1/controls/"+cid.String()+"/coverage", bearer)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d; want 200; body=%s", resp.StatusCode, body)
	}
	var got struct {
		Requirements []struct {
			Code             string   `json:"code"`
			FrameworkSlug    string   `json:"framework_slug"`
			FrameworkVersion string   `json:"framework_version"`
			Strength         float64  `json:"strength"`
			Coverage         *float64 `json:"coverage"`
		} `json:"requirements"`
	}
	if err := json.Unmarshal(body, &got); err != nil {
		t.Fatalf("unmarshal: %v\nbody=%s", err, body)
	}
	if len(got.Requirements) == 0 {
		t.Fatal("expected at least one requirement row")
	}
	sawCC66 := false
	for _, r := range got.Requirements {
		if r.Code == "CC6.6" && r.FrameworkSlug == "soc2" && r.FrameworkVersion == "2017" {
			sawCC66 = true
			if r.Coverage == nil {
				t.Fatalf("CC6.6 coverage = null; want strength*pass_rate (strength=%v)", r.Strength)
			}
			want := r.Strength * (2.0 / 3.0)
			if delta := *r.Coverage - want; delta < -1e-9 || delta > 1e-9 {
				t.Fatalf("CC6.6 coverage = %v; want %v (strength=%v × pass_rate=2/3)", *r.Coverage, want, r.Strength)
			}
		}
	}
	if !sawCC66 {
		t.Fatalf("expected CC6.6 in requirements: %+v", got.Requirements)
	}
}

// AC-2 branch (b) — out-of-scope row returns `coverage: null`. We seed
// evaluations (so effectiveness has data), but do NOT activate any
// framework_scope for SOC 2 — slice 018's `Activated` returns
// ErrNotFound, the handler treats that as out-of-scope, and every row
// must report null coverage. Distinguishes "out of scope" from
// "in-scope but failing".
func TestControlCoverage_Slice256_OutOfScopeRowReturnsNull(t *testing.T) {
	ensureCatalog(t)
	wipeTenantState(t)
	cid := seedControl(t, tenantA, "IAC-06", "test-mfa-256-oos", "MFA Enforcement (256-oos)")

	// Plenty of effectiveness data so the null can't be confused with
	// "no data yet".
	now := time.Now().UTC()
	seedEvaluation(t, tenantA, cid, "pass", now.Add(-1*time.Hour))
	seedEvaluation(t, tenantA, cid, "pass", now.Add(-2*time.Hour))

	ts, bearer := setupHTTPServer(t, tenantA)
	resp, body := get(t, ts, "/v1/controls/"+cid.String()+"/coverage", bearer)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d; want 200; body=%s", resp.StatusCode, body)
	}
	var got struct {
		Requirements []struct {
			Code     string   `json:"code"`
			Coverage *float64 `json:"coverage"`
		} `json:"requirements"`
	}
	if err := json.Unmarshal(body, &got); err != nil {
		t.Fatalf("unmarshal: %v\nbody=%s", err, body)
	}
	if len(got.Requirements) == 0 {
		t.Fatal("expected at least one requirement row")
	}
	for _, r := range got.Requirements {
		if r.Coverage != nil {
			t.Fatalf("requirement %q coverage = %v; want null (no activated framework_scope = out of scope)",
				r.Code, *r.Coverage)
		}
	}
}

// AC-2 branch (c) — in-scope row with ZERO effectiveness data returns
// `coverage: null`, NOT 0. This is the anti-criterion P0 contract: a
// 0 would imply "perfectly failing" (every evaluation returned fail);
// null is "we don't have enough data to weigh this yet." The DB has an
// activated framework_scope (so the row is in scope) but no
// control_evaluations rows in the 30-day window.
func TestControlCoverage_Slice256_NoEffectivenessDataReturnsNull(t *testing.T) {
	ensureCatalog(t)
	wipeTenantState(t)
	cid := seedControl(t, tenantA, "IAC-06", "test-mfa-256-nodata", "MFA Enforcement (256-nodata)")
	soc2FVID := soc2017FrameworkVersionID(t)
	seedActivatedFrameworkScope(t, tenantA, soc2FVID, "SOC 2 — Test Activated (nodata)")
	// Intentionally seed NO control_evaluations rows.

	ts, bearer := setupHTTPServer(t, tenantA)
	resp, body := get(t, ts, "/v1/controls/"+cid.String()+"/coverage", bearer)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d; want 200; body=%s", resp.StatusCode, body)
	}
	var got struct {
		Requirements []struct {
			Code             string   `json:"code"`
			FrameworkSlug    string   `json:"framework_slug"`
			FrameworkVersion string   `json:"framework_version"`
			Coverage         *float64 `json:"coverage"`
		} `json:"requirements"`
	}
	if err := json.Unmarshal(body, &got); err != nil {
		t.Fatalf("unmarshal: %v\nbody=%s", err, body)
	}
	if len(got.Requirements) == 0 {
		t.Fatal("expected at least one requirement row")
	}
	// Even SOC 2 — which is in-scope — must report null because the
	// control has no evaluations to weight against. Anti-criterion: a
	// zero would be a lie about the operational record.
	sawSOC2 := false
	for _, r := range got.Requirements {
		if r.FrameworkSlug == "soc2" && r.FrameworkVersion == "2017" {
			sawSOC2 = true
			if r.Coverage != nil {
				t.Fatalf("SOC2 row coverage = %v; want null (no effectiveness data must NOT degrade to 0)", *r.Coverage)
			}
		}
	}
	if !sawSOC2 {
		t.Fatalf("expected a SOC 2 row in requirements: %+v", got.Requirements)
	}
}

// AC-1 wire-shape guard: the `coverage` field is ALWAYS emitted on the
// /v1/controls/{id}/coverage response — never omitted. JSON nulls
// must render as the explicit key `"coverage": null` so frontend
// destructuring is stable (a missing key vs an explicit null is a real
// shape difference in TS strict mode).
func TestControlCoverage_Slice256_CoverageKeyAlwaysEmitted(t *testing.T) {
	ensureCatalog(t)
	wipeTenantState(t)
	cid := seedControl(t, tenantA, "IAC-06", "test-mfa-256-keypresent", "MFA Enforcement (256-keypresent)")

	ts, bearer := setupHTTPServer(t, tenantA)
	resp, body := get(t, ts, "/v1/controls/"+cid.String()+"/coverage", bearer)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d; want 200; body=%s", resp.StatusCode, body)
	}
	// Peek at raw JSON so we can assert key presence even when value is
	// null. json.Unmarshal-into-struct hides absent keys vs null.
	var raw struct {
		Requirements []map[string]json.RawMessage `json:"requirements"`
	}
	if err := json.Unmarshal(body, &raw); err != nil {
		t.Fatalf("unmarshal: %v\nbody=%s", err, body)
	}
	if len(raw.Requirements) == 0 {
		t.Fatal("expected requirements")
	}
	for i, r := range raw.Requirements {
		if _, ok := r["coverage"]; !ok {
			t.Fatalf("requirements[%d] missing `coverage` key; raw=%s", i, string(body))
		}
	}
}
