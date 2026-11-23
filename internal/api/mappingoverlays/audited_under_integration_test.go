//go:build integration

// #1801 — the "audited under" report (API and CSV). Runs against a real
// Postgres with RLS, through the API servers newFixture builds on the
// non-owner app role.
//
// Fixture: newFixture (#1797): tenant A's control on IAC-06 was tested
// under C1 (reached by the catalog edge C1 -> IAC-06) and C2 (not reached).
// On top of it, catalog edges C2 -> GOV-01 in release B (current) and in
// release A (legacy), so C2 has one candidate anchor in the current release
// and one that the report must leave out; and a tenant B control on IAC-06
// tested under C2.

package mappingoverlays_test

import (
	"context"
	"encoding/csv"
	"net/http"
	"strings"
	"testing"

	"github.com/google/uuid"

	"github.com/mgoodric/security-atlas/internal/scfrelease/scfreleasetest"
)

type auditedUnderFixture struct {
	fixture
	controlA uuid.UUID
	controlB uuid.UUID
	anchorID uuid.UUID // IAC-06 in release B
}

func newAuditedUnderFixture(t *testing.T) auditedUnderFixture {
	t.Helper()
	ctx := context.Background()
	f := auditedUnderFixture{fixture: newFixture(t)}
	if err := f.admin.QueryRow(ctx, `
		SELECT a.id FROM scf_anchors a JOIN framework_versions fv ON fv.id = a.framework_version_id
		WHERE fv.version = $1 AND a.scf_id = 'IAC-06'`, scfreleasetest.ReleaseB).Scan(&f.anchorID); err != nil {
		t.Fatalf("read anchor id: %v", err)
	}
	if err := f.admin.QueryRow(ctx, `SELECT id FROM controls WHERE tenant_id = $1 AND scf_anchor_id = $2`,
		f.tenantA.tenant, f.anchorID).Scan(&f.controlA); err != nil {
		t.Fatalf("read tenant A control: %v", err)
	}
	for _, release := range []string{scfreleasetest.ReleaseB, scfreleasetest.ReleaseA} {
		if _, err := f.admin.Exec(ctx, `
			INSERT INTO fw_to_scf_edges (id, framework_requirement_id, scf_anchor_id,
				relationship_type, strength, source_attribution, mapping_tier)
			SELECT $1, $2, a.id, 'subset_of', 0.7, 'scf_official', 'verified'
			FROM scf_anchors a JOIN framework_versions fv ON fv.id = a.framework_version_id
			WHERE fv.version = $3 AND a.scf_id = 'GOV-01'`,
			uuid.New(), f.requirement["C2"], release); err != nil {
			t.Fatalf("seed catalog edge C2 -> GOV-01 (%s): %v", release, err)
		}
	}
	f.controlB = uuid.New()
	refs := `[{"system":"` + f.framework + `","kind":"tested_under","id":"C2"}]`
	if _, err := f.admin.Exec(ctx, `
		INSERT INTO controls (
			id, tenant_id, bundle_id, version, scf_id, scf_anchor_id, title, description,
			control_family, implementation_type, owner_role, lifecycle_state, applicability_expr,
			evidence_queries, manual_evidence_schema, linked_policy_ids, freshness_class,
			bundle_manifest_yaml, bundle_manifest_hash, bundle_uploaded_at, bundle_uploaded_by,
			external_refs
		)
		VALUES ($1, $2, $3, 1, 'IAC-06', $4, 'Tenant B confidentiality agreements', '',
			'human_resources', 'automated', 'security', 'active', '',
			'[]'::jsonb, NULL, ARRAY[]::TEXT[], NULL, '', '', now(), 'test', $5::jsonb)`,
		f.controlB, f.tenantB.tenant, "aud-b-"+f.framework, f.anchorID, refs); err != nil {
		t.Fatalf("seed tenant B control: %v", err)
	}
	return f
}

func reportRows(t *testing.T, e env, bearer, query string) (map[string]any, []map[string]any) {
	t.Helper()
	status, body := call(t, e, http.MethodGet, "/v1/mapping-overlays/audited-under"+query, bearer, nil)
	if status != http.StatusOK {
		t.Fatalf("audited-under%s: status %d (%v)", query, status, body)
	}
	var rows []map[string]any
	for _, r := range body["rows"].([]any) {
		rows = append(rows, r.(map[string]any))
	}
	return body, rows
}

