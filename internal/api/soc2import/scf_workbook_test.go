package soc2import_test

import (
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/xuri/excelize/v2"

	"github.com/mgoodric/security-atlas/internal/api/soc2import"
)

func TestLoadSCFWorkbook_WideWorkbookMatchesYAMLModelShape(t *testing.T) {
	t.Parallel()
	xlsx := writeWideSCFWorkbook(t)
	cw, err := soc2import.LoadSCFWorkbook(xlsx, soc2import.SCFWorkbookOptions{AllowCodeTitles: true})
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

// The published 2026.3 workbook puts "SCF Domains & Principles" ahead of the
// "SCF 2026.3" control sheet and names the metadata sheet "Focal Documents
// (FD)". The loader must pick sheets by name, not by position.
func TestLoadSCFWorkbook_PublishedSheetOrderPicksControlSheet(t *testing.T) {
	t.Parallel()
	xlsx := writePublishedLayoutSCFWorkbook(t)
	cw, err := soc2import.LoadSCFWorkbook(xlsx, soc2import.SCFWorkbookOptions{
		FrameworkColumns: []string{"Example\nStandard\n2026"},
		AllowCodeTitles:  true,
	})
	if err != nil {
		t.Fatalf("LoadSCFWorkbook: %v", err)
	}
	if len(cw.Mappings) != 3 {
		t.Fatalf("mappings = %d; want 3 from the SCF 2026.3 sheet", len(cw.Mappings))
	}
	if !strings.Contains(cw.Mappings[0].Rationale, `sheet "SCF 2026.3"`) {
		t.Fatalf("rationale should cite the SCF 2026.3 sheet; got %q", cw.Mappings[0].Rationale)
	}
	if cw.FrameworkSlug != "general-example-standard-2026" || cw.FrameworkIssuer != "Example Issuer" {
		t.Fatalf("focal-document metadata not read from \"Focal Documents (FD)\": %+v", cw)
	}
}

// SCF maps ISO 27001's management-system clauses under "ISO 27001 2022" and
// its Annex A controls under "ISO 27002 2022" as bare 5.1-8.34. Repeating the
// framework column combines both into one framework, and the "=A." suffix
// puts the Annex A codes in the A.x form the shipped iso27001 crosswalk uses.
func TestLoadSCFWorkbook_CombinesColumnsWithPrefix(t *testing.T) {
	t.Parallel()
	f := excelize.NewFile()
	mustSetSheetName(t, f, "Sheet1", "Focal Documents")
	mustSetRows(t, f, "Focal Documents", [][]interface{}{
		{"Geography", "SCF Column Header", "Focal Document Identifier (FDI)", "Source", "Focal Document Name (FDN)"},
		{"General", "ISO\n27001\n2022", "general-iso-27001-2022", "ISO", "ISO/IEC 27001:2022"},
		{"General", "ISO\n27002\n2022", "general-iso-27002-2022", "ISO", "ISO/IEC 27002:2022"},
	})
	if _, err := f.NewSheet("SCF 2026.3"); err != nil {
		t.Fatalf("NewSheet: %v", err)
	}
	mustSetRows(t, f, "SCF 2026.3", [][]interface{}{
		{"SCF Domain", "SCF Control", "SCF #", "ISO\n27001\n2022", "ISO\n27002\n2022"},
		{"Governance", "Program", "GOV-01", "4.4\n5.1", "5.1"},
		{"Identity", "Access", "IAC-01", "", "5.15\n5.16"},
	})
	path := filepath.Join(t.TempDir(), "scf-iso.xlsx")
	if err := f.SaveAs(path); err != nil {
		t.Fatalf("SaveAs: %v", err)
	}

	cw, err := soc2import.LoadSCFWorkbook(path, soc2import.SCFWorkbookOptions{
		FrameworkColumns: []string{"ISO 27001 2022", "ISO 27002 2022=A."},
		FrameworkSlug:    "iso27001",
		FrameworkVersion: "2022",
		// The bundled ISO YAML covers Annex A only; clauses 4.4 and 5.1 keep
		// their codes.
		AllowCodeTitles: true,
	})
	if err != nil {
		t.Fatalf("LoadSCFWorkbook: %v", err)
	}
	var codes []string
	for _, r := range cw.Requirements {
		codes = append(codes, r.Code)
	}
	if got, want := strings.Join(codes, ","), "4.4,5.1,A.5.1,A.5.15,A.5.16"; got != want {
		t.Fatalf("requirement codes = %s; want %s", got, want)
	}
	if len(cw.Mappings) != 5 {
		t.Fatalf("mappings = %d; want 5", len(cw.Mappings))
	}
	if cw.FrameworkSlug != "iso27001" || cw.FrameworkName != "ISO/IEC 27001:2022" || cw.FrameworkVersion != "2022" {
		t.Fatalf("metadata should come from the first column: %+v", cw)
	}
	// #1971: the source names the columns read, so it never matches the
	// bundled YAML's and the two imports prune only their own edges.
	if want := "scf-workbook:ISO 27001 2022+ISO 27002 2022=A."; cw.Source != want {
		t.Fatalf("Source = %q; want %q", cw.Source, want)
	}
	for _, m := range cw.Mappings {
		if strings.HasPrefix(m.RequirementCode, "A.") != strings.Contains(m.Rationale, "27002") {
			t.Fatalf("rationale should cite the column the code came from: %+v", m)
		}
	}
}

func TestLoadSCFWorkbook_FrameworkColumnErrors(t *testing.T) {
	t.Parallel()
	xlsx := writePublishedLayoutSCFWorkbook(t)
	for _, tc := range []struct {
		name    string
		columns []string
		want    string
	}{
		{"unknown header", []string{"Nope"}, `framework column "Nope" not found`},
		{"unknown header with prefix", []string{"Nope=A."}, `framework column "Nope" not found`},
		{"duplicate column", []string{"Example Standard 2026", "Example Standard 2026"}, "adds no mappings beyond the columns before it"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			_, err := soc2import.LoadSCFWorkbook(xlsx, soc2import.SCFWorkbookOptions{FrameworkColumns: tc.columns})
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("err = %v; want %q", err, tc.want)
			}
		})
	}
}

