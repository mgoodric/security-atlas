//go:build integration

package anchors_test

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

const tenantA = "11111111-1111-1111-1111-111111111111"

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

func setupHTTPServer(t *testing.T) (*httptest.Server, string) {
	t.Helper()

	// Wipe + import via admin pool. Slice 007 added fw_to_scf_edges +
	// framework_requirements. We wipe only slice-007 + slice-006 owned
	// rows; controls or other tenant-scoped data that other packages
	// might leave behind is NOT in our cleanup scope (and would block
	// scf_anchors deletes via FK from controls.scf_anchor_id under the
	// full integration suite).
	adminPool := openPoolDSN(t, adminDSN(t))
	defer adminPool.Close()
	for _, stmt := range []string{
		"DELETE FROM fw_to_scf_edges",
		"DELETE FROM framework_requirements",
		"DELETE FROM framework_versions WHERE framework_id IN (SELECT id FROM frameworks WHERE slug = 'soc2' AND tenant_id IS NULL)",
		"DELETE FROM frameworks WHERE slug = 'soc2' AND tenant_id IS NULL",
	} {
		if _, err := adminPool.Exec(context.Background(), stmt); err != nil {
			t.Fatalf("cleanup %q: %v", stmt, err)
		}
	}
	// SCF anchors: load idempotently — only wipe + reimport if zero rows.
	// The slice-006 importer is content-equality-aware so re-importing a
	// loaded catalog is a no-op anyway, but skipping when fully loaded
	// keeps test runtime bounded under the full integration suite.
	var anchorCount int
	if err := adminPool.QueryRow(context.Background(), `SELECT count(*) FROM scf_anchors`).Scan(&anchorCount); err != nil {
		t.Fatalf("count scf_anchors: %v", err)
	}
	if anchorCount == 0 {
		cat, err := scfimport.Load("../../../migrations/fixtures/scf-sample.json")
		if err != nil {
			t.Fatalf("Load: %v", err)
		}
		if _, err := scfimport.Import(context.Background(), adminPool, cat); err != nil {
			t.Fatalf("Import: %v", err)
		}
	}
	// Slice 007: load the SOC 2 crosswalk so the requirement-traversal
	// route has data to return.
	cw, err := soc2import.Load(filepath.Join("..", "..", "..", "data", "crosswalks", "soc2-tsc-2017.yaml"))
	if err != nil {
		t.Fatalf("soc2import.Load: %v", err)
	}
	if _, err := soc2import.Import(context.Background(), adminPool, cw); err != nil {
		t.Fatalf("soc2import.Import: %v", err)
	}

	// Boot the server with the app role.
	appPool := openPoolDSN(t, appDSN(t))
	srv := api.New(api.Config{RotationGrace: time.Hour})
	srv.AttachDB(appPool)
	// Slice 197: mint a JWT via the slice 190 path; the legacy
	// `IssueBootstrapCredential` opaque-bearer middleware is retired.
	// ViewerFor mirrors the legacy default (no admin/approver/owner
	// elevation) — sufficient for the read-only anchor list endpoints.
	bearer := srv.IssueTestJWT(t, testjwt.ViewerFor(uuid.MustParse(tenantA)))
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

func openPoolDSN(t *testing.T, dsn string) *pgxpool.Pool {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	pool, err := pgxpool.New(ctx, dsn)
	if err != nil {
		t.Fatalf("pgxpool.New: %v", err)
	}
	return pool
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

func TestListAnchors_ReturnsDBBackedAnchors(t *testing.T) {
	ts, bearer := setupHTTPServer(t)
	resp, body := get(t, ts, "/v1/anchors", bearer)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d; want 200; body=%s", resp.StatusCode, body)
	}
	var got struct {
		Anchors []struct {
			ID, SCFID, Family, Name, Description string `json:"-"`
		} `json:"anchors"`
	}
	// Custom decode because the embedded tags use the wire names.
	var raw struct {
		Anchors []map[string]string `json:"anchors"`
	}
	if err := json.Unmarshal(body, &raw); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if len(raw.Anchors) < 30 {
		t.Fatalf("expected >=30 anchors from the fixture, got %d", len(raw.Anchors))
	}
	for _, a := range raw.Anchors {
		if a["id"] == "" || a["scf_id"] == "" || a["family"] == "" || a["name"] == "" {
			t.Fatalf("anchor missing required field: %+v", a)
		}
	}
	_ = got
}

func TestGetAnchorByID_BySCFID(t *testing.T) {
	ts, bearer := setupHTTPServer(t)
	resp, body := get(t, ts, "/v1/anchors/IAC-06", bearer)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d; want 200; body=%s", resp.StatusCode, body)
	}
	var got struct {
		Anchor map[string]string `json:"anchor"`
	}
	if err := json.Unmarshal(body, &got); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if got.Anchor["scf_id"] != "IAC-06" {
		t.Fatalf("scf_id = %q; want IAC-06", got.Anchor["scf_id"])
	}
	if got.Anchor["name"] != "Multi-Factor Authentication (MFA)" {
		t.Fatalf("name = %q", got.Anchor["name"])
	}
}