// reportCSV fetches the export and returns its records, header first.
func reportCSV(t *testing.T, e env, query string) [][]string {
	t.Helper()
	req, _ := http.NewRequest(http.MethodGet, e.url+"/v1/mapping-overlays/audited-under/export"+query, nil)
	req.Header.Set("Authorization", "Bearer "+e.admin)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("export%s: status %d", query, resp.StatusCode)
	}
	if ct := resp.Header.Get("Content-Type"); !strings.HasPrefix(ct, "text/csv") {
		t.Fatalf("export content type %q", ct)
	}
	recs, err := csv.NewReader(resp.Body).ReadAll()
	if err != nil {
		t.Fatalf("parse export: %v", err)
	}
	return recs
}

func TestAuditedUnder_ListsTheUnreachedTestedCriterion(t *testing.T) {
	f := newAuditedUnderFixture(t)

	// An admin reads it: the report is read-only and tenant-scoped. The
	// engine refuses a viewer every mapping-overlays read today
	// (TestOverlay_ViewerRefusedByAuthz); whether it should is #3021.
	body, rows := reportRows(t, f.tenantA, f.tenantA.admin, "")
	if len(rows) != 1 {
		t.Fatalf("rows = %v; want exactly the C2 gap", rows)
	}
	row := rows[0]
	want := map[string]any{
		"control_id":         f.controlA.String(),
		"framework":          f.framework,
		"framework_version":  f.version,
		"requirement_id":     f.requirement["C2"].String(),
		"requirement_code":   "C2",
		"anchor_id":          f.anchorID.String(),
		"anchor_code":        "IAC-06",
		"anchor_scf_release": scfreleasetest.ReleaseB,
		"anchor_fit":         "exact",
		"tested_under_note":  "FY25 report s4",
	}
	for k, v := range want {
		if row[k] != v {
			t.Errorf("row[%s] = %v; want %v", k, row[k], v)
		}
	}
	if body["candidate_scf_release"] != scfreleasetest.ReleaseB {
		t.Errorf("candidate_scf_release = %v; want %s", body["candidate_scf_release"], scfreleasetest.ReleaseB)
	}
	// One candidate: GOV-01 in the current release. The release-A edge is
	// left out.
	cands := row["candidate_anchors"].([]any)
	if len(cands) != 1 {
		t.Fatalf("candidate_anchors = %v; want GOV-01 in release B only", cands)
	}
	c := cands[0].(map[string]any)
	if c["anchor_code"] != "GOV-01" || c["relationship_type"] != "subset_of" || c["strength"] != 0.7 {
		t.Errorf("candidate = %v; want GOV-01 subset_of 0.7", c)
	}
	var gov01B string
	if err := f.admin.QueryRow(context.Background(), `
		SELECT a.id::text FROM scf_anchors a JOIN framework_versions fv ON fv.id = a.framework_version_id
		WHERE fv.version = $1 AND a.scf_id = 'GOV-01'`, scfreleasetest.ReleaseB).Scan(&gov01B); err != nil {
		t.Fatal(err)
	}
	if c["anchor_id"] != gov01B {
		t.Errorf("candidate anchor_id = %v; want release B's GOV-01 %s", c["anchor_id"], gov01B)
	}
}

func TestAuditedUnder_OverlayEdgeClosesTheGap(t *testing.T) {
	f := newAuditedUnderFixture(t)

	status, body := call(t, f.tenantA, http.MethodPost, "/v1/mapping-overlays", f.tenantA.admin, f.body("C2", "IAC-06"))
	if status != http.StatusCreated {
		t.Fatalf("create overlay: status %d (%v)", status, body)
	}
	if _, rows := reportRows(t, f.tenantA, f.tenantA.admin, ""); len(rows) != 0 {
		t.Fatalf("after the overlay edge: rows = %v; want none", rows)
	}
	// B's gap is B's own: A's overlay edge does not close it.
	if _, rows := reportRows(t, f.tenantB, f.tenantB.admin, ""); len(rows) != 1 {
		t.Fatalf("tenant B after A's overlay: rows = %v; want B's C2 gap", rows)
	}

	// Retiring the edge reopens the gap at once.
	id := edgeOf(t, body)["id"].(string)
	if status, b := call(t, f.tenantA, http.MethodPost, "/v1/mapping-overlays/"+id+"/retire", f.tenantA.admin,
		map[string]any{"reason": "wrong criterion"}); status != http.StatusOK {
		t.Fatalf("retire: status %d (%v)", status, b)
	}
	if _, rows := reportRows(t, f.tenantA, f.tenantA.admin, ""); len(rows) != 1 {
		t.Fatalf("after retiring: rows = %v; want the C2 gap back", rows)
	}
}