// The control sheet is found by the version regex, else by the fallback: the
// first "SCF ..." sheet whose header row carries an "SCF #" column.
func TestLoadSCFWorkbook_ControlSheetSelection(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name      string
		sheets    []string
		wantSheet string
	}{
		{"version sheet", []string{"SCF Domains & Principles", "SCF 2026.3"}, "SCF 2026.3"},
		{"patch version sheet", []string{"SCF Domains & Principles", "SCF 2026.3.1"}, "SCF 2026.3.1"},
		{"fallback to SCF # header", []string{"SCF Domains & Principles", "SCF Controls"}, "SCF Controls"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			f := excelize.NewFile()
			mustSetSheetName(t, f, "Sheet1", "Focal Documents")
			mustSetRows(t, f, "Focal Documents", [][]interface{}{
				{"SCF Column Header", "Focal Document Identifier (FDI)", "Source"},
				{"Example\nStandard", "general-example-standard", "Example Issuer"},
			})
			for _, name := range tc.sheets {
				if _, err := f.NewSheet(name); err != nil {
					t.Fatalf("NewSheet %q: %v", name, err)
				}
			}
			mustSetRows(t, f, "SCF Domains & Principles", [][]interface{}{
				{"#", "SCF Domain", "SCF Identifier"},
				{"1", "Governance", "GOV"},
			})
			mustSetRows(t, f, tc.wantSheet, [][]interface{}{
				{"SCF #", "Example\nStandard"},
				{"GOV-01", "EX-1"},
			})
			path := filepath.Join(t.TempDir(), "select.xlsx")
			if err := f.SaveAs(path); err != nil {
				t.Fatalf("SaveAs: %v", err)
			}
			cw, err := soc2import.LoadSCFWorkbook(path, soc2import.SCFWorkbookOptions{FrameworkVersion: "1", AllowCodeTitles: true})
			if err != nil {
				t.Fatalf("LoadSCFWorkbook: %v", err)
			}
			if !strings.Contains(cw.Mappings[0].Rationale, strconv.Quote(tc.wantSheet)) {
				t.Fatalf("rationale should cite sheet %q; got %q", tc.wantSheet, cw.Mappings[0].Rationale)
			}
		})
	}
}

// Without focal-document metadata the slug would fall back to the column
// header and could duplicate a framework, so the import fails unless the
// operator passes a slug.
func TestLoadSCFWorkbook_MissingFocalMetadataRequiresSlug(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name      string
		focal     [][]interface{}
		wantCause string
		autoCause string
	}{
		{"no focal documents sheet", nil, `no "Focal Documents" sheet`, `no "Focal Documents" sheet`},
		{"no SCF Column Header", [][]interface{}{
			{"Focal Document Identifier (FDI)", "Source"},
			{"general-example-standard", "Example Issuer"},
		}, `missing "SCF Column Header"`, `missing "SCF Column Header"`},
		{"column not listed", [][]interface{}{
			{"SCF Column Header", "Focal Document Identifier (FDI)"},
			{"Other\nStandard", "general-other-standard"},
		}, `no row for column "Example Standard"`, `no column header matches`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			f := excelize.NewFile()
			mustSetSheetName(t, f, "Sheet1", "SCF 2026.3")
			mustSetRows(t, f, "SCF 2026.3", [][]interface{}{
				{"SCF #", "Example\nStandard"},
				{"GOV-01", "EX-1"},
			})
			if tc.focal != nil {
				if _, err := f.NewSheet("Focal Documents"); err != nil {
					t.Fatalf("NewSheet: %v", err)
				}
				mustSetRows(t, f, "Focal Documents", tc.focal)
			}
			path := filepath.Join(t.TempDir(), "no-focal.xlsx")
			if err := f.SaveAs(path); err != nil {
				t.Fatalf("SaveAs: %v", err)
			}

			_, err := soc2import.LoadSCFWorkbook(path, soc2import.SCFWorkbookOptions{FrameworkColumns: []string{"Example Standard"}})
			if err == nil {
				t.Fatal("expected an error without --framework-slug")
			}
			if !strings.Contains(err.Error(), tc.wantCause) || !strings.Contains(err.Error(), "--framework-slug") {
				t.Fatalf("error should name %q and --framework-slug; got %v", tc.wantCause, err)
			}

			cw, err := soc2import.LoadSCFWorkbook(path, soc2import.SCFWorkbookOptions{
				FrameworkColumns: []string{"Example Standard"},
				FrameworkSlug:    "example-standard",
				AllowCodeTitles:  true,
			})
			if err != nil {
				t.Fatalf("LoadSCFWorkbook with slug: %v", err)
			}
			if cw.FrameworkSlug != "example-standard" {
				t.Fatalf("framework_slug = %q", cw.FrameworkSlug)
			}

			_, err = soc2import.LoadSCFWorkbook(path, soc2import.SCFWorkbookOptions{})
			if err == nil || !strings.Contains(err.Error(), "no framework mapping columns found") || !strings.Contains(err.Error(), tc.autoCause) {
				t.Fatalf("auto-select error should name the cause; got %v", err)
			}
		})
	}
}

