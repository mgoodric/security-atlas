//go:build integration

// Integration tests for the Statement of Applicability export (issue #1791,
// spec #1744): CSV and xlsx carry every requirement of the fixture with its
// decision, justification, implementation reference, linked controls,
// contradiction flag, scope revision and approval; the xlsx is titled
// "Statement of Applicability" for ISO/IEC 27001; ?revision= and ?as_of=
// pick the revision exported.

package frameworkscope_test

import (
	"bytes"
	"context"
	"encoding/csv"
	"fmt"
	"net/http"
	"net/url"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/mgoodric/security-atlas/internal/dbtest"
	"github.com/mgoodric/security-atlas/internal/frameworkscope"
	"github.com/mgoodric/security-atlas/internal/xlsxbound"
)

// formulaRef is R-07's implementation reference: a cell a spreadsheet would
// run as a formula unless the export neutralises it.
const formulaRef = `=HYPERLINK("https://evil.example","click")`

// soaExportSetup seeds the fixture under slug/name, records its decisions
// (R-07's implementation reference is formulaRef), and approves and
// activates the revision effective an hour ago.
func soaExportSetup(t *testing.T, slug, name string) (admin *pgxpool.Pool, f soaFixture, c soaClient, scopeID string) {
	t.Helper()
	admin = dbtest.NewMigratePool(t)
	tenant := soaTenant(t, admin)
	f = seedSoAFixtureAs(t, admin, tenant, slug, name)
	ts, bearer, approver := setupHTTPServer(t, tenant)
	c = soaClient{t: t, base: ts.URL, bearer: bearer}
	scopeID = c.createScope(f.FrameworkVersionID, "ISMS")
	applyFixtureDecisions(c, f, scopeID)
	c.mustSet(scopeID, f.Req["R-07"], fmt.Sprintf(`{"decision":"applicable","implementation_ref":%q}`, formulaRef))
	activateScope(c, scopeID, approver, time.Now().UTC().Add(-time.Hour))
	return admin, f, c, scopeID
}

func activateScope(c soaClient, scopeID, approver string, at time.Time) {
	c.t.Helper()
	c.transition(scopeID, "submit", c.bearer, "")
	c.transition(scopeID, "approve", approver, "")
	c.transition(scopeID, "activate", c.bearer, fmt.Sprintf(`{"effective_from":%q}`, at.Format(time.RFC3339)))
}

// expectedSoARows is the fixture's export, by requirement code, before any
// formula neutralisation: the decisions applyFixtureDecisions records plus
// R-07's formulaRef, the controls the crosswalk puts behind each
// requirement, R-01 contradicted by passing C1, and the revision's approval.
func expectedSoARows(t *testing.T, admin *pgxpool.Pool, f soaFixture, scopeID string) [][]string {
	t.Helper()
	ctx := context.Background()
	var approver, approvedAt string
	var at time.Time
	if err := admin.QueryRow(ctx, `SELECT l.actor, s.approved_at
		FROM framework_scopes s
		JOIN framework_scope_workflow_log l ON l.framework_scope_id = s.id AND l.action = 'approve'
		WHERE s.id = $1`, scopeID).Scan(&approver, &at); err != nil {
		t.Fatalf("read approval: %v", err)
	}
	if approver == "" {
		t.Fatal("approve actor is empty")
	}
	approvedAt = at.UTC().Format(time.RFC3339)
	bundle := map[string]string{}
	for name, id := range f.Controls {
		var b string
		if err := admin.QueryRow(ctx, `SELECT bundle_id FROM controls WHERE id = $1`, id).Scan(&b); err != nil {
			t.Fatalf("read bundle_id: %v", err)
		}
		bundle[name] = b
	}
	const wiki = "https://wiki.example/isms/A"
	row := func(code, decision, justification, ref, controls, contradiction string) []string {
		return []string{code, "requirement " + code, decision, justification, ref, controls, contradiction, scopeID, approver, approvedAt}
	}
	return [][]string{
		row("R-01", "excluded", "No in-house software development", "", bundle["C1"], "true"),
		row("R-02", "excluded", "No cardholder data processed", "", "", "false"),
		row("R-03", "undecided", "", "", "", "false"),
		row("R-04", "applicable", "", wiki, bundle["C2"], "false"),
		row("R-05", "applicable", "", wiki, bundle["C3"], "false"),
		row("R-06", "applicable", "", wiki, bundle["C1"], "false"),
		row("R-07", "applicable", "", formulaRef, "", "false"),
		row("R-08", "applicable", "", wiki, "", "false"),
	}
}

func exportURL(c soaClient, fvID uuid.UUID, format string, extra url.Values) string {
	q := url.Values{"framework_version": {fvID.String()}, "format": {format}}
	for k, v := range extra {
		q[k] = v
	}
	return c.base + "/v1/framework-scopes/applicability/export?" + q.Encode()
}

