package main

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/mgoodric/security-atlas/internal/control"
)

// remapAPI stands in for the remap proposal routes, recording the request
// it received and answering with status and response.
type remapAPI struct {
	status    int
	response  any
	gotMethod string
	gotPath   string
	gotQuery  string
	gotAuth   string
	gotBody   map[string]any
}

func (a *remapAPI) server(t *testing.T) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		a.gotMethod, a.gotPath, a.gotQuery = r.Method, r.URL.Path, r.URL.RawQuery
		a.gotAuth = r.Header.Get("Authorization")
		raw, _ := io.ReadAll(r.Body)
		a.gotBody = nil
		_ = json.Unmarshal(raw, &a.gotBody)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(a.status)
		_ = json.NewEncoder(w).Encode(a.response)
	}))
	t.Cleanup(srv.Close)
	return srv
}

var approvedDecision = control.RemapDecision{
	ProposalID: "prop-1", Decision: "approved", BundleID: "acme_mfa",
	LegacyControlID: "ctl-old", LegacySCFID: "IAC-06", LegacyRelease: "2025.1",
	NewControlID: "ctl-new", NewVersion: 2, NewSCFID: "IAC-60", NewRelease: "2025.2",
	DecidedBy: "user-1", DecidedAt: "2026-11-15T12:00:00Z",
}

func TestCatalogRemapList_FiltersAndRenders(t *testing.T) {
	resetControlsAuth(t)
	api := &remapAPI{status: http.StatusOK, response: map[string]any{
		"proposals": []map[string]any{{
			"id": "prop-1", "bundle_id": "acme_logging", "legacy_scf_id": "MON-01", "legacy_release": "2025.1",
			"target_release": "2025.2", "class": "split", "status": "open", "successor_scf_ids": []string{"MON-10", "MON-11"},
		}},
		"count": 1,
	}}
	srv := api.server(t)
	out, err := runImportCmd(t, "--endpoint", srv.URL, "--token", "tok-1",
		"catalog", "remap", "list", "--class", "split", "--status", "open")
	if err != nil {
		t.Fatalf("list: %v\n%s", err, out)
	}
	if api.gotMethod != http.MethodGet || api.gotPath != "/v1/controls/remap-proposals" ||
		api.gotQuery != "class=split&status=open" || api.gotAuth != "Bearer tok-1" {
		t.Errorf("request %s %s?%s auth=%q", api.gotMethod, api.gotPath, api.gotQuery, api.gotAuth)
	}
	if want := "prop-1  open  split  MON-01@2025.1 -> MON-10,MON-11 (2025.2)  acme_logging"; !strings.Contains(out, want) {
		t.Errorf("output %q; want it to contain %q", out, want)
	}
}

func TestCatalogRemapList_PagesAndNamesTheNextCursor(t *testing.T) {
	resetControlsAuth(t)
	api := &remapAPI{status: http.StatusOK, response: map[string]any{
		"proposals": []map[string]any{{
			"id": "prop-1", "bundle_id": "acme_logging", "legacy_scf_id": "MON-01", "legacy_release": "2025.1",
			"target_release": "2025.2", "class": "split", "status": "rebound", "successor_scf_ids": []string{"MON-01", "MON-11"},
		}},
		"count": 1, "total": 2, "next_cursor": "cur-1",
	}}
	srv := api.server(t)
	out, err := runImportCmd(t, "--endpoint", srv.URL, "--token", "tok-1",
		"catalog", "remap", "list", "--status", "rebound", "--class-not", "unchanged", "--limit", "1", "--cursor", "cur-0")
	if err != nil {
		t.Fatalf("list: %v\n%s", err, out)
	}
	if api.gotQuery != "class_not=unchanged&cursor=cur-0&limit=1&status=rebound" {
		t.Errorf("query %q", api.gotQuery)
	}
	if want := "1 of 2 shown; next page: --cursor cur-1"; !strings.Contains(out, want) {
		t.Errorf("output %q; want it to contain %q", out, want)
	}
}

func TestCatalogRemapApprove_SendsChosenAnchor(t *testing.T) {
	resetControlsAuth(t)
	api := &remapAPI{status: http.StatusOK, response: approvedDecision}
	srv := api.server(t)
	out, err := runImportCmd(t, "--endpoint", srv.URL, "--token", "tok-1",
		"catalog", "remap", "approve", "prop-1", "--scf-id", "IAC-60", "--reason", "same intent")
	if err != nil {
		t.Fatalf("approve: %v\n%s", err, out)
	}
	if api.gotMethod != http.MethodPost || api.gotPath != "/v1/controls/remap-proposals/prop-1/approve" {
		t.Errorf("request %s %s", api.gotMethod, api.gotPath)
	}
	if api.gotBody["scf_id"] != "IAC-60" || api.gotBody["reason"] != "same intent" {
		t.Errorf("body = %v", api.gotBody)
	}
	for _, want := range []string{
		"proposal prop-1  acme_mfa  approved",
		"legacy:  IAC-06 in SCF release 2025.1 (control ctl-old)",
		"new:     IAC-60 in SCF release 2025.2 (control ctl-new, version 2)",
		"decided by user-1 at 2026-11-15T12:00:00Z",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("output missing %q:\n%s", want, out)
		}
	}
}

