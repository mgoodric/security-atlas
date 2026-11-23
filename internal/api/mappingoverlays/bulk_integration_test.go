//go:build integration

// #1799 — overlay edges: bulk import (CSV and YAML) and CSV export (API).
// Uses the #1797 fixture from integration_test.go.

package mappingoverlays_test

import (
	"bytes"
	"context"
	"encoding/csv"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"slices"
	"strings"
	"testing"

	"github.com/mgoodric/security-atlas/internal/mappingoverlay"
	"github.com/mgoodric/security-atlas/internal/scfrelease/scfreleasetest"
)

// raw sends body as-is and returns the status, the response bytes and its
// Content-Type.
func raw(t *testing.T, e env, method, path, bearer, contentType string, body []byte) (int, []byte, string) {
	t.Helper()
	req, _ := http.NewRequest(method, e.url+path, bytes.NewReader(body))
	if contentType != "" {
		req.Header.Set("Content-Type", contentType)
	}
	req.Header.Set("Authorization", "Bearer "+bearer)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("%s %s: %v", method, path, err)
	}
	defer func() { _ = resp.Body.Close() }()
	b, _ := io.ReadAll(resp.Body)
	return resp.StatusCode, b, resp.Header.Get("Content-Type")
}

type importResp struct {
	Written bool `json:"written"`
	Partial bool `json:"partial"`
	Created []struct {
		Row         int            `json:"row"`
		OverlayEdge map[string]any `json:"overlay_edge"`
	} `json:"created"`
	Rejected []struct {
		Row    int    `json:"row"`
		Line   int    `json:"line"`
		Code   string `json:"code"`
		Reason string `json:"reason"`
	} `json:"rejected"`
	Error string `json:"error"`
	Code  string `json:"code"`
}

func importCSV(t *testing.T, e env, query, body string) (int, importResp) {
	t.Helper()
	status, b, _ := raw(t, e, http.MethodPost, "/v1/mapping-overlays/import"+query, e.admin, "text/csv", []byte(body))
	var out importResp
	if err := json.Unmarshal(b, &out); err != nil {
		t.Fatalf("decode import response (%d): %v: %s", status, err, b)
	}
	return status, out
}

const importHeader = "framework,framework_version,requirement_code,scf_release,anchor_code,relationship_type,strength,rationale,source_ref\n"

// csvRow is one import row for requirement -> anchor in release B.
func (f fixture) csvRow(code, anchor string) string {
	return fmt.Sprintf("%s,%s,%s,%s,%s,intersects_with,0.8,auditor tested it under %s,\"FY25 report, %s\"\n",
		f.framework, f.version, code, scfreleasetest.ReleaseB, anchor, code, code)
}

func auditCreates(t *testing.T, f fixture, tenant string) int {
	t.Helper()
	var n int
	if err := f.admin.QueryRow(context.Background(), `
		SELECT count(*) FROM fw_to_scf_overlay_edge_audit_log WHERE tenant_id = $1 AND action = 'create'`,
		tenant).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}

func TestOverlayImport_CleanCSVCreatesEveryEdge(t *testing.T) {
	f := newFixture(t)
	body := importHeader + f.csvRow("C2", "IAC-06") + f.csvRow("C3", "CRY-03") + f.csvRow("C2", "CRY-03")

	status, res := importCSV(t, f.tenantA, "", body)
	if status != http.StatusCreated || !res.Written || len(res.Created) != 3 || len(res.Rejected) != 0 {
		t.Fatalf("import: status %d %+v", status, res)
	}
	for i, c := range res.Created {
		if c.Row != i+1 || c.OverlayEdge["status"] != "active" || c.OverlayEdge["created_by"] == "" {
			t.Errorf("created[%d] = %+v", i, c)
		}
	}
	got := list(t, f.tenantA, "")
	if len(got) != 3 {
		t.Fatalf("list after import: %d edges; want 3", len(got))
	}
	pairs := map[string]bool{}
	for _, e := range got {
		pairs[e["requirement_code"].(string)+"->"+e["anchor_code"].(string)] = true
		if e["source_ref"] != "FY25 report, "+e["requirement_code"].(string) {
			t.Errorf("source_ref = %v", e["source_ref"])
		}
	}
	for _, p := range []string{"C2->IAC-06", "C3->CRY-03", "C2->CRY-03"} {
		if !pairs[p] {
			t.Errorf("import did not create %s (got %v)", p, pairs)
		}
	}
	if n := auditCreates(t, f, f.tenantA.tenant); n != 3 {
		t.Errorf("audit rows = %d; want one per edge (3)", n)
	}
	if got := list(t, f.tenantB, ""); len(got) != 0 {
		t.Errorf("tenant B sees tenant A's imported edges: %v", got)
	}
}

