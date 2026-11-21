package main

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/mgoodric/security-atlas/internal/risk/riskimport"
)

// fakeRiskImportAPI stands in for POST /v1/risks/imports and records the
// multipart request it received.
type fakeRiskImportAPI struct {
	gotAuth      string
	gotFile      string
	gotFileBytes string
	gotColumnMap []string
}

func (f *fakeRiskImportAPI) server(t *testing.T, status int, body any) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.URL.Path != "/v1/risks/imports" {
			http.Error(w, "unexpected "+r.Method+" "+r.URL.Path, http.StatusNotFound)
			return
		}
		if err := r.ParseMultipartForm(1 << 20); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		f.gotAuth = r.Header.Get("Authorization")
		f.gotColumnMap = r.MultipartForm.Value["column_map"]
		file, hdr, err := r.FormFile("file")
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		defer file.Close()
		raw, _ := io.ReadAll(file)
		f.gotFile, f.gotFileBytes = hdr.Filename, string(raw)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(status)
		_ = json.NewEncoder(w).Encode(body)
	}))
	t.Cleanup(srv.Close)
	return srv
}

func TestRisksImport_PrintsSummaryFromAPI(t *testing.T) {
	resetControlsAuth(t)
	api := &fakeRiskImportAPI{}
	srv := api.server(t, http.StatusCreated, riskimport.Batch{
		ID:              "batch-1",
		SourceFilename:  "register.csv",
		Status:          riskimport.BatchStatusStaged,
		TotalRows:       4,
		StagedCount:     2,
		RejectedCount:   2,
		UnmappedColumns: []string{"Risk Owner", "Target Date"},
		Rejected: []riskimport.RejectedRow{
			{RowNumber: 4, ExternalRef: "R-3", Reason: riskimport.RejectMissingField, Detail: "missing title"},
			{RowNumber: 5, ExternalRef: "r-1", Reason: riskimport.RejectDuplicateExternal, Detail: `risk ID "r-1" already appears on row 2`},
		},
	})
	sheet := writeSheet(t, "register.csv", "Ref No.,Title,Category\nR-1,Outage,operational\n")

	out, err := runImportCmd(t, "--endpoint", srv.URL, "--token", "tok-2839",
		"risks", "import", sheet, "--column-map", "external_ref=Ref No.", "--column-map", "title=Title")
	if err != nil {
		t.Fatalf("risks import: %v\n%s", err, out)
	}
	if api.gotAuth != "Bearer tok-2839" || api.gotFile != "register.csv" || !strings.HasPrefix(api.gotFileBytes, "Ref No.,Title") {
		t.Errorf("request auth=%q file=%q body=%q", api.gotAuth, api.gotFile, api.gotFileBytes)
	}
	if strings.Join(api.gotColumnMap, "|") != "external_ref=Ref No.|title=Title" {
		t.Errorf("column_map = %v", api.gotColumnMap)
	}
	for _, want := range []string{
		"risk import batch batch-1 (register.csv)",
		"rows:     4",
		"staged:   2",
		"rejected: 2",
		"unmapped columns (kept on the staged rows): Risk Owner, Target Date",
		"row 4  R-3  missing_required_field: missing title",
		"row 5  r-1  duplicate_external_ref:",
		"/v1/risks/imports/batch-1/approve",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("output missing %q:\n%s", want, out)
		}
	}
}

func TestRisksImport_ReportsAPIError(t *testing.T) {
	resetControlsAuth(t)
	api := &fakeRiskImportAPI{}
	srv := api.server(t, http.StatusForbidden, map[string]string{"error": "forbidden"})
	sheet := writeSheet(t, "register.csv", "Title,Category\nOutage,operational\n")
	out, err := runImportCmd(t, "--endpoint", srv.URL, "--token", "tok", "risks", "import", sheet)
	if err == nil || !strings.Contains(err.Error(), "HTTP 403: forbidden") {
		t.Fatalf("err = %v, want the API's 403\n%s", err, out)
	}
}

func TestRisksImport_RefusesUnsupportedFile(t *testing.T) {
	resetControlsAuth(t)
	sheet := writeSheet(t, "register.txt", "Title,Category\n")
	_, err := runImportCmd(t, "--endpoint", "http://127.0.0.1:1", "--token", "tok", "risks", "import", sheet)
	if err == nil || !strings.Contains(err.Error(), ".csv or .xlsx") {
		t.Fatalf("err = %v, want the format refusal", err)
	}
}
