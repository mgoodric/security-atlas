package soc2import_test

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/xuri/excelize/v2"

	"github.com/mgoodric/security-atlas/internal/api/soc2import"
)

func TestLoadSCFWorkbook_WideWorkbookMatchesYAMLModelShape(t *testing.T) {
	t.Parallel()
	xlsx := writeWideSCFWorkbook(t)
	cw, err := soc2import.LoadSCFWorkbook(xlsx, soc2import.SCFWorkbookOptions{})
	if err != nil {
		t.Fatalf("LoadSCFWorkbook: %v", err)
	}

	yamlPath := writeTemp(t, `
schema_version: "1.0"
framework_slug: "general-example-standard-2026"
framework_name: "Example Standard 2026"
framework_issuer: "Example Issuer"
framework_version: "2026"
source_attribution: "community_draft"
requirements:
  - code: EX-1
    title: EX-1
  - code: EX-2
    title: EX-2
mappings:
  - requirement_code: EX-1
    scf_anchor: GOV-01
    relationship_type: intersects_with
    strength: 0.5
    rationale: workbook default
  - requirement_code: EX-2
    scf_anchor: GOV-01
    relationship_type: intersects_with
    strength: 0.5
    rationale: workbook default
  - requirement_code: EX-2
    scf_anchor: IAC-01
    relationship_type: intersects_with
    strength: 0.5
    rationale: workbook default
`)
	yamlCW, err := soc2import.Load(yamlPath)
	if err != nil {
		t.Fatalf("Load yaml fixture: %v", err)
	}

	if cw.FrameworkSlug != yamlCW.FrameworkSlug || cw.FrameworkName != yamlCW.FrameworkName ||
		cw.FrameworkIssuer != yamlCW.FrameworkIssuer || cw.FrameworkVersion != yamlCW.FrameworkVersion {
		t.Fatalf("metadata mismatch:\n xlsx=%+v\n yaml=%+v", cw, yamlCW)
	}
	if len(cw.Requirements) != len(yamlCW.Requirements) || len(cw.Mappings) != len(yamlCW.Mappings) {
		t.Fatalf("shape mismatch: xlsx req/map=%d/%d yaml req/map=%d/%d",
			len(cw.Requirements), len(cw.Mappings), len(yamlCW.Requirements), len(yamlCW.Mappings))
	}
	for _, m := range cw.Mappings {
		if m.RelationshipType != "intersects_with" || m.Strength != 0.5 {
			t.Fatalf("wide workbook mapping did not get community draft STRM defaults: %+v", m)
		}
	}
	if cw.SourceAttribution != "community_draft" {
		t.Fatalf("source attribution = %q; want community_draft", cw.SourceAttribution)
	}
}

func TestLoadSCFWorkbook_OLIRWorkbookUsesExplicitSTRM(t *testing.T) {
	t.Parallel()
	xlsx := writeOLIRWorkbook(t, "Subset Of", "10")
	cw, err := soc2import.LoadSCFWorkbook(xlsx, soc2import.SCFWorkbookOptions{
		FrameworkSlug:   "nist-800-53",
		FrameworkIssuer: "NIST",
	})
	if err != nil {
		t.Fatalf("LoadSCFWorkbook: %v", err)
	}
	if cw.FrameworkVersion != "NIST SP 800-53 R5.1.1" {
		t.Fatalf("framework_version = %q", cw.FrameworkVersion)
	}
	if len(cw.Requirements) != 1 || len(cw.Mappings) != 1 {
		t.Fatalf("got requirements/mappings %d/%d", len(cw.Requirements), len(cw.Mappings))
	}
	got := cw.Mappings[0]
	if got.RequirementCode != "AC-01" || got.SCFAnchor != "GOV-02" ||
		got.RelationshipType != "subset_of" || got.Strength != 1.0 {
		t.Fatalf("mapping = %+v", got)
	}
}