func TestOverlayImport_CleanYAMLCreatesEveryEdge(t *testing.T) {
	f := newFixture(t)
	body := fmt.Sprintf(`
- framework: %[1]s
  framework_version: "%[2]s"
  requirement_code: C2
  scf_release: %[3]s
  anchor_code: IAC-06
  relationship_type: equal
  strength: 1
  rationale: auditor tested it under C2
  source_ref: FY25 report s4
- framework: %[1]s
  requirement_code: C3
  anchor_code: CRY-03
  relationship_type: subset_of
  strength: 0.5
  rationale: auditor tested it under C3
  source_ref: FY25 report s5
`, f.framework, f.version, scfreleasetest.ReleaseB)
	status, b, _ := raw(t, f.tenantA, http.MethodPost, "/v1/mapping-overlays/import?format=yaml", f.tenantA.admin, "", []byte(body))
	var res importResp
	_ = json.Unmarshal(b, &res)
	if status != http.StatusCreated || len(res.Created) != 2 {
		t.Fatalf("yaml import: status %d %s", status, b)
	}
	// Left out, framework_version and scf_release mean current, as on a
	// single create.
	if e := res.Created[1].OverlayEdge; e["scf_release"] != scfreleasetest.ReleaseB || e["framework_version"] != f.version {
		t.Errorf("defaulted row = %v", e)
	}
}

func TestOverlayImport_OneBadRowWritesNothingUnlessPartial(t *testing.T) {
	f := newFixture(t)
	body := importHeader +
		f.csvRow("C2", "IAC-06") +
		f.csvRow("C9", "IAC-06") + // no such requirement
		f.csvRow("C1", "IAC-06") + // an active catalog edge already joins it
		f.csvRow("C2", "IAC-06") + // repeats row 1
		f.csvRow("C3", "CRY-03")

	status, res := importCSV(t, f.tenantA, "", body)
	if status != http.StatusUnprocessableEntity || res.Written || len(res.Created) != 0 {
		t.Fatalf("all-or-nothing import: status %d %+v", status, res)
	}
	want := []struct {
		row, line int
		code      string
	}{
		{2, 3, "unknown_requirement"},
		{3, 4, "catalog_edge_exists"},
		{4, 5, "overlay_edge_exists"},
	}
	if len(res.Rejected) != len(want) {
		t.Fatalf("rejected = %+v; want %d rows", res.Rejected, len(want))
	}
	for i, w := range want {
		r := res.Rejected[i]
		if r.Row != w.row || r.Line != w.line || r.Code != w.code || r.Reason == "" {
			t.Errorf("rejected[%d] = %+v; want row %d line %d code %s", i, r, w.row, w.line, w.code)
		}
		if strings.Contains(r.Reason, "C9") || strings.Contains(r.Reason, f.framework) {
			t.Errorf("reason echoes the file's cells: %q", r.Reason)
		}
	}
	if got := list(t, f.tenantA, ""); len(got) != 0 {
		t.Fatalf("a refused import wrote %d edges", len(got))
	}
	if n := auditCreates(t, f, f.tenantA.tenant); n != 0 {
		t.Errorf("a refused import left %d audit rows", n)
	}

	status, res = importCSV(t, f.tenantA, "?partial=true", body)
	if status != http.StatusCreated || !res.Written || !res.Partial || len(res.Created) != 2 || len(res.Rejected) != 3 {
		t.Fatalf("partial import: status %d %+v", status, res)
	}
	if res.Created[0].Row != 1 || res.Created[1].Row != 5 {
		t.Errorf("created rows = %d, %d; want 1, 5", res.Created[0].Row, res.Created[1].Row)
	}
	if got := list(t, f.tenantA, ""); len(got) != 2 {
		t.Errorf("partial import: %d edges listed; want 2", len(got))
	}
	if n := auditCreates(t, f, f.tenantA.tenant); n != 2 {
		t.Errorf("partial import: %d audit rows; want 2", n)
	}

	// Every row bad: nothing to write, even with partial.
	status, res = importCSV(t, f.tenantA, "?partial=true", importHeader+f.csvRow("C9", "IAC-06"))
	if status != http.StatusUnprocessableEntity || res.Written || len(res.Rejected) != 1 {
		t.Errorf("all-bad partial import: status %d %+v", status, res)
	}
}

func TestOverlayImport_RefusesFileTenantRoleAndSize(t *testing.T) {
	f := newFixture(t)
	status, res := importCSV(t, f.tenantA, "", "tenant_id,"+strings.TrimSuffix(importHeader, "\n")+"\n"+
		f.tenantB.tenant+","+f.csvRow("C2", "IAC-06"))
	if status != http.StatusBadRequest || res.Code != "invalid_import_file" || !strings.Contains(res.Error, "the tenant comes from the credential") {
		t.Errorf("tenant column: status %d %+v", status, res)
	}

	if status, b, _ := raw(t, f.tenantA, http.MethodPost, "/v1/mapping-overlays/import", f.tenantA.viewer, "text/csv",
		[]byte(importHeader+f.csvRow("C2", "IAC-06"))); status != http.StatusForbidden {
		t.Errorf("viewer import: status %d (%s)", status, b)
	}
	if status, b, _ := raw(t, f.tenantA, http.MethodPost, "/v1/mapping-overlays/import", f.tenantA.admin, "application/json",
		[]byte(`{}`)); status != http.StatusUnsupportedMediaType {
		t.Errorf("json body: status %d (%s)", status, b)
	}
	big := importHeader + strings.Repeat(f.csvRow("C2", "IAC-06"), (4<<20)/len(f.csvRow("C2", "IAC-06"))+1)
	if status, b, _ := raw(t, f.tenantA, http.MethodPost, "/v1/mapping-overlays/import", f.tenantA.admin, "text/csv",
		[]byte(big)); status != http.StatusRequestEntityTooLarge {
		t.Errorf("oversized: status %d (%s)", status, b)
	}
	if got := list(t, f.tenantA, ""); len(got) != 0 {
		t.Errorf("refused imports wrote %d edges", len(got))
	}
}