func TestGetAnchorByID_UUID(t *testing.T) {
	ts, bearer := setupHTTPServer(t)

	// First find the UUID via the SCF ID.
	_, body := get(t, ts, "/v1/anchors/IAC-06", bearer)
	var first struct {
		Anchor map[string]string `json:"anchor"`
	}
	if err := json.Unmarshal(body, &first); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	uuid := first.Anchor["id"]
	if uuid == "" {
		t.Fatal("anchor id empty")
	}

	resp, body := get(t, ts, "/v1/anchors/"+uuid, bearer)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status by UUID = %d; want 200; body=%s", resp.StatusCode, body)
	}
}

func TestGetAnchor_UnknownReturns404(t *testing.T) {
	ts, bearer := setupHTTPServer(t)
	resp, _ := get(t, ts, "/v1/anchors/ZZZ-99", bearer)
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("status = %d; want 404", resp.StatusCode)
	}
}

func TestRequirementsForAnchor_StillReturnsMappings(t *testing.T) {
	ts, bearer := setupHTTPServer(t)
	resp, body := get(t, ts, "/v1/anchors/IAC-06/requirements", bearer)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d; want 200; body=%s", resp.StatusCode, body)
	}
	var got struct {
		Anchor       map[string]string `json:"anchor"`
		Requirements []map[string]any  `json:"requirements"`
	}
	if err := json.Unmarshal(body, &got); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if got.Anchor["scf_id"] != "IAC-06" {
		t.Fatalf("anchor scf_id = %q", got.Anchor["scf_id"])
	}
	if len(got.Requirements) == 0 {
		t.Fatal("expected requirement mappings for IAC-06")
	}
}

func TestListFrameworks_IncludesSCF(t *testing.T) {
	ts, bearer := setupHTTPServer(t)
	resp, body := get(t, ts, "/v1/frameworks", bearer)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d; want 200; body=%s", resp.StatusCode, body)
	}
	var got struct {
		Frameworks []map[string]string `json:"frameworks"`
	}
	if err := json.Unmarshal(body, &got); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	found := false
	for _, f := range got.Frameworks {
		if f["slug"] == "scf" {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("SCF framework not in list: %+v", got.Frameworks)
	}
}

func TestListSCFVersions_HasCurrent(t *testing.T) {
	ts, bearer := setupHTTPServer(t)
	resp, body := get(t, ts, "/v1/frameworks/scf/versions", bearer)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d; want 200; body=%s", resp.StatusCode, body)
	}
	var got struct {
		Versions []map[string]string `json:"versions"`
	}
	if err := json.Unmarshal(body, &got); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if len(got.Versions) == 0 {
		t.Fatal("expected at least one SCF version")
	}
	currentCount := 0
	for _, v := range got.Versions {
		if v["status"] == "current" {
			currentCount++
		}
	}
	if currentCount != 1 {
		t.Fatalf("expected exactly one 'current' version, got %d", currentCount)
	}
}