func fetchCSV(t *testing.T, c soaClient, fvID uuid.UUID, extra url.Values) (*http.Response, [][]string) {
	t.Helper()
	resp, body := doJSON(t, http.MethodGet, exportURL(c, fvID, "csv", extra), c.bearer, "")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("csv export %v: status %d; body %s", extra, resp.StatusCode, body)
	}
	records, err := csv.NewReader(bytes.NewReader(body)).ReadAll()
	if err != nil {
		t.Fatalf("parse csv: %v\n%s", err, body)
	}
	if len(records) == 0 || !reflect.DeepEqual(records[0], frameworkscope.SoAHeader) {
		t.Fatalf("csv header = %v, want %v", records, frameworkscope.SoAHeader)
	}
	return resp, records[1:]
}

func fetchXLSX(t *testing.T, c soaClient, fvID uuid.UUID) (sheet string, rows [][]string, formula string) {
	t.Helper()
	resp, body := doJSON(t, http.MethodGet, exportURL(c, fvID, "xlsx", nil), c.bearer, "")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("xlsx export: status %d; body %s", resp.StatusCode, body)
	}
	if ct := resp.Header.Get("Content-Type"); ct != "application/vnd.openxmlformats-officedocument.spreadsheetml.sheet" {
		t.Fatalf("xlsx content type = %q", ct)
	}
	lim := xlsxbound.Limits{MaxDecompressedBytes: 8 << 20, MaxXMLPartBytes: 8 << 20, MaxRows: 100, MaxColumns: 20}
	wb, err := xlsxbound.Open(body, lim)
	if err != nil {
		t.Fatalf("open xlsx: %v", err)
	}
	defer func() { _ = wb.Close() }()
	sheets := wb.GetSheetList()
	if len(sheets) != 1 {
		t.Fatalf("sheets = %v, want one", sheets)
	}
	if rows, err = xlsxbound.ReadRows(wb, sheets[0], lim); err != nil {
		t.Fatalf("read rows: %v", err)
	}
	// R-07's implementation reference: data starts after the 4-row title
	// block and the header, R-07 is the seventh data row, column E.
	if formula, err = wb.GetCellFormula(sheets[0], "E12"); err != nil {
		t.Fatalf("read formula: %v", err)
	}
	return sheets[0], rows, formula
}

// The CSV has the header and one row per requirement, matching the fixture
// field for field; the formula-looking cell is neutralised.
func TestHTTP_SoAExport_CSV(t *testing.T) {
	admin, f, c, scopeID := soaExportSetup(t, "", "SoA test framework (#1791)")

	resp, got := fetchCSV(t, c, f.FrameworkVersionID, nil)
	if ct := resp.Header.Get("Content-Type"); !strings.HasPrefix(ct, "text/csv") {
		t.Fatalf("content type = %q, want text/csv", ct)
	}
	if cd := resp.Header.Get("Content-Disposition"); !strings.HasPrefix(cd, `attachment; filename="applicability-soa-1787-`) ||
		!strings.HasSuffix(cd, `-2022.csv"`) {
		t.Fatalf("content disposition = %q", cd)
	}
	want := expectedSoARows(t, admin, f, scopeID)
	want[6][4] = "'" + formulaRef
	if len(got) != len(f.Codes) {
		t.Fatalf("csv rows = %d, want %d (every requirement)", len(got), len(f.Codes))
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("csv rows:\n got %q\nwant %q", got, want)
	}
}

// The xlsx carries the same table under the title block. ISO/IEC 27001 is
// titled "Statement of Applicability"; other frameworks "Applicability".
func TestHTTP_SoAExport_XLSX_ISOTitle(t *testing.T) {
	admin, f, c, scopeID := soaExportSetup(t, "iso27001", "ISO/IEC 27001")

	sheet, rows, formula := fetchXLSX(t, c, f.FrameworkVersionID)
	if sheet != "Statement of Applicability" {
		t.Fatalf("sheet = %q, want Statement of Applicability", sheet)
	}
	if len(rows) < 5 {
		t.Fatalf("xlsx rows = %q", rows)
	}
	if rows[0][0] != "Statement of Applicability" || rows[1][0] != "ISO/IEC 27001 2022" {
		t.Fatalf("title block = %q / %q", rows[0], rows[1])
	}
	if !strings.Contains(rows[2][0], scopeID) || !strings.Contains(rows[3][0], "undecided: 1") ||
		!strings.Contains(rows[3][0], "contradictions: 1") {
		t.Fatalf("revision/counts lines = %q / %q", rows[2], rows[3])
	}
	if !reflect.DeepEqual(rows[4], frameworkscope.SoAHeader) {
		t.Fatalf("xlsx header = %q", rows[4])
	}
	// xlsx cells are typed strings, so the formula text is kept verbatim and
	// never stored as a formula.
	if want := expectedSoARows(t, admin, f, scopeID); !reflect.DeepEqual(rows[5:], want) {
		t.Fatalf("xlsx rows:\n got %q\nwant %q", rows[5:], want)
	}
	if formula != "" {
		t.Fatalf("E12 holds formula %q, want plain text", formula)
	}
}

