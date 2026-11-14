package soc2import_test

import (
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/xuri/excelize/v2"

	"github.com/mgoodric/security-atlas/internal/api/soc2import"
)

// writeSOC2WideWorkbook builds a wide SCF workbook whose single framework
// column ("AICPA TSC 2017") holds the given cell per SCF anchor row.
func writeSOC2WideWorkbook(t *testing.T, cells map[string]string) string {
	t.Helper()
	f := excelize.NewFile()
	mustSetSheetName(t, f, "Sheet1", "Focal Documents")
	mustSetRows(t, f, "Focal Documents", [][]interface{}{
		{"Geography", "SCF Column Header", "Focal Document Identifier (FDI)", "Source", "Focal Document Name (FDN)"},
		{"US", "AICPA\nTSC 2017", "usa-aicpa-tsc-2017", "AICPA", "AICPA Trust Services Criteria 2017"},
	})
	if _, err := f.NewSheet("SCF 2026.3"); err != nil {
		t.Fatalf("NewSheet: %v", err)
	}
	rows := [][]interface{}{{"SCF Domain", "SCF Control", "SCF #", "AICPA\nTSC 2017"}}
	for _, anchor := range []string{"AST-01", "BCD-02", "IAC-01"} {
		if cell, ok := cells[anchor]; ok {
			rows = append(rows, []interface{}{"Domain", "Control", anchor, cell})
		}
	}
	mustSetRows(t, f, "SCF 2026.3", rows)
	path := filepath.Join(t.TempDir(), "scf-soc2.xlsx")
	if err := f.SaveAs(path); err != nil {
		t.Fatalf("SaveAs: %v", err)
	}
	return path
}

// writeSOC2OLIRWorkbook builds an OLIR STRM workbook with one row per
// {code, relationship, anchor, strength}.
func writeSOC2OLIRWorkbook(t *testing.T, rows [][4]string) string {
	t.Helper()
	f := excelize.NewFile()
	mustSetSheetName(t, f, "Sheet1", "AICPA TSC 2017")
	data := [][]interface{}{{"Focal Document\nElement", "Rationale", "Relationship", "Reference Document\nElement", "Strength of\nRelationship\n(Optional)"}}
	for _, r := range rows {
		data = append(data, []interface{}{r[0], "source rationale", r[1], r[2], r[3]})
	}
	mustSetRows(t, f, "AICPA TSC 2017", data)
	path := filepath.Join(t.TempDir(), "soc2-olir.xlsx")
	if err := f.SaveAs(path); err != nil {
		t.Fatalf("SaveAs: %v", err)
	}
	return path
}

func edgesByCodeAnchor(cw *soc2import.Crosswalk) map[string]soc2import.Mapping {
	out := map[string]soc2import.Mapping{}
	for _, m := range cw.Mappings {
		out[m.RequirementCode+"->"+m.SCFAnchor] = m
	}
	return out
}

// #1731 AC-1: a criterion and two of its points of focus, each against a
// different anchor, import as one requirement with three edges and two
// points of focus in its metadata.
func TestLoadSCFWorkbook_RollsPointsOfFocusIntoCriterion(t *testing.T) {
	t.Parallel()
	xlsx := writeSOC2WideWorkbook(t, map[string]string{
		"AST-01": "CC1.1",
		"BCD-02": "CC1.1-POF1",
		"IAC-01": "CC1.1-POF2",
	})
	cw, err := soc2import.LoadSCFWorkbook(xlsx, soc2import.SCFWorkbookOptions{FrameworkSlug: "soc2", FrameworkVersion: "2017"})
	if err != nil {
		t.Fatalf("LoadSCFWorkbook: %v", err)
	}
	if len(cw.Requirements) != 1 || cw.Requirements[0].Code != "CC1.1" {
		t.Fatalf("requirements = %+v; want only CC1.1", cw.Requirements)
	}
	wantPOF := []soc2import.PointOfFocus{
		{Code: "CC1.1-POF1", SCFAnchors: []string{"BCD-02"}},
		{Code: "CC1.1-POF2", SCFAnchors: []string{"IAC-01"}},
	}
	if got := cw.Requirements[0].PointsOfFocus; !reflect.DeepEqual(got, wantPOF) {
		t.Fatalf("points of focus = %+v; want %+v", got, wantPOF)
	}
	edges := edgesByCodeAnchor(cw)
	if len(cw.Mappings) != 3 {
		t.Fatalf("mappings = %d (%+v); want 3", len(cw.Mappings), cw.Mappings)
	}
	for _, key := range []string{"CC1.1->AST-01", "CC1.1->BCD-02", "CC1.1->IAC-01"} {
		if _, ok := edges[key]; !ok {
			t.Fatalf("missing edge %s in %+v", key, cw.Mappings)
		}
	}
	if !strings.Contains(edges["CC1.1->BCD-02"].Rationale, "point of focus CC1.1-POF1") {
		t.Fatalf("rolled-up edge rationale should name its point of focus: %q", edges["CC1.1->BCD-02"].Rationale)
	}
	if got := cw.PointsOfFocusRollUp; got.PointsOfFocus != 2 || got.Criteria != 1 || len(got.CreatedParents) != 0 {
		t.Fatalf("roll-up = %+v; want 2 points of focus into 1 criterion, none created", got)
	}
	if got := cw.PointsOfFocusRollUp.Summary(); len(got) != 1 || got[0] != "points of focus rolled up=2 into criteria=1" {
		t.Fatalf("summary = %q", got)
	}
}