func TestAuditedUnder_ReanchoredControlUpdatesTheReport(t *testing.T) {
	f := newAuditedUnderFixture(t)
	// Re-anchor A's control to GOV-01 (release B): C2 is now reached and C1
	// is not.
	if _, err := f.admin.Exec(context.Background(), `
		UPDATE controls SET scf_anchor_id = a.id, scf_id = a.scf_id
		FROM scf_anchors a JOIN framework_versions fv ON fv.id = a.framework_version_id
		WHERE controls.id = $1 AND fv.version = $2 AND a.scf_id = 'GOV-01'`,
		f.controlA, scfreleasetest.ReleaseB); err != nil {
		t.Fatal(err)
	}
	_, rows := reportRows(t, f.tenantA, f.tenantA.admin, "")
	if len(rows) != 1 || rows[0]["requirement_code"] != "C1" || rows[0]["anchor_code"] != "GOV-01" {
		t.Fatalf("after re-anchoring: rows = %v; want the C1 gap on GOV-01", rows)
	}
	cands := rows[0]["candidate_anchors"].([]any)
	if len(cands) != 1 || cands[0].(map[string]any)["anchor_code"] != "IAC-06" {
		t.Errorf("C1 candidates = %v; want IAC-06", cands)
	}
}

func TestAuditedUnder_ShowsAnchorFit(t *testing.T) {
	f := newAuditedUnderFixture(t)
	if _, err := f.admin.Exec(context.Background(), `
		UPDATE controls SET anchor_fit = 'none', anchor_fit_note = 'no SCF control covers board independence'
		WHERE id = $1`, f.controlA); err != nil {
		t.Fatal(err)
	}
	_, rows := reportRows(t, f.tenantA, f.tenantA.admin, "")
	if len(rows) != 1 {
		t.Fatalf("rows = %v; want one", rows)
	}
	if rows[0]["anchor_fit"] != "none" || rows[0]["anchor_fit_note"] != "no SCF control covers board independence" {
		t.Errorf("anchor_fit = %v / %v; want none with its note", rows[0]["anchor_fit"], rows[0]["anchor_fit_note"])
	}
	recs := reportCSV(t, f.tenantA, "")
	col := columnIndex(t, recs[0])
	if recs[1][col["anchor_fit"]] != "none" {
		t.Errorf("CSV anchor_fit = %q; want none", recs[1][col["anchor_fit"]])
	}
}

func TestAuditedUnder_UnanchoredControlListsEveryTestedCriterion(t *testing.T) {
	f := newAuditedUnderFixture(t)
	if _, err := f.admin.Exec(context.Background(), `
		UPDATE controls SET scf_anchor_id = NULL, anchor_fit = 'none', anchor_fit_note = 'no SCF equivalent'
		WHERE id = $1`, f.controlA); err != nil {
		t.Fatal(err)
	}
	_, rows := reportRows(t, f.tenantA, f.tenantA.admin, "")
	if len(rows) != 2 {
		t.Fatalf("rows = %v; want C1 and C2", rows)
	}
	for _, r := range rows {
		if r["anchor_id"] != nil {
			t.Errorf("anchor_id = %v; want null", r["anchor_id"])
		}
	}
}

func columnIndex(t *testing.T, header []string) map[string]int {
	t.Helper()
	col := map[string]int{}
	for i, h := range header {
		col[h] = i
	}
	return col
}