func TestListAnchors_RejectsMissingBearer(t *testing.T) {
	ts, _ := setupHTTPServer(t)
	resp, _ := get(t, ts, "/v1/anchors", "")
	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("status = %d; want 401", resp.StatusCode)
	}
}

// Slice 007 — AC-3: the reverse-traversal endpoint returns SCF anchors
// for a SOC 2 requirement with relationship_type + strength + attribution.
// Path form: `soc2:2017:CC6.6` (slug:version:code).
func TestAnchorsForRequirement_ResolvesSOC2CC66BySlugVersionCode(t *testing.T) {
	ts, bearer := setupHTTPServer(t)
	resp, body := get(t, ts, "/v1/requirements/soc2:2017:CC6.6/anchors", bearer)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d; want 200; body=%s", resp.StatusCode, body)
	}
	var got struct {
		Requirement map[string]string `json:"requirement"`
		Anchors     []struct {
			SCFID             string  `json:"scf_id"`
			RelationshipType  string  `json:"relationship_type"`
			Strength          float64 `json:"strength"`
			SourceAttribution string  `json:"source_attribution"`
		} `json:"anchors"`
	}
	if err := json.Unmarshal(body, &got); err != nil {
		t.Fatalf("unmarshal: %v\nbody=%s", err, body)
	}
	if got.Requirement["code"] != "CC6.6" {
		t.Fatalf("requirement.code = %q; want CC6.6", got.Requirement["code"])
	}
	if len(got.Anchors) == 0 {
		t.Fatal("expected at least one anchor for CC6.6 (NET-04, IAC-06 per crosswalk)")
	}
	for _, a := range got.Anchors {
		if a.SCFID == "" {
			t.Fatalf("anchor missing scf_id: %+v", a)
		}
		if a.RelationshipType == "" {
			t.Fatalf("anchor missing relationship_type: %+v", a)
		}
		if a.SourceAttribution != "community_draft" {
			t.Fatalf("anchor source_attribution = %q; want community_draft", a.SourceAttribution)
		}
		if a.Strength < 0.0 || a.Strength > 1.0 {
			t.Fatalf("anchor strength %v out of [0,1]", a.Strength)
		}
	}
}

// Convenience form: omit the version → resolve against the framework's
// current version. `soc2::CC6.6` should match the same row as
// `soc2:2017:CC6.6`.
func TestAnchorsForRequirement_ConvenienceFormCurrentVersion(t *testing.T) {
	ts, bearer := setupHTTPServer(t)
	resp, body := get(t, ts, "/v1/requirements/soc2::CC6.6/anchors", bearer)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d; want 200; body=%s", resp.StatusCode, body)
	}
	var got struct {
		Requirement map[string]string `json:"requirement"`
	}
	if err := json.Unmarshal(body, &got); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if got.Requirement["code"] != "CC6.6" {
		t.Fatalf("requirement.code = %q", got.Requirement["code"])
	}
}

func TestAnchorsForRequirement_404OnUnknownCode(t *testing.T) {
	ts, bearer := setupHTTPServer(t)
	resp, _ := get(t, ts, "/v1/requirements/soc2:2017:CC99.99/anchors", bearer)
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("status = %d; want 404", resp.StatusCode)
	}
}

func TestAnchorsForRequirement_RejectsMissingBearer(t *testing.T) {
	ts, _ := setupHTTPServer(t)
	resp, _ := get(t, ts, "/v1/requirements/soc2:2017:CC6.6/anchors", "")
	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("status = %d; want 401", resp.StatusCode)
	}
}