// #1731 AC-2: a point of focus whose criterion the column never names bare
// creates the criterion, and the summary reports it.
func TestLoadSCFWorkbook_PointOfFocusCreatesMissingParent(t *testing.T) {
	t.Parallel()
	xlsx := writeSOC2WideWorkbook(t, map[string]string{
		"AST-01": "CC1.1",
		"IAC-01": "CC2.1-POF1",
	})
	cw, err := soc2import.LoadSCFWorkbook(xlsx, soc2import.SCFWorkbookOptions{FrameworkSlug: "soc2", FrameworkVersion: "2017"})
	if err != nil {
		t.Fatalf("LoadSCFWorkbook: %v", err)
	}
	var codes []string
	for _, r := range cw.Requirements {
		codes = append(codes, r.Code)
	}
	if got := strings.Join(codes, ","); got != "CC1.1,CC2.1" {
		t.Fatalf("requirement codes = %s; want CC1.1,CC2.1", got)
	}
	if got := cw.Requirements[1]; got.Title != "CC2.1" || len(got.PointsOfFocus) != 1 {
		t.Fatalf("created parent = %+v; want title CC2.1 and one point of focus", got)
	}
	if got := cw.PointsOfFocusRollUp.CreatedParents; !reflect.DeepEqual(got, []string{"CC2.1"}) {
		t.Fatalf("created parents = %v; want [CC2.1]", got)
	}
	summary := strings.Join(cw.PointsOfFocusRollUp.Summary(), "\n")
	if !strings.Contains(summary, "criterion CC2.1 created from its points of focus") {
		t.Fatalf("summary should report the created criterion:\n%s", summary)
	}
}

// #1731 AC-4: when the criterion and a point of focus map the same anchor
// there is one edge, carrying the criterion's own relationship and strength.
// Against an anchor only points of focus map, the strongest one wins.
func TestLoadSCFWorkbook_PointOfFocusEdgePrecedence(t *testing.T) {
	t.Parallel()
	xlsx := writeSOC2OLIRWorkbook(t, [][4]string{
		{"CC1.1-POF1", "Intersects With", "AST-01", "9"},
		{"CC1.1", "Equal", "AST-01", "6"},
		{"CC1.1-POF1", "Subset Of", "IAC-01", "3"},
		{"CC1.1-POF2", "Intersects With", "IAC-01", "8"},
		{"CC1.1-POF3", "Superset Of", "IAC-01", "8"},
	})
	cw, err := soc2import.LoadSCFWorkbook(xlsx, soc2import.SCFWorkbookOptions{FrameworkSlug: "soc2", FrameworkVersion: "2017"})
	if err != nil {
		t.Fatalf("LoadSCFWorkbook: %v", err)
	}
	if len(cw.Mappings) != 2 {
		t.Fatalf("mappings = %+v; want 2 (no duplicate edges)", cw.Mappings)
	}
	edges := edgesByCodeAnchor(cw)
	own := edges["CC1.1->AST-01"]
	if own.RelationshipType != "equal" || own.Strength != 0.6 || own.Rationale != "source rationale" {
		t.Fatalf("CC1.1->AST-01 = %+v; want the criterion's own equal/0.6 edge", own)
	}
	rolled := edges["CC1.1->IAC-01"]
	if rolled.RelationshipType != "intersects_with" || rolled.Strength != 0.8 {
		t.Fatalf("CC1.1->IAC-01 = %+v; want the first strongest point-of-focus edge (intersects_with/0.8)", rolled)
	}
	if len(cw.Requirements) != 1 || len(cw.Requirements[0].PointsOfFocus) != 3 {
		t.Fatalf("requirements = %+v; want CC1.1 with three points of focus", cw.Requirements)
	}
	if got := cw.Requirements[0].PointsOfFocus[0]; !reflect.DeepEqual(got.SCFAnchors, []string{"AST-01", "IAC-01"}) {
		t.Fatalf("CC1.1-POF1 anchors = %v; want both anchors it mapped", got.SCFAnchors)
	}
}

// A crosswalk without points of focus is untouched by the roll-up.
func TestLoadSCFWorkbook_NoPointsOfFocusNoRollUp(t *testing.T) {
	t.Parallel()
	cw, err := soc2import.LoadSCFWorkbook(writeWideSCFWorkbook(t), soc2import.SCFWorkbookOptions{})
	if err != nil {
		t.Fatalf("LoadSCFWorkbook: %v", err)
	}
	if got := cw.PointsOfFocusRollUp; got.PointsOfFocus != 0 || got.Summary() != nil {
		t.Fatalf("roll-up = %+v; want zero", got)
	}
	for _, r := range cw.Requirements {
		if r.PointsOfFocus != nil {
			t.Fatalf("requirement %s has points of focus %+v", r.Code, r.PointsOfFocus)
		}
	}
}

// #1731 desired behavior 7: a YAML crosswalk may list points of focus.
func TestLoad_YAMLAcceptsPointsOfFocus(t *testing.T) {
	t.Parallel()
	cw, err := soc2import.Load(writeTemp(t, `
schema_version: "1.0"
framework_slug: soc2
framework_version: "2017"
source_attribution: community_draft
requirements:
  - code: CC1.1
    title: COSO Principle 1
    points_of_focus:
      - code: CC1.1-POF1
        scf_anchors: [GOV-01]
mappings:
  - requirement_code: CC1.1
    scf_anchor: GOV-01
    relationship_type: intersects_with
    strength: 0.5
    rationale: test
`))
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	want := []soc2import.PointOfFocus{{Code: "CC1.1-POF1", SCFAnchors: []string{"GOV-01"}}}
	if got := cw.Requirements[0].PointsOfFocus; !reflect.DeepEqual(got, want) {
		t.Fatalf("points of focus = %+v; want %+v", got, want)
	}
}
