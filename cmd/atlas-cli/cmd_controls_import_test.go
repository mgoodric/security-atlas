package main

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mgoodric/security-atlas/internal/controlimport"
)

// fakeImportAPI stands in for POST /v1/controls/imports. It records the
// multipart request it received and answers with status and body.
type fakeImportAPI struct {
	gotAuth      string
	gotFile      string
	gotFileBytes string
	gotFramework string
	gotColumnMap []string
}

func (f *fakeImportAPI) server(t *testing.T, status int, body any) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.URL.Path != "/v1/controls/imports" {
			http.Error(w, "unexpected "+r.Method+" "+r.URL.Path, http.StatusNotFound)
			return
		}
		if err := r.ParseMultipartForm(1 << 20); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		f.gotAuth = r.Header.Get("Authorization")
		f.gotFramework = r.FormValue("framework")
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

func writeSheet(t *testing.T, name, content string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), name)
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatalf("write sheet: %v", err)
	}
	return path
}

func runImportCmd(t *testing.T, args ...string) (string, error) {
	t.Helper()
	cmd := newRootCmd()
	var out bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetErr(&out)
	cmd.SetArgs(args)
	err := cmd.Execute()
	return out.String(), err
}

func TestControlsImport_PrintsSummaryFromAPI(t *testing.T) {
	resetControlsAuth(t)
	api := &fakeImportAPI{}
	srv := api.server(t, http.StatusCreated, controlimport.Batch{
		ID:              "batch-1",
		Framework:       controlimport.FrameworkRef{Slug: "soc2", Version: "2017"},
		SourceFilename:  "controls.csv",
		Status:          controlimport.BatchStatusStaged,
		TotalRows:       5,
		StagedCount:     2,
		RejectedCount:   3,
		UnmappedColumns: []string{"Owner"},
		Rejected: []controlimport.RejectedRow{
			{RowNumber: 4, ExternalID: "AC-3", Reason: controlimport.RejectMissingField, Detail: "missing title"},
			{RowNumber: 5, ExternalID: "AC-4", Reason: controlimport.RejectUnknownTestedUnder, Detail: "CC9.9 is not in soc2 2017"},
			{RowNumber: 6, ExternalID: "AC-1", Reason: controlimport.RejectDuplicateID, Detail: `control ID "AC-1" already appears on row 2`},
		},
	})
	sheet := writeSheet(t, "controls.csv", "Control ID,Title,Description\nAC-1,MFA,Users use MFA\n")

	out, err := runImportCmd(t, "--endpoint", srv.URL, "--token", "tok-123",
		"controls", "import", sheet, "--framework", "soc2",
		"--column-map", "id=Ref No.", "--column-map", "tested_under=Audit Mapping")
	if err != nil {
		t.Fatalf("controls import: %v\n%s", err, out)
	}

	if api.gotAuth != "Bearer tok-123" || api.gotFramework != "soc2" || api.gotFile != "controls.csv" {
		t.Errorf("request auth=%q framework=%q file=%q", api.gotAuth, api.gotFramework, api.gotFile)
	}
	if !strings.HasPrefix(api.gotFileBytes, "Control ID,Title") {
		t.Errorf("uploaded file body = %q", api.gotFileBytes)
	}
	if strings.Join(api.gotColumnMap, "|") != "id=Ref No.|tested_under=Audit Mapping" {
		t.Errorf("column_map = %v", api.gotColumnMap)
	}
	for _, want := range []string{
		"import batch batch-1 (controls.csv, framework soc2 2017)",
		"rows:     5",
		"staged:   2",
		"rejected: 3",
		"ignored columns: Owner",
		"row 4  AC-3  missing_required_field: missing title",
		"row 5  AC-4  unknown_tested_under: CC9.9 is not in soc2 2017",
		"row 6  AC-1  duplicate_id:",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("output missing %q:\n%s", want, out)
		}
	}
}

func TestControlsImport_SurfacesAPIError(t *testing.T) {
	resetControlsAuth(t)
	api := &fakeImportAPI{}
	srv := api.server(t, http.StatusBadRequest, map[string]string{"error": "control file exceeds the 5000-row cap; split it into smaller files"})
	sheet := writeSheet(t, "controls.csv", "Control ID,Title,Description\n")

	_, err := runImportCmd(t, "--endpoint", srv.URL, "--token", "tok",
		"controls", "import", sheet, "--framework", "soc2")
	if err == nil || !strings.Contains(err.Error(), "HTTP 400") || !strings.Contains(err.Error(), "row cap") {
		t.Fatalf("err = %v, want the API's row-cap message", err)
	}
}

func TestControlsImport_RequiresFrameworkAndSheetFormat(t *testing.T) {
	resetControlsAuth(t)
	csvSheet := writeSheet(t, "controls.csv", "x")
	pdf := writeSheet(t, "controls.pdf", "x")

	if _, err := runImportCmd(t, "--endpoint", "http://atlas", "--token", "tok", "controls", "import", csvSheet); err == nil ||
		!strings.Contains(err.Error(), "--framework") {
		t.Errorf("missing framework err = %v", err)
	}
	if _, err := runImportCmd(t, "--endpoint", "http://atlas", "--token", "tok", "controls", "import", pdf, "--framework", "soc2"); err == nil ||
		!strings.Contains(err.Error(), ".csv or .xlsx") {
		t.Errorf("pdf err = %v", err)
	}
}