func TestLoadSCFWorkbook_NoControlSheetNamesCandidates(t *testing.T) {
	t.Parallel()
	f := excelize.NewFile()
	mustSetSheetName(t, f, "Sheet1", "SCF Domains & Principles")
	mustSetRows(t, f, "SCF Domains & Principles", [][]interface{}{
		{"#", "SCF Domain", "SCF Identifier"},
		{"1", "Governance", "GOV"},
	})
	path := filepath.Join(t.TempDir(), "no-control-sheet.xlsx")
	if err := f.SaveAs(path); err != nil {
		t.Fatalf("SaveAs: %v", err)
	}
	_, err := soc2import.LoadSCFWorkbook(path, soc2import.SCFWorkbookOptions{})
	if err == nil {
		t.Fatal("expected missing control sheet error")
	}
	if !strings.Contains(err.Error(), `"SCF Domains & Principles"`) {
		t.Fatalf("error should name the SCF sheets it rejected; got %v", err)
	}
}

func TestLoadSCFWorkbook_OLIRWorkbookUsesExplicitSTRM(t *testing.T) {
	t.Parallel()
	xlsx := writeOLIRWorkbook(t, "Subset Of", "10")
	cw, err := soc2import.LoadSCFWorkbook(xlsx, soc2import.SCFWorkbookOptions{
		FrameworkSlug:   "nist-800-53",
		FrameworkIssuer: "NIST",
		AllowCodeTitles: true,
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
	if !strings.HasPrefix(cw.Source, "scf-olir:") {
		t.Fatalf("Source = %q; want an scf-olir: source", cw.Source)
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

// writePublishedLayoutSCFWorkbook builds a minimal fixture with the sheet
// order of the published SCF 2026.3 workbook. Cell contents are synthetic;
// the real workbook is not committed.
func writePublishedLayoutSCFWorkbook(t *testing.T) string {
	t.Helper()
	f := excelize.NewFile()
	mustSetSheetName(t, f, "Sheet1", "READ THIS")
	mustSetRows(t, f, "READ THIS", [][]interface{}{{"Synthetic fixture"}})
	for _, name := range []string{"Change Overview", "STRM Errata", "SCF Domains & Principles", "Focal Documents (FD)", "SCF 2026.3", "Assessment Objectives 2026.3"} {
		if _, err := f.NewSheet(name); err != nil {
			t.Fatalf("NewSheet %q: %v", name, err)
		}
	}
	mustSetRows(t, f, "Change Overview", [][]interface{}{{"Change", "SCF #"}, {"Added", "GOV-01"}})
	// STRM Errata sorts ahead of the control sheet and carries all four OLIR
	// STRM headers; it must never be imported as the crosswalk.
	mustSetRows(t, f, "STRM Errata", [][]interface{}{
		{"Focal Document Element", "Relationship", "Reference Document Element", "Strength of Relationship"},
		{"ERR-1", "Equal To", "GOV-99", "10"},
	})
	mustSetRows(t, f, "SCF Domains & Principles", [][]interface{}{
		{"#", "SCF Domain", "SCF Identifier", "Cybersecurity & Data Privacy by Design (C|P) Principles"},
		{"1", "Governance", "GOV", "principle"},
	})
	mustSetRows(t, f, "Focal Documents (FD)", [][]interface{}{
		{"Geography", "SCF Column Header", "Focal Document Identifier (FDI)", "Source", "Focal Document Name (FDN)"},
		{"General", "Example\nStandard\n2026", "general-example-standard-2026", "Example Issuer", "Example Standard 2026"},
	})
	mustSetRows(t, f, "SCF 2026.3", [][]interface{}{
		{"SCF Domain", "SCF Control", "SCF #", "Secure Controls Framework (SCF)\nControl Description", "Example\nStandard\n2026", "Other\nStandard"},
		{"Governance", "Program", "GOV-01", "short", "EX-1\nEX-2", "OT-1"},
		{"Identity", "Access", "IAC-01", "short", "EX-2", ""},
	})
	path := filepath.Join(t.TempDir(), "scf-published-layout.xlsx")
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
