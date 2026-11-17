package main

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// The overlay commands reuse taxonomyAPI (cmd_taxonomy_test.go) as their
// stand-in server.

func overlayResp(id, status string) map[string]any {
	return map[string]any{"overlay_edge": map[string]any{
		"id": id, "framework": "soc2", "framework_version": "2017", "requirement_code": "CC1.1",
		"scf_release": "2025.2", "anchor_code": "HRS-07.1", "relationship_type": "intersects_with",
		"strength": 0.8, "status": status, "source_ref": "FY25 SOC 2 report, s4",
	}}
}

func runOverlay(t *testing.T, srv *httptest.Server, args ...string) (string, error) {
	t.Helper()
	resetControlsAuth(t)
	return runImportCmd(t, append([]string{"--endpoint", srv.URL, "--token", "tok", "mappings", "overlay"}, args...)...)
}

func TestMappingsOverlayAdd_PostsTheEdge(t *testing.T) {
	api := &taxonomyAPI{}
	srv := api.server(t, http.StatusCreated, overlayResp("e-1", "active"))

	out, err := runOverlay(t, srv, "add", "soc2", "CC1.1", "HRS-07.1",
		"--framework-version", "2017", "--scf-release", "2025.2",
		"--relationship", "intersects_with", "--strength", "0.8",
		"--rationale", "auditor tested it under CC1.1", "--source", "FY25 SOC 2 report, s4")
	if err != nil {
		t.Fatalf("add: %v\n%s", err, out)
	}
	if api.method != http.MethodPost || api.path != "/v1/mapping-overlays" || api.auth != "Bearer tok" {
		t.Errorf("request %s %s auth=%q", api.method, api.path, api.auth)
	}
	want := map[string]any{
		"framework": "soc2", "framework_version": "2017", "requirement_code": "CC1.1",
		"scf_release": "2025.2", "anchor_code": "HRS-07.1", "relationship_type": "intersects_with",
		"strength": 0.8, "rationale": "auditor tested it under CC1.1", "source_ref": "FY25 SOC 2 report, s4",
	}
	for k, v := range want {
		if api.body[k] != v {
			t.Errorf("body[%s] = %v; want %v", k, api.body[k], v)
		}
	}
	if _, ok := api.body["tenant_id"]; ok {
		t.Error("body carries a tenant: the tenant must come from the credential")
	}
	if !strings.Contains(out, "added e-1: soc2:2017:CC1.1 -> SCF 2025.2:HRS-07.1 [active]") {
		t.Errorf("output:\n%s", out)
	}
}

func TestMappingsOverlayAdd_RequiresRationaleAndSource(t *testing.T) {
	api := &taxonomyAPI{}
	srv := api.server(t, http.StatusCreated, overlayResp("e-1", "active"))
	base := []string{"add", "soc2", "CC1.1", "HRS-07.1", "--relationship", "equal", "--strength", "1"}

	if _, err := runOverlay(t, srv, append(base, "--source", "s")...); err == nil || !strings.Contains(err.Error(), "--rationale") {
		t.Errorf("missing rationale: err=%v", err)
	}
	if _, err := runOverlay(t, srv, append(base, "--rationale", "r")...); err == nil || !strings.Contains(err.Error(), "--source") {
		t.Errorf("missing source: err=%v", err)
	}
	if api.method != "" {
		t.Errorf("a refused add still called the API: %s %s", api.method, api.path)
	}
}

func TestMappingsOverlayAdd_SurfacesAPIRefusal(t *testing.T) {
	api := &taxonomyAPI{}
	srv := api.server(t, http.StatusConflict, map[string]any{
		"error": "mapping overlay: catalog edge 7f0c (equal, verified) already joins this requirement and anchor",
		"code":  "catalog_edge_exists",
	})
	_, err := runOverlay(t, srv, "add", "soc2", "CC6.1", "IAC-01", "--relationship", "equal", "--strength", "1",
		"--rationale", "r", "--source", "s")
	if err == nil || !strings.Contains(err.Error(), "HTTP 409") || !strings.Contains(err.Error(), "catalog edge 7f0c") {
		t.Errorf("err = %v", err)
	}
}

