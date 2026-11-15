package main

import (
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// #1799: `mappings overlay import | export`. runOverlay is in
// cmd_mappings_test.go.

// rawAPI records a non-JSON request and replies with a fixed body.
type rawAPI struct {
	method, path, query, auth string
	body                      string
}

func (a *rawAPI) server(t *testing.T, status int, contentType, resp string) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		a.method, a.path, a.query, a.auth = r.Method, r.URL.Path, r.URL.RawQuery, r.Header.Get("Authorization")
		raw, _ := io.ReadAll(r.Body)
		a.body = string(raw)
		w.Header().Set("Content-Type", contentType)
		w.WriteHeader(status)
		_, _ = io.WriteString(w, resp)
	}))
	t.Cleanup(srv.Close)
	return srv
}

func writeTemp(t *testing.T, name, content string) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), name)
	if err := os.WriteFile(p, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	return p
}

const overlayCSV = "framework,requirement_code,anchor_code,relationship_type,strength,rationale,source_ref\n" +
	"soc2,CC1.1,HRS-07.1,equal,1,r,s\n"

func TestMappingsOverlayImport_SendsFileAndPrintsResult(t *testing.T) {
	api := &rawAPI{}
	srv := api.server(t, http.StatusCreated, "application/json", `{"written":true,"partial":false,
		"created":[{"row":1,"overlay_edge":{"id":"e-1","framework":"soc2","framework_version":"2017",
		"requirement_code":"CC1.1","scf_release":"2025.2","anchor_code":"HRS-07.1"}}],"rejected":[]}`)

	out, err := runOverlay(t, srv, "import", writeTemp(t, "edges.csv", overlayCSV))
	if err != nil {
		t.Fatalf("import: %v\n%s", err, out)
	}
	if api.method != http.MethodPost || api.path != "/v1/mapping-overlays/import" || api.query != "format=csv" ||
		api.auth != "Bearer tok" || api.body != overlayCSV {
		t.Errorf("request %s %s?%s auth=%q body=%q", api.method, api.path, api.query, api.auth, api.body)
	}
	for _, want := range []string{
		"row 1: added e-1: soc2:2017:CC1.1 -> SCF 2025.2:HRS-07.1",
		"imported 1 overlay edge(s), 0 row(s) rejected",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("output missing %q:\n%s", want, out)
		}
	}
}

func TestMappingsOverlayImport_PartialAndYAMLByExtension(t *testing.T) {
	api := &rawAPI{}
	srv := api.server(t, http.StatusCreated, "application/json", `{"written":true,"partial":true,"created":[],
		"rejected":[{"row":2,"line":9,"code":"unknown_anchor","reason":"the SCF release has no anchor with this code"}]}`)

	out, err := runOverlay(t, srv, "import", writeTemp(t, "edges.yml", "- framework: soc2\n"), "--partial")
	if err != nil {
		t.Fatalf("import: %v\n%s", err, out)
	}
	if api.query != "format=yaml&partial=true" {
		t.Errorf("query = %q", api.query)
	}
	if !strings.Contains(out, "row 2 (line 9): rejected: unknown_anchor: the SCF release has no anchor with this code") {
		t.Errorf("output:\n%s", out)
	}
}

func TestMappingsOverlayImport_NothingWrittenIsAnError(t *testing.T) {
	api := &rawAPI{}
	srv := api.server(t, http.StatusUnprocessableEntity, "application/json", `{"written":false,"created":[],
		"rejected":[{"row":1,"line":2,"code":"unknown_requirement","reason":"the framework version has no requirement with this code"}]}`)

	out, err := runOverlay(t, srv, "import", writeTemp(t, "edges.csv", overlayCSV))
	if err == nil || !strings.Contains(err.Error(), "nothing imported: 1 row(s) rejected") {
		t.Errorf("err = %v", err)
	}
	if !strings.Contains(out, "row 1 (line 2): rejected: unknown_requirement") {
		t.Errorf("output:\n%s", out)
	}
}

func TestMappingsOverlayImport_RefusalsBeforeAndFromTheAPI(t *testing.T) {
	api := &rawAPI{}
	srv := api.server(t, http.StatusBadRequest, "application/json",
		`{"error":"mapping overlay import: the file names a tenant; the tenant comes from the credential","code":"invalid_import_file"}`)

	if _, err := runOverlay(t, srv, "import", writeTemp(t, "edges.txt", overlayCSV)); err == nil || !strings.Contains(err.Error(), "--format") {
		t.Errorf("unknown extension: err = %v", err)
	}
	if _, err := runOverlay(t, srv, "import", writeTemp(t, "edges.csv", overlayCSV), "--format", "xlsx"); err == nil ||
		!strings.Contains(err.Error(), "--format must be csv or yaml") {
		t.Errorf("bad format: err = %v", err)
	}
	big := writeTemp(t, "big.csv", strings.Repeat("x", maxOverlayFileBytes+1))
	if _, err := runOverlay(t, srv, "import", big); err == nil || !strings.Contains(err.Error(), "4 MiB") {
		t.Errorf("oversized: err = %v", err)
	}
	if api.method != "" {
		t.Errorf("a refused import still called the API: %s %s", api.method, api.path)
	}

	_, err := runOverlay(t, srv, "import", writeTemp(t, "edges.txt", overlayCSV), "--format", "csv")
	if err == nil || !strings.Contains(err.Error(), "HTTP 400") || !strings.Contains(err.Error(), "the file names a tenant") {
		t.Errorf("API refusal: err = %v", err)
	}
}

func TestMappingsOverlayExport_WritesCSV(t *testing.T) {
	const csvBody = "id,framework\ne-1,soc2\n"
	api := &rawAPI{}
	srv := api.server(t, http.StatusOK, "text/csv; charset=utf-8", csvBody)

	out, err := runOverlay(t, srv, "export", "--status", "active", "--framework", "soc2")
	if err != nil {
		t.Fatalf("export: %v\n%s", err, out)
	}
	if api.method != http.MethodGet || api.path != "/v1/mapping-overlays/export" || api.query != "framework=soc2&status=active" {
		t.Errorf("request %s %s?%s", api.method, api.path, api.query)
	}
	if out != csvBody {
		t.Errorf("stdout = %q; want %q", out, csvBody)
	}

	dst := filepath.Join(t.TempDir(), "out.csv")
	if _, err := runOverlay(t, srv, "export", "-o", dst); err != nil {
		t.Fatalf("export -o: %v", err)
	}
	if got, _ := os.ReadFile(dst); string(got) != csvBody {
		t.Errorf("file = %q", got)
	}
	if api.query != "" {
		t.Errorf("unfiltered export query = %q", api.query)
	}
}

func TestMappingsOverlayExport_SurfacesAPIRefusal(t *testing.T) {
	api := &rawAPI{}
	srv := api.server(t, http.StatusBadRequest, "application/json",
		`{"error":"mapping overlay: invalid overlay edge: status must be active or retired","code":"invalid_overlay_edge"}`)
	_, err := runOverlay(t, srv, "export", "--status", "gone")
	if err == nil || !strings.Contains(err.Error(), "HTTP 400") || !strings.Contains(err.Error(), "status must be active or retired") {
		t.Errorf("err = %v", err)
	}
}
