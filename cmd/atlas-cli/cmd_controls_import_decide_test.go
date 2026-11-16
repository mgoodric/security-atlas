package main

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/mgoodric/security-atlas/internal/control"
	"github.com/mgoodric/security-atlas/internal/controlimport"
)

// decideAPI stands in for the rows list and the approve / reject routes of
// one batch, recording the decision request it received.
type decideAPI struct {
	status   int
	response any
	gotPath  string
	gotAuth  string
	gotBody  map[string]any
}

func (a *decideAPI) server(t *testing.T) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if r.Method == http.MethodGet && r.URL.Path == "/v1/controls/imports/batch-1/rows" {
			_ = json.NewEncoder(w).Encode(controlimport.RowList{BatchID: "batch-1", Rows: []controlimport.BatchControl{
				{ID: "row-uuid-2", RowNumber: 2, ExternalID: "ORG-AC-01"},
				{ID: "row-uuid-3", RowNumber: 3, ExternalID: "ORG-AC-02"},
			}})
			return
		}
		if r.Method != http.MethodPost {
			http.Error(w, "unexpected "+r.Method, http.StatusMethodNotAllowed)
			return
		}
		a.gotPath, a.gotAuth = r.URL.Path, r.Header.Get("Authorization")
		raw, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(raw, &a.gotBody)
		w.WriteHeader(a.status)
		_ = json.NewEncoder(w).Encode(a.response)
	}))
	t.Cleanup(srv.Close)
	return srv
}

func TestControlsImportApprove_WithCandidate(t *testing.T) {
	resetControlsAuth(t)
	api := &decideAPI{status: http.StatusOK, response: controlimport.RowDecision{
		BatchID: "batch-1", RowID: "row-uuid-2", RowNumber: 2, ExternalID: "ORG-AC-01",
		Status: controlimport.RowStatusApproved, DecidedBy: "user-1",
		ControlID: "ctl-1", BundleID: "org-ac-01", SCFID: "IAC-06", SCFRelease: "2025.2",
		ExternalRefs: []control.ExternalRef{
			{System: "organisation", Kind: "reference", ID: "ORG-AC-01"},
			{System: "soc2", Kind: "tested_under", ID: "CC6.1"},
		},
	}}
	srv := api.server(t)

	out, err := runImportCmd(t, "--endpoint", srv.URL, "--token", "tok-123",
		"controls", "import", "approve", "batch-1", "2", "--candidate", "1", "--implementation-type", "automated")
	if err != nil {
		t.Fatalf("approve: %v\n%s", err, out)
	}
	if api.gotPath != "/v1/controls/imports/batch-1/rows/row-uuid-2/approve" || api.gotAuth != "Bearer tok-123" {
		t.Errorf("request path=%q auth=%q", api.gotPath, api.gotAuth)
	}
	if api.gotBody["candidate_rank"] != float64(1) || api.gotBody["implementation_type"] != "automated" || api.gotBody["scf_id"] != nil {
		t.Errorf("body = %v", api.gotBody)
	}
	for _, want := range []string{
		"row 2  ORG-AC-01  approved",
		"control: ctl-1 (bundle org-ac-01)",
		"anchor:  IAC-06 in SCF release 2025.2",
		"ref:     organisation reference ORG-AC-01",
		"ref:     soc2 tested_under CC6.1",
		"decided by user-1",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("output missing %q:\n%s", want, out)
		}
	}
}

func TestControlsImportApprove_WithSearchedAnchorAndRowID(t *testing.T) {
	resetControlsAuth(t)
	api := &decideAPI{status: http.StatusOK, response: controlimport.RowDecision{Status: controlimport.RowStatusApproved}}
	srv := api.server(t)

	out, err := runImportCmd(t, "--endpoint", srv.URL, "--token", "tok",
		"controls", "import", "approve", "batch-1", "row-uuid-3", "--scf-id", "MON-02",
		"--applicability-expr", `{"op":"eq","dim":"env","value":"prod"}`)
	if err != nil {
		t.Fatalf("approve: %v\n%s", err, out)
	}
	if api.gotPath != "/v1/controls/imports/batch-1/rows/row-uuid-3/approve" {
		t.Errorf("path = %q", api.gotPath)
	}
	if api.gotBody["scf_id"] != "MON-02" || api.gotBody["candidate_rank"] != nil {
		t.Errorf("body = %v", api.gotBody)
	}
	if appl, ok := api.gotBody["applicability_expr"].(map[string]any); !ok || appl["dim"] != "env" {
		t.Errorf("applicability_expr = %v", api.gotBody["applicability_expr"])
	}
}