func TestCatalogRemapApprove_WithoutAnchorSendsEmptyBodyAndSurfacesError(t *testing.T) {
	resetControlsAuth(t)
	api := &remapAPI{status: http.StatusBadRequest, response: map[string]string{
		"error": "choose the successor anchor (scf_id) to approve this proposal (class split)",
	}}
	srv := api.server(t)
	out, err := runImportCmd(t, "--endpoint", srv.URL, "--token", "tok-1", "catalog", "remap", "approve", "prop-1")
	if err == nil || !strings.Contains(err.Error(), "HTTP 400: choose the successor anchor") {
		t.Fatalf("err = %v; want the API's 400\n%s", err, out)
	}
	if len(api.gotBody) != 0 {
		t.Errorf("body = %v; want empty", api.gotBody)
	}
}

func TestCatalogRemapApproveUnchanged_ReportsApprovedAndSkipped(t *testing.T) {
	resetControlsAuth(t)
	api := &remapAPI{status: http.StatusOK, response: control.RemapBulkResult{
		ApprovedCount: 1,
		Approved:      []control.RemapDecision{approvedDecision},
		Skipped:       []control.RemapBulkSkip{{ProposalID: "prop-2", BundleID: "acme_keys", Reason: "the control changed since the proposal was made"}},
	}}
	srv := api.server(t)
	out, err := runImportCmd(t, "--endpoint", srv.URL, "--token", "tok-1", "catalog", "remap", "approve-unchanged")
	if err != nil {
		t.Fatalf("approve-unchanged: %v\n%s", err, out)
	}
	if api.gotMethod != http.MethodPost || api.gotPath != "/v1/controls/remap-proposals/approve-unchanged" {
		t.Errorf("request %s %s", api.gotMethod, api.gotPath)
	}
	for _, want := range []string{
		"approved 1 unchanged proposals, skipped 1",
		"proposal prop-1  acme_mfa  approved",
		"skipped prop-2  acme_keys: the control changed since the proposal was made",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("output missing %q:\n%s", want, out)
		}
	}
}

func TestCatalogRemapRejectAndKeepLegacy(t *testing.T) {
	for _, verb := range []string{"reject", "keep-legacy"} {
		t.Run(verb, func(t *testing.T) {
			resetControlsAuth(t)
			decision := map[string]string{"reject": "rejected", "keep-legacy": "kept_legacy"}[verb]
			api := &remapAPI{status: http.StatusOK, response: control.RemapDecision{
				ProposalID: "prop-3", Decision: decision, BundleID: "acme_change",
				LegacyControlID: "ctl-3", LegacySCFID: "CHG-02", LegacyRelease: "2025.1",
				Reason: "scope differs", DecidedBy: "user-2", DecidedAt: "2026-11-15T12:00:00Z",
			}}
			srv := api.server(t)
			out, err := runImportCmd(t, "--endpoint", srv.URL, "--token", "tok-1",
				"catalog", "remap", verb, "prop-3", "--reason", "scope differs")
			if err != nil {
				t.Fatalf("%s: %v\n%s", verb, err, out)
			}
			if api.gotPath != "/v1/controls/remap-proposals/prop-3/"+verb || api.gotBody["reason"] != "scope differs" {
				t.Errorf("request %s body %v", api.gotPath, api.gotBody)
			}
			for _, want := range []string{"proposal prop-3  acme_change  " + decision, "reason:  scope differs", "decided by user-2"} {
				if !strings.Contains(out, want) {
					t.Errorf("output missing %q:\n%s", want, out)
				}
			}
			if strings.Contains(out, "new:") {
				t.Errorf("%s output names a new anchor:\n%s", verb, out)
			}
		})
	}
}

func TestCatalogRemap_DecidedProposalSurfacesConflict(t *testing.T) {
	resetControlsAuth(t)
	api := &remapAPI{status: http.StatusConflict, response: map[string]string{"error": "proposal already decided (status kept_legacy)"}}
	srv := api.server(t)
	_, err := runImportCmd(t, "--endpoint", srv.URL, "--token", "tok-1", "catalog", "remap", "reject", "prop-4")
	if err == nil || !strings.Contains(err.Error(), "HTTP 409: proposal already decided") {
		t.Fatalf("err = %v; want the API's 409", err)
	}
}