func TestAuditedUnder_CSVMatchesAPIAndFiltersAndIsolates(t *testing.T) {
	f := newAuditedUnderFixture(t)

	_, rows := reportRows(t, f.tenantA, f.tenantA.admin, "")
	recs := reportCSV(t, f.tenantA, "")
	if len(recs) != len(rows)+1 {
		t.Fatalf("CSV has %d records; want header + %d rows", len(recs), len(rows))
	}
	col := columnIndex(t, recs[0])
	for i, r := range rows {
		rec := recs[i+1]
		for _, k := range []string{"framework", "framework_version", "requirement_code", "control_id",
			"control_bundle_id", "control_title", "anchor_code", "anchor_scf_release", "anchor_fit", "tested_under_note"} {
			if rec[col[k]] != r[k] {
				t.Errorf("CSV row %d %s = %q; API has %v", i, k, rec[col[k]], r[k])
			}
		}
		if got := rec[col["candidate_anchors"]]; got != "GOV-01 (subset_of 0.7)" {
			t.Errorf("CSV candidate_anchors = %q", got)
		}
		if got := rec[col["candidate_scf_release"]]; got != scfreleasetest.ReleaseB {
			t.Errorf("CSV candidate_scf_release = %q", got)
		}
	}

	// Framework filter: this framework keeps the row; another drops it.
	if _, rows := reportRows(t, f.tenantA, f.tenantA.admin, "?framework="+f.framework); len(rows) != 1 {
		t.Errorf("?framework=%s: rows = %v; want one", f.framework, rows)
	}
	if _, rows := reportRows(t, f.tenantA, f.tenantA.admin, "?framework=no-such-framework"); len(rows) != 0 {
		t.Errorf("?framework=no-such-framework: rows = %v; want none", rows)
	}
	if recs := reportCSV(t, f.tenantA, "?framework=no-such-framework"); len(recs) != 1 {
		t.Errorf("filtered CSV = %v; want header only", recs)
	}

	// Each tenant sees only its own controls.
	_, rowsB := reportRows(t, f.tenantB, f.tenantB.admin, "")
	if len(rowsB) != 1 || rowsB[0]["control_id"] != f.controlB.String() {
		t.Fatalf("tenant B rows = %v; want only B's control %s", rowsB, f.controlB)
	}
	for _, r := range rows {
		if r["control_id"] == f.controlB.String() {
			t.Errorf("tenant A sees tenant B's control: %v", r)
		}
	}
	recsB := reportCSV(t, f.tenantB, "")
	if len(recsB) != 2 || recsB[1][col["control_id"]] != f.controlB.String() {
		t.Errorf("tenant B CSV = %v; want B's control only", recsB)
	}
}

// A control title an organisation entered is neutralised in the CSV, so a
// spreadsheet never evaluates it.
func TestAuditedUnder_CSVNeutralisesFormulas(t *testing.T) {
	f := newAuditedUnderFixture(t)
	if _, err := f.admin.Exec(context.Background(), `UPDATE controls SET title = '=HYPERLINK("x")' WHERE id = $1`, f.controlA); err != nil {
		t.Fatal(err)
	}
	recs := reportCSV(t, f.tenantA, "")
	col := columnIndex(t, recs[0])
	if got := recs[1][col["control_title"]]; strings.HasPrefix(got, "=") {
		t.Errorf("control_title cell %q starts with '='; want it neutralised", got)
	}
}

// addRefs appends tested-under references to a control.
func addRefs(t *testing.T, f auditedUnderFixture, control uuid.UUID, refs string) {
	t.Helper()
	if _, err := f.admin.Exec(context.Background(),
		`UPDATE controls SET external_refs = external_refs || $2::jsonb WHERE id = $1`, control, refs); err != nil {
		t.Fatalf("add refs: %v", err)
	}
}

// reportCSVHeader fetches the export and returns its response headers.
func reportCSVHeader(t *testing.T, e env, query string) http.Header {
	t.Helper()
	req, _ := http.NewRequest(http.MethodGet, e.url+"/v1/mapping-overlays/audited-under/export"+query, nil)
	req.Header.Set("Authorization", "Bearer "+e.admin)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("export%s: status %d", query, resp.StatusCode)
	}
	return resp.Header
}

// #2324: two references to one criterion on one control are one row, with
// their distinct notes merged in a fixed order.
func TestAuditedUnder_FoldsDuplicateReferences(t *testing.T) {
	f := newAuditedUnderFixture(t)
	addRefs(t, f, f.controlA, `[`+
		`{"system":"`+f.framework+`","kind":"tested_under","id":"C2","note":"FY24 walkthrough"},`+
		`{"system":"`+f.framework+`","kind":"tested_under","id":"C2","note":"FY25 report s4"},`+
		`{"system":"`+f.framework+`","kind":"tested_under","id":"C2"}]`)

	body, rows := reportRows(t, f.tenantA, f.tenantA.admin, "")
	if len(rows) != 1 || body["total"] != float64(1) {
		t.Fatalf("rows = %v, total = %v; want one folded C2 row", rows, body["total"])
	}
	if got := rows[0]["tested_under_note"]; got != "FY24 walkthrough; FY25 report s4" {
		t.Errorf("tested_under_note = %q; want the distinct notes, sorted, joined with '; '", got)
	}
	recs := reportCSV(t, f.tenantA, "")
	if len(recs) != 2 {
		t.Fatalf("CSV = %v; want header + one row", recs)
	}
	if got := recs[1][columnIndex(t, recs[0])["tested_under_note"]]; got != "FY24 walkthrough; FY25 report s4" {
		t.Errorf("CSV tested_under_note = %q", got)
	}
}