func TestControlsImportApprove_NeedsExactlyOneAnchorChoice(t *testing.T) {
	for _, args := range [][]string{
		{"controls", "import", "approve", "batch-1", "2"},
		{"controls", "import", "approve", "batch-1", "2", "--candidate", "1", "--scf-id", "IAC-06"},
	} {
		resetControlsAuth(t)
		_, err := runImportCmd(t, append([]string{"--endpoint", "http://127.0.0.1:1", "--token", "tok"}, args...)...)
		if err == nil || !strings.Contains(err.Error(), "exactly one of --candidate or --scf-id") {
			t.Errorf("%v: err = %v", args, err)
		}
	}
}

func TestControlsImportApprove_SurfacesRefusal(t *testing.T) {
	resetControlsAuth(t)
	api := &decideAPI{status: http.StatusForbidden, response: map[string]string{
		"error": "a person must approve or reject: sign in as a user",
	}}
	srv := api.server(t)

	_, err := runImportCmd(t, "--endpoint", srv.URL, "--token", "machine",
		"controls", "import", "approve", "batch-1", "row-uuid-2", "--candidate", "1")
	if err == nil || !strings.Contains(err.Error(), "HTTP 403") || !strings.Contains(err.Error(), "a person must approve") {
		t.Fatalf("err = %v, want the API's 403", err)
	}
}

func TestControlsImportApprove_UnknownRowNumber(t *testing.T) {
	resetControlsAuth(t)
	api := &decideAPI{status: http.StatusOK}
	srv := api.server(t)

	_, err := runImportCmd(t, "--endpoint", srv.URL, "--token", "tok",
		"controls", "import", "approve", "batch-1", "9", "--candidate", "1")
	if err == nil || !strings.Contains(err.Error(), "no staged row 9") {
		t.Fatalf("err = %v", err)
	}
	if api.gotPath != "" {
		t.Errorf("approve was sent to %q", api.gotPath)
	}
}

func TestControlsImportReject(t *testing.T) {
	resetControlsAuth(t)
	api := &decideAPI{status: http.StatusOK, response: controlimport.RowDecision{
		RowNumber: 3, ExternalID: "ORG-AC-02", Status: controlimport.RowStatusRejected,
		DecidedBy: "user-1", RejectionReason: "covered by ORG-AC-01",
	}}
	srv := api.server(t)

	out, err := runImportCmd(t, "--endpoint", srv.URL, "--token", "tok",
		"controls", "import", "reject", "batch-1", "3", "--reason", "covered by ORG-AC-01")
	if err != nil {
		t.Fatalf("reject: %v\n%s", err, out)
	}
	if api.gotPath != "/v1/controls/imports/batch-1/rows/row-uuid-3/reject" || api.gotBody["reason"] != "covered by ORG-AC-01" {
		t.Errorf("path=%q body=%v", api.gotPath, api.gotBody)
	}
	for _, want := range []string{"row 3  ORG-AC-02  rejected", "reason:  covered by ORG-AC-01", "decided by user-1"} {
		if !strings.Contains(out, want) {
			t.Errorf("output missing %q:\n%s", want, out)
		}
	}
}

func TestControlsImportReject_RequiresReason(t *testing.T) {
	resetControlsAuth(t)
	_, err := runImportCmd(t, "--endpoint", "http://127.0.0.1:1", "--token", "tok",
		"controls", "import", "reject", "batch-1", "3")
	if err == nil || !strings.Contains(err.Error(), "--reason is required") {
		t.Fatalf("err = %v", err)
	}
}

func TestControlsImportShow_PrintsDecisions(t *testing.T) {
	resetControlsAuth(t)
	var path, query, auth string
	srv := rowsAPI(t, http.StatusOK, controlimport.RowList{
		BatchID: "batch-1",
		Rows: []controlimport.BatchControl{
			{ID: "r2", RowNumber: 2, ExternalID: "ORG-AC-01", Title: "MFA", Status: "approved", ControlID: "ctl-1", ApprovedSCFID: "IAC-06"},
			{ID: "r3", RowNumber: 3, ExternalID: "ORG-AC-02", Title: "Review", Status: "rejected", RejectionReason: "out of scope"},
			{ID: "r7", RowNumber: 7, ExternalID: "ORG-HR-01", Title: "Checks", Status: "duplicate", ControlID: "ctl-9"},
		},
	}, &path, &query, &auth)

	out, err := runImportCmd(t, "--endpoint", srv.URL, "--token", "tok", "controls", "import", "show", "batch-1")
	if err != nil {
		t.Fatalf("show: %v\n%s", err, out)
	}
	for _, want := range []string{
		"  id: r2\n  approved as control ctl-1 (IAC-06)\n",
		"  id: r3\n  rejected: out of scope\n",
		"  id: r7\n  duplicate of control ctl-9\n",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("output missing %q:\n%s", want, out)
		}
	}
}