func TestMappingsOverlayList_FiltersAndPrints(t *testing.T) {
	api := &taxonomyAPI{}
	srv := api.server(t, http.StatusOK, map[string]any{"overlay_edges": []map[string]any{
		overlayResp("e-1", "active")["overlay_edge"].(map[string]any),
		overlayResp("e-2", "retired")["overlay_edge"].(map[string]any),
	}})

	out, err := runOverlay(t, srv, "list", "--framework", "soc2", "--status", "retired", "--anchor", "HRS-07.1")
	if err != nil {
		t.Fatalf("list: %v\n%s", err, out)
	}
	if api.method != http.MethodGet || api.path != "/v1/mapping-overlays" ||
		api.query != "anchor=HRS-07.1&framework=soc2&status=retired" {
		t.Errorf("request %s %s?%s", api.method, api.path, api.query)
	}
	for _, want := range []string{
		"ID", "REQUIREMENT", "ANCHOR", "STATUS",
		"e-1  soc2:2017:CC1.1  SCF 2025.2:HRS-07.1  intersects_with  0.8       active",
		"e-2  soc2:2017:CC1.1  SCF 2025.2:HRS-07.1  intersects_with  0.8       retired",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("output missing %q:\n%s", want, out)
		}
	}

	if _, err := runOverlay(t, srv, "list"); err != nil || api.query != "" {
		t.Errorf("unfiltered list: err=%v query=%q", err, api.query)
	}
}

func TestMappingsOverlayRetire_PostsReason(t *testing.T) {
	api := &taxonomyAPI{}
	srv := api.server(t, http.StatusOK, overlayResp("e-1", "retired"))

	out, err := runOverlay(t, srv, "retire", "e-1", "--reason", "auditor withdrew the finding")
	if err != nil {
		t.Fatalf("retire: %v\n%s", err, out)
	}
	if api.method != http.MethodPost || api.path != "/v1/mapping-overlays/e-1/retire" ||
		api.body["reason"] != "auditor withdrew the finding" {
		t.Errorf("request %s %s body=%v", api.method, api.path, api.body)
	}
	if !strings.Contains(out, "retired e-1") || !strings.Contains(out, "[retired]") {
		t.Errorf("output:\n%s", out)
	}

	api.method = ""
	if _, err := runOverlay(t, srv, "retire", "e-1"); err == nil || !strings.Contains(err.Error(), "--reason") {
		t.Errorf("missing reason: err=%v", err)
	}
	if api.method != "" {
		t.Error("a retire without a reason still called the API")
	}
}

func TestMappingsOverlayAuditedUnder_PrintsRowsUnresolvedAndTruncation(t *testing.T) {
	api := &taxonomyAPI{}
	srv := api.server(t, http.StatusOK, map[string]any{
		"rows": []map[string]any{{
			"control_bundle_id": "ctrl-nda", "anchor_code": "IAC-06", "anchor_scf_release": "2025.2",
			"anchor_fit": "partial", "framework": "soc2", "framework_version": "2017",
			"requirement_code": "CC1.4", "tested_under_note": "FY25 report s4",
			"candidate_anchors": []map[string]any{{"anchor_code": "HRS-06"}, {"anchor_code": "GOV-01"}},
		}},
		"count": 1, "total": 3, "truncated": true,
		"unresolved_references": []map[string]any{{
			"control_bundle_id": "ctrl-nda", "system": "soc2", "code": "CC9.9", "tested_under_note": "FY25 import",
		}},
		"unresolved_count": 1,
	})

	out, err := runOverlay(t, srv, "audited-under", "--framework", "soc2", "--limit", "1")
	if err != nil {
		t.Fatalf("audited-under: %v\n%s", err, out)
	}
	if api.method != http.MethodGet || api.path != "/v1/mapping-overlays/audited-under" ||
		api.query != "framework=soc2&limit=1" || api.auth != "Bearer tok" {
		t.Errorf("request %s %s?%s auth=%q", api.method, api.path, api.query, api.auth)
	}
	for _, want := range []string{
		"CRITERION", "CANDIDATES",
		"soc2:2017:CC1.4  ctrl-nda  SCF 2025.2:IAC-06  partial  HRS-06, GOV-01  FY25 report s4",
		"1 tested-under reference(s) name no requirement",
		"soc2 CC9.9 on ctrl-nda (FY25 import)",
		"truncated: showing 1 of 3 row(s) and 1 of 1 unresolved reference(s)",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("output missing %q:\n%s", want, out)
		}
	}
}

func TestMappingsOverlayAuditedUnder_CSVReadsTheExport(t *testing.T) {
	api := &taxonomyAPI{}
	srv := api.server(t, http.StatusOK, "framework,requirement_code")

	out, err := runOverlay(t, srv, "audited-under", "--csv")
	if err != nil {
		t.Fatalf("audited-under --csv: %v\n%s", err, out)
	}
	if api.path != "/v1/mapping-overlays/audited-under/export" || api.query != "" {
		t.Errorf("request %s?%s; want the export", api.path, api.query)
	}
	if !strings.Contains(out, "framework,requirement_code") {
		t.Errorf("output:\n%s", out)
	}
	if _, err := runOverlay(t, srv, "audited-under", "--csv", "--json"); err == nil {
		t.Error("--csv with --json: want an error")
	}
}