// #2324: a reference that names no requirement in the framework's current
// version, or no framework at all, is listed instead of dropped.
func TestAuditedUnder_ListsUnresolvedReferences(t *testing.T) {
	f := newAuditedUnderFixture(t)
	addRefs(t, f, f.controlA, `[`+
		`{"system":"`+f.framework+`","kind":"tested_under","id":"C9","note":"typo in FY25 import"},`+
		`{"system":"`+f.framework+`","kind":"tested_under","id":"C9"},`+
		`{"system":"no-such-framework","kind":"tested_under","id":"X1"},`+
		`{"system":"no-such-framework","kind":"mapped_to","id":"X2"}]`)

	body, rows := reportRows(t, f.tenantA, f.tenantA.admin, "")
	if len(rows) != 1 {
		t.Errorf("rows = %v; want the C2 gap only", rows)
	}
	if body["unresolved_count"] != float64(2) {
		t.Fatalf("unresolved_count = %v; want 2 (C9 folded, X1; the mapped_to ref is not tested-under)", body["unresolved_count"])
	}
	got := map[string]map[string]any{}
	for _, u := range body["unresolved_references"].([]any) {
		m := u.(map[string]any)
		got[m["system"].(string)+":"+m["code"].(string)] = m
	}
	c9 := got[f.framework+":C9"]
	if c9 == nil || c9["control_id"] != f.controlA.String() || c9["tested_under_note"] != "typo in FY25 import" {
		t.Errorf("C9 = %v; want tenant A's control with its note", c9)
	}
	if got["no-such-framework:X1"] == nil {
		t.Errorf("unresolved = %v; want no-such-framework:X1", got)
	}

	// The framework filter applies to the reference's system.
	body, _ = reportRows(t, f.tenantA, f.tenantA.admin, "?framework="+f.framework)
	if body["unresolved_count"] != float64(1) {
		t.Errorf("?framework=%s: unresolved_count = %v; want 1", f.framework, body["unresolved_count"])
	}
	// Tenant B sees none of A's.
	if body, _ := reportRows(t, f.tenantB, f.tenantB.admin, ""); body["unresolved_count"] != float64(0) {
		t.Errorf("tenant B unresolved_count = %v; want 0", body["unresolved_count"])
	}
	if h := reportCSVHeader(t, f.tenantA, ""); h.Get("X-Atlas-Unresolved-References") != "2" {
		t.Errorf("X-Atlas-Unresolved-References = %q; want 2", h.Get("X-Atlas-Unresolved-References"))
	}
}

// #2324: ?limit= caps the rows; total and truncated say what was cut.
func TestAuditedUnder_CapsAndFlagsTruncation(t *testing.T) {
	f := newAuditedUnderFixture(t)
	// Unanchored, A's control lists C1 and C2.
	if _, err := f.admin.Exec(context.Background(),
		`UPDATE controls SET scf_anchor_id = NULL, anchor_fit = 'none', anchor_fit_note = 'no SCF equivalent' WHERE id = $1`, f.controlA); err != nil {
		t.Fatal(err)
	}
	body, rows := reportRows(t, f.tenantA, f.tenantA.admin, "")
	if len(rows) != 2 || body["total"] != float64(2) || body["truncated"] != false || body["limit"] != float64(5000) {
		t.Fatalf("uncapped: %d rows, total=%v truncated=%v limit=%v", len(rows), body["total"], body["truncated"], body["limit"])
	}
	body, rows = reportRows(t, f.tenantA, f.tenantA.admin, "?limit=1")
	if len(rows) != 1 || rows[0]["requirement_code"] != "C1" || body["total"] != float64(2) || body["truncated"] != true {
		t.Fatalf("limit=1: rows=%v total=%v truncated=%v; want C1 of 2, truncated", rows, body["total"], body["truncated"])
	}
	if recs := reportCSV(t, f.tenantA, "?limit=1"); len(recs) != 2 {
		t.Errorf("CSV limit=1 = %v; want header + one row", recs)
	}
	if h := reportCSVHeader(t, f.tenantA, "?limit=1"); h.Get("X-Atlas-Truncated") != "true" {
		t.Errorf("X-Atlas-Truncated = %q; want true", h.Get("X-Atlas-Truncated"))
	}
	if h := reportCSVHeader(t, f.tenantA, ""); h.Get("X-Atlas-Truncated") != "false" {
		t.Errorf("uncapped X-Atlas-Truncated = %q; want false", h.Get("X-Atlas-Truncated"))
	}
	for _, q := range []string{"?limit=0", "?limit=5001", "?limit=abc"} {
		if status, _ := call(t, f.tenantA, http.MethodGet, "/v1/mapping-overlays/audited-under"+q, f.tenantA.admin, nil); status != http.StatusBadRequest {
			t.Errorf("%s: status %d; want 400", q, status)
		}
	}
}
