package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/mgoodric/security-atlas/internal/controlimport"
)

// rowsAPI stands in for GET /v1/controls/imports/{id}/rows and records the
// request it received.
func rowsAPI(t *testing.T, status int, body any, gotPath, gotQuery, gotAuth *string) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			http.Error(w, "unexpected "+r.Method, http.StatusMethodNotAllowed)
			return
		}
		*gotPath, *gotQuery, *gotAuth = r.URL.Path, r.URL.RawQuery, r.Header.Get("Authorization")
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(status)
		_ = json.NewEncoder(w).Encode(body)
	}))
	t.Cleanup(srv.Close)
	return srv
}

var (
	cc61 = controlimport.Requirement{Code: "CC6.1", RequirementID: "r-61"}
	cc62 = controlimport.Requirement{Code: "CC6.2", RequirementID: "r-62"}
	cc71 = controlimport.Requirement{Code: "CC7.1", RequirementID: "r-71"}
)

func TestControlsImportShow_PrintsCandidatesWithSourceAndFit(t *testing.T) {
	resetControlsAuth(t)
	var path, query, auth string
	srv := rowsAPI(t, http.StatusOK, controlimport.RowList{
		BatchID:           "batch-1",
		SCFRelease:        &controlimport.SCFRelease{VersionID: "v", Version: "2026.3"},
		SuggestionsStatus: controlimport.SuggestionsReady,
		Rows: []controlimport.BatchControl{
			{
				RowNumber: 2, ExternalID: "ORG-AC-01", Title: "MFA for workforce", Status: "pending",
				TestedUnder: []controlimport.Requirement{cc61, cc62},
				Candidates: []controlimport.Candidate{
					{Rank: 1, SCFID: "IAC-06", Title: "Multi-Factor Authentication", Source: controlimport.SourceCrosswalk,
						Reaches: []controlimport.Requirement{cc61, cc62}, TestedCount: 2},
					{Rank: 2, SCFID: "IAC-15", Title: "Account Management", Source: controlimport.SourceCrosswalk,
						Reaches: []controlimport.Requirement{cc62}, TestedCount: 2},
					{Rank: 3, SCFID: "IAC-01", Title: "Identity & Access Management", Source: controlimport.SourceKeyword,
						Reaches: []controlimport.Requirement{}, TestedCount: 2},
				},
			},
			{
				RowNumber: 7, ExternalID: "ORG-HR-01", Title: "Background checks", Status: "pending",
				TestedUnder: []controlimport.Requirement{},
				Candidates: []controlimport.Candidate{
					{Rank: 1, SCFID: "HRS-04", Title: "Personnel Screening", Source: controlimport.SourceKeyword, Reaches: []controlimport.Requirement{}},
				},
			},
			{
				RowNumber: 8, ExternalID: "ORG-OP-01", Title: "Incident monitoring", Status: "pending",
				TestedUnder: []controlimport.Requirement{cc71}, NoCandidateReaches: true,
				Candidates: []controlimport.Candidate{
					{Rank: 1, SCFID: "MON-01", Title: "Continuous Monitoring", Source: controlimport.SourceKeyword,
						Reaches: []controlimport.Requirement{}, TestedCount: 1},
				},
			},
		},
	}, &path, &query, &auth)

	out, err := runImportCmd(t, "--endpoint", srv.URL, "--token", "tok-123", "controls", "import", "show", "batch-1")
	if err != nil {
		t.Fatalf("controls import show: %v\n%s", err, out)
	}
	if path != "/v1/controls/imports/batch-1/rows" || query != "" || auth != "Bearer tok-123" {
		t.Errorf("request path=%q query=%q auth=%q", path, query, auth)
	}
	for _, want := range []string{
		"import batch batch-1  SCF release 2026.3  suggestions ready",
		"row 2  ORG-AC-01  MFA for workforce  [pending]",
		"  tested under: CC6.1, CC6.2",
		"  1. IAC-06  Multi-Factor Authentication  (crosswalk; reaches 2 of 2: CC6.1, CC6.2)",
		"  2. IAC-15  Account Management  (crosswalk; reaches 1 of 2: CC6.2)",
		"  3. IAC-01  Identity & Access Management  (keyword; reaches 0 of 2)",
		"  1. HRS-04  Personnel Screening  (keyword)",
		"row 8  ORG-OP-01  Incident monitoring  [pending]",
		"  flag: no candidate reaches a tested criterion",
		"  1. MON-01  Continuous Monitoring  (keyword; reaches 0 of 1)",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("output missing %q:\n%s", want, out)
		}
	}
}

func TestControlsImportShow_PassesFilters(t *testing.T) {
	resetControlsAuth(t)
	var path, query, auth string
	srv := rowsAPI(t, http.StatusOK, controlimport.RowList{BatchID: "batch-1", SuggestionsStatus: "ready"}, &path, &query, &auth)

	out, err := runImportCmd(t, "--endpoint", srv.URL, "--token", "tok", "controls", "import", "show", "batch-1",
		"--status", "pending", "--no-candidate-reaches")
	if err != nil {
		t.Fatalf("controls import show: %v\n%s", err, out)
	}
	if query != "no_candidate_reaches=true&status=pending" {
		t.Errorf("query = %q", query)
	}
	if !strings.Contains(out, "SCF release none") || !strings.Contains(out, "no matching rows") {
		t.Errorf("output = %s", out)
	}
}

func TestControlsImportShow_SurfacesAPIError(t *testing.T) {
	resetControlsAuth(t)
	var path, query, auth string
	srv := rowsAPI(t, http.StatusNotFound, map[string]string{"error": "import batch not found"}, &path, &query, &auth)

	_, err := runImportCmd(t, "--endpoint", srv.URL, "--token", "tok", "controls", "import", "show", "nope")
	if err == nil || !strings.Contains(err.Error(), "HTTP 404") || !strings.Contains(err.Error(), "import batch not found") {
		t.Fatalf("err = %v, want the API's 404 message", err)
	}
}
