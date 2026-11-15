package main

import (
	"net/http"
	"strings"
	"testing"

	"github.com/mgoodric/security-atlas/internal/controlimport"
)

func TestControlsImportApproveClear(t *testing.T) {
	resetControlsAuth(t)
	api := &decideAPI{status: http.StatusOK, response: controlimport.ApproveClearResult{
		BatchID: "batch-1", Qualifying: 4, ApprovedCount: 2,
		Approved: []controlimport.RowDecision{
			{RowNumber: 2, ExternalID: "ORG-AC-01", Status: controlimport.RowStatusApproved, SCFID: "IAC-06", ControlID: "ctl-1"},
			{RowNumber: 3, ExternalID: "ORG-AC-02", Status: controlimport.RowStatusApproved, SCFID: "IAC-17", ControlID: "ctl-2"},
		},
		Duplicates: []controlimport.RowDecision{
			{RowNumber: 5, ExternalID: "ORG-AC-04", Status: controlimport.RowStatusDuplicate, DuplicateOf: "ctl-0"},
		},
		Skipped: []controlimport.ApproveClearSkip{
			{RowNumber: 6, ExternalID: "AC-01", Reason: "a control with this bundle ID already exists: ac-01 (from AC-01)"},
		},
	}}
	srv := api.server(t)

	out, err := runImportCmd(t, "--endpoint", srv.URL, "--token", "tok-123",
		"controls", "import", "approve-clear", "batch-1")
	if err != nil {
		t.Fatalf("approve-clear: %v\n%s", err, out)
	}
	if api.gotPath != "/v1/controls/imports/batch-1/approve-clear" || api.gotAuth != "Bearer tok-123" {
		t.Errorf("request path=%q auth=%q", api.gotPath, api.gotAuth)
	}
	for _, want := range []string{
		"approved 2 of 4 clear rows (1 duplicate, 1 skipped)",
		"row 2  ORG-AC-01  approved  IAC-06 -> control ctl-1",
		"row 3  ORG-AC-02  approved  IAC-17 -> control ctl-2",
		"row 5  ORG-AC-04  duplicate of control ctl-0",
		"row 6  AC-01  skipped: a control with this bundle ID already exists",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("output missing %q:\n%s", want, out)
		}
	}
}

func TestControlsImportApproveClear_SurfacesRefusal(t *testing.T) {
	resetControlsAuth(t)
	api := &decideAPI{status: http.StatusForbidden, response: map[string]string{
		"error": "a person must approve or reject: sign in as a user",
	}}
	srv := api.server(t)

	_, err := runImportCmd(t, "--endpoint", srv.URL, "--token", "machine",
		"controls", "import", "approve-clear", "batch-1")
	if err == nil || !strings.Contains(err.Error(), "HTTP 403") || !strings.Contains(err.Error(), "a person must approve") {
		t.Fatalf("err = %v, want the API's 403", err)
	}
}

func TestControlsImportApproveClear_NeedsBatch(t *testing.T) {
	resetControlsAuth(t)
	if _, err := runImportCmd(t, "--token", "tok", "controls", "import", "approve-clear"); err == nil {
		t.Fatal("approve-clear without a batch succeeded, want an argument error")
	}
}