func exportCSV(t *testing.T, e env, query string) [][]string {
	t.Helper()
	status, b, ct := raw(t, e, http.MethodGet, "/v1/mapping-overlays/export"+query, e.admin, "", nil)
	if status != http.StatusOK || !strings.HasPrefix(ct, "text/csv") {
		t.Fatalf("export: status %d type %q (%s)", status, ct, b)
	}
	recs, err := csv.NewReader(bytes.NewReader(b)).ReadAll()
	if err != nil {
		t.Fatalf("export is not CSV: %v\n%s", err, b)
	}
	return recs
}

func TestOverlayExport_EveryColumnOwnTenantOnly(t *testing.T) {
	f := newFixture(t)
	formula := f.body("C2", "IAC-06")
	formula["rationale"] = "=HYPERLINK(\"https://evil.example\",\"click\")"
	_, body := call(t, f.tenantA, http.MethodPost, "/v1/mapping-overlays", f.tenantA.admin, formula)
	active := edgeOf(t, body)
	_, body = call(t, f.tenantA, http.MethodPost, "/v1/mapping-overlays", f.tenantA.admin, f.body("C3", "CRY-03"))
	retiredID := edgeOf(t, body)["id"].(string)
	if status, body := call(t, f.tenantA, http.MethodPost, "/v1/mapping-overlays/"+retiredID+"/retire", f.tenantA.admin,
		map[string]any{"reason": "auditor withdrew it"}); status != http.StatusOK {
		t.Fatalf("retire: %d %v", status, body)
	}
	bEdge := f.body("C2", "IAC-06")
	bEdge["source_ref"] = "tenant B private source"
	if status, body := call(t, f.tenantB, http.MethodPost, "/v1/mapping-overlays", f.tenantB.admin, bEdge); status != http.StatusCreated {
		t.Fatalf("tenant B create: %d %v", status, body)
	}

	recs := exportCSV(t, f.tenantA, "")
	if !slices.Equal(recs[0], mappingoverlay.ExportColumns) {
		t.Fatalf("header = %v; want %v", recs[0], mappingoverlay.ExportColumns)
	}
	if len(recs) != 3 {
		t.Fatalf("export has %d edges; want tenant A's 2", len(recs)-1)
	}
	byID := map[string]map[string]string{}
	for _, rec := range recs[1:] {
		row := map[string]string{}
		for i, c := range recs[0] {
			row[c] = rec[i]
		}
		byID[row["id"]] = row
		for _, cell := range rec {
			if strings.Contains(cell, "tenant B") {
				t.Errorf("tenant A's export carries tenant B's edge: %v", rec)
			}
		}
	}
	a := byID[active["id"].(string)]
	for col, want := range map[string]string{
		"framework": f.framework, "framework_version": f.version, "requirement_code": "C2",
		"scf_release": scfreleasetest.ReleaseB, "anchor_code": "IAC-06", "relationship_type": "intersects_with",
		"strength": "0.8", "rationale": "'" + formula["rationale"].(string), "source_ref": "FY25 SOC 2 report, section 4, C2",
		"status": "active", "retirement_reason": "", "created_by": active["created_by_name"].(string),
		"created_by_id": active["created_by"].(string), "retired_by": "", "retired_by_id": "", "retired_at": "",
	} {
		if a[col] != want {
			t.Errorf("active edge %s = %q; want %q", col, a[col], want)
		}
	}
	if a["created_at"] == "" {
		t.Error("active edge has no created_at")
	}
	r := byID[retiredID]
	if r["status"] != "retired" || r["retirement_reason"] != "auditor withdrew it" || r["retired_by"] == "" || r["retired_by_id"] == "" || r["retired_at"] == "" {
		t.Errorf("retired edge = %v", r)
	}

	if recs := exportCSV(t, f.tenantA, "?status=active"); len(recs) != 2 {
		t.Errorf("?status=active export has %d edges; want 1", len(recs)-1)
	}
	if status, b, _ := raw(t, f.tenantA, http.MethodGet, "/v1/mapping-overlays/export?status=gone", f.tenantA.admin, "", nil); status != http.StatusBadRequest {
		t.Errorf("bad status filter: %d (%s)", status, b)
	}
	recsB := exportCSV(t, f.tenantB, "")
	if len(recsB) != 2 || recsB[1][slices.Index(mappingoverlay.ExportColumns, "source_ref")] != "tenant B private source" {
		t.Errorf("tenant B export = %v", recsB)
	}
}