func TestLoadSCFWorkbook_MalformedWorkbookFailsClearly(t *testing.T) {
	t.Parallel()
	f := excelize.NewFile()
	path := filepath.Join(t.TempDir(), "wrong.xlsx")
	if err := f.SaveAs(path); err != nil {
		t.Fatalf("SaveAs: %v", err)
	}
	_, err := soc2import.LoadSCFWorkbook(path, soc2import.SCFWorkbookOptions{})
	if err == nil {
		t.Fatal("expected malformed workbook error")
	}
	if !strings.Contains(err.Error(), "missing an \"SCF <version>\" sheet") {
		t.Fatalf("error should name missing SCF sheet; got %v", err)
	}
}

func TestLoadSCFWorkbook_OLIRRejectsBadRelationship(t *testing.T) {
	t.Parallel()
	xlsx := writeOLIRWorkbook(t, "Close Enough", "10")
	_, err := soc2import.LoadSCFWorkbook(xlsx, soc2import.SCFWorkbookOptions{})
	if err == nil {
		t.Fatal("expected bad relationship error")
	}
	if !strings.Contains(err.Error(), "Relationship") && !strings.Contains(err.Error(), "relationship") {
		t.Fatalf("error should mention relationship; got %v", err)
	}
}

func writeWideSCFWorkbook(t *testing.T) string {
	t.Helper()
	f := excelize.NewFile()
	mustSetSheetName(t, f, "Sheet1", "Focal Documents")
	mustSetRows(t, f, "Focal Documents", [][]interface{}{
		{"Geography", "SCF Column Header", "Focal Document Identifier (FDI)", "Source", "Focal Document Name (FDN)"},
		{"General", "Example\nStandard\n2026", "general-example-standard-2026", "Example Issuer", "Example Standard 2026"},
	})
	_, err := f.NewSheet("SCF 2026.2")
	if err != nil {
		t.Fatalf("NewSheet: %v", err)
	}
	mustSetRows(t, f, "SCF 2026.2", [][]interface{}{
		{"SCF Domain", "SCF Control", "SCF #", "Secure Controls Framework (SCF)\nControl Description", "Example\nStandard\n2026"},
		{"Governance", "Program", "GOV-01", "short", "EX-1\nEX-2"},
		{"Identity", "Access", "IAC-01", "short", "EX-2"},
	})
	path := filepath.Join(t.TempDir(), "scf-wide.xlsx")
	if err := f.SaveAs(path); err != nil {
		t.Fatalf("SaveAs: %v", err)
	}
	return path
}

func writeOLIRWorkbook(t *testing.T, relationship, strength string) string {
	t.Helper()
	f := excelize.NewFile()
	mustSetSheetName(t, f, "Sheet1", "General Information")
	mustSetRows(t, f, "General Information", [][]interface{}{
		{"Informative Reference Submission Form"},
		{"Field Name", "Value"},
		{"Focal Document Version", "NIST SP 800-53 R5.1.1"},
	})
	_, err := f.NewSheet("NIST 800-53 R5.1.1")
	if err != nil {
		t.Fatalf("NewSheet: %v", err)
	}
	mustSetRows(t, f, "NIST 800-53 R5.1.1", [][]interface{}{
		{"Focal Document\nElement", "Focal Document Element Description", "Security Control\nBaseline", "Rationale", "Relationship", "Reference Document\nElement", "Reference Document\nElement Description\n(Optional)", "Comments\n(Optional)", "Strength of\nRelationship\n(Optional)"},
		{"AC-01", "do not import prose", "Low", "Functional", relationship, "GOV-02", "do not import prose", "", strength},
	})
	path := filepath.Join(t.TempDir(), "scf-olir.xlsx")
	if err := f.SaveAs(path); err != nil {
		t.Fatalf("SaveAs: %v", err)
	}
	return path
}

func mustSetSheetName(t *testing.T, f *excelize.File, oldName, newName string) {
	t.Helper()
	if err := f.SetSheetName(oldName, newName); err != nil {
		t.Fatalf("SetSheetName: %v", err)
	}
}

func mustSetRows(t *testing.T, f *excelize.File, sheet string, rows [][]interface{}) {
	t.Helper()
	for r, row := range rows {
		for c, val := range row {
			cell, err := excelize.CoordinatesToCellName(c+1, r+1)
			if err != nil {
				t.Fatalf("CoordinatesToCellName: %v", err)
			}
			if err := f.SetCellValue(sheet, cell, val); err != nil {
				t.Fatalf("SetCellValue: %v", err)
			}
		}
	}
}