func TestHTTP_SoAExport_XLSX_OtherFrameworkTitle(t *testing.T) {
	_, f, c, _ := soaExportSetup(t, "", "SoA test framework (#1791)")
	sheet, rows, _ := fetchXLSX(t, c, f.FrameworkVersionID)
	if sheet != "Applicability" || rows[0][0] != "Applicability" || rows[1][0] != "SoA test framework (#1791) 2022" {
		t.Fatalf("sheet %q title %q / %q, want Applicability", sheet, rows[0], rows[1])
	}
}

// The activated revision is exported by default; ?revision= exports the one
// named and ?as_of= the one in force then (none: every requirement
// undecided). Bad selectors are 400 and unknown ids 404.
func TestHTTP_SoAExport_RevisionAndAsOf(t *testing.T) {
	admin := dbtest.NewMigratePool(t)
	tenant := soaTenant(t, admin)
	f := seedSoAFixture(t, admin, tenant)
	ts, bearer, approver := setupHTTPServer(t, tenant)
	c := soaClient{t: t, base: ts.URL, bearer: bearer}
	now := time.Now().UTC().Truncate(time.Second)

	// A: the fixture decisions, effective two hours ago. B: R-01 and R-03
	// applicable, effective one hour ago, superseding A. D: a draft.
	a := c.createScope(f.FrameworkVersionID, "ISMS rev A")
	applyFixtureDecisions(c, f, a)
	activateScope(c, a, approver, now.Add(-2*time.Hour))
	b := c.createScope(f.FrameworkVersionID, "ISMS rev B")
	applyFixtureDecisions(c, f, b)
	c.mustSet(b, f.Req["R-01"], applicableBody)
	c.mustSet(b, f.Req["R-03"], applicableBody)
	activateScope(c, b, approver, now.Add(-time.Hour))
	d := c.createScope(f.FrameworkVersionID, "ISMS draft")
	c.mustSet(d, f.Req["R-08"], `{"decision":"excluded","justification":"Draft only"}`)

	// summary is "<revision>|<code>=<decision>,..." for one export.
	summary := func(rows [][]string) string {
		parts := make([]string, len(rows))
		rev := ""
		for i, r := range rows {
			parts[i] = r[0] + "=" + r[2]
			if r[6] == "true" {
				parts[i] += "!"
			}
			rev = r[7]
		}
		return rev + "|" + strings.Join(parts, ",")
	}
	revA := a + "|R-01=excluded!,R-02=excluded,R-03=undecided,R-04=applicable,R-05=applicable,R-06=applicable,R-07=applicable,R-08=applicable"
	revB := b + "|R-01=applicable,R-02=excluded,R-03=applicable,R-04=applicable,R-05=applicable,R-06=applicable,R-07=applicable,R-08=applicable"
	revD := d + "|R-01=undecided,R-02=undecided,R-03=undecided,R-04=undecided,R-05=undecided,R-06=undecided,R-07=undecided,R-08=excluded"
	none := "|R-01=undecided,R-02=undecided,R-03=undecided,R-04=undecided,R-05=undecided,R-06=undecided,R-07=undecided,R-08=undecided"

	for _, tc := range []struct {
		name  string
		extra url.Values
		want  string
	}{
		{"default is the activated revision", nil, revB},
		{"revision A", url.Values{"revision": {a}}, revA},
		{"revision B", url.Values{"revision": {b}}, revB},
		{"draft revision", url.Values{"revision": {d}}, revD},
		{"as_of while A was in force", url.Values{"as_of": {now.Add(-90 * time.Minute).Format(time.RFC3339)}}, revA},
		{"as_of now", url.Values{"as_of": {now.Format(time.RFC3339)}}, revB},
		{"as_of before any revision", url.Values{"as_of": {now.Add(-3 * time.Hour).Format(time.RFC3339)}}, none},
	} {
		_, rows := fetchCSV(t, c, f.FrameworkVersionID, tc.extra)
		if got := summary(rows); got != tc.want {
			t.Fatalf("%s:\n got %s\nwant %s", tc.name, got, tc.want)
		}
	}

	for _, tc := range []struct {
		name string
		url  string
		want int
	}{
		{"revision and as_of", exportURL(c, f.FrameworkVersionID, "csv", url.Values{"revision": {a}, "as_of": {now.Format(time.RFC3339)}}), http.StatusBadRequest},
		{"bad format", exportURL(c, f.FrameworkVersionID, "pdf", nil), http.StatusBadRequest},
		{"bad revision", exportURL(c, f.FrameworkVersionID, "csv", url.Values{"revision": {"nope"}}), http.StatusBadRequest},
		{"bad as_of", exportURL(c, f.FrameworkVersionID, "csv", url.Values{"as_of": {"yesterday"}}), http.StatusBadRequest},
		{"unknown revision", exportURL(c, f.FrameworkVersionID, "csv", url.Values{"revision": {uuid.NewString()}}), http.StatusNotFound},
		{"unknown framework version", exportURL(c, uuid.New(), "csv", nil), http.StatusNotFound},
	} {
		if resp, body := doJSON(t, http.MethodGet, tc.url, c.bearer, ""); resp.StatusCode != tc.want {
			t.Fatalf("%s: status %d, want %d; body %s", tc.name, resp.StatusCode, tc.want, body)
		}
	}
}
