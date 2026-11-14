package scfimport

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/xuri/excelize/v2"
)

// writeWorkbook builds an .xlsx with the published 2026.3 layout: a
// "SCF Domains & Principles" sheet ahead of the "SCF 2026.3" control sheet,
// and header cells carrying the workbook's line breaks.
func writeWorkbook(t *testing.T, sheets map[string][][]interface{}, order []string) string {
	t.Helper()
	f := excelize.NewFile()
	defer func() { _ = f.Close() }()
	for i, name := range order {
		if i == 0 {
			if err := f.SetSheetName("Sheet1", name); err != nil {
				t.Fatal(err)
			}
		} else if _, err := f.NewSheet(name); err != nil {
			t.Fatal(err)
		}
		for r, row := range sheets[name] {
			cell, err := excelize.CoordinatesToCellName(1, r+1)
			if err != nil {
				t.Fatal(err)
			}
			row := row
			if err := f.SetSheetRow(name, cell, &row); err != nil {
				t.Fatal(err)
			}
		}
	}
	path := filepath.Join(t.TempDir(), "secure-controls-framework-scf-2026-3.xlsx")
	if err := f.SaveAs(path); err != nil {
		t.Fatal(err)
	}
	return path
}

var publishedHeaders = []interface{}{
	"SCF Domain", "SCF Control", "SCF #", "Secure Controls Framework (SCF)\nControl Description", "Conformity Validation\nCadence",
}

func publishedLayout(rows ...[]interface{}) (map[string][][]interface{}, []string) {
	control := append([][]interface{}{publishedHeaders}, rows...)
	return map[string][][]interface{}{
		"SCF Domains & Principles": {{"#", "SCF Domain", "SCF Identifier"}, {"1", "Cybersecurity & Data Protection Governance", "GOV"}},
		"SCF 2026.3":               control,
	}, []string{"SCF Domains & Principles", "SCF 2026.3"}
}

func TestLoadWorkbook_PublishedLayout(t *testing.T) {
	t.Parallel()
	sheets, order := publishedLayout(
		[]interface{}{"Cybersecurity & Data Protection Governance", "Cybersecurity & Data Protection Governance Program", "GOV-01", "Mechanisms exist to facilitate governance.", "Annual"},
		[]interface{}{"Identification & Authentication", "Multi-Factor Authentication (MFA)", "IAC-37", "Mechanisms exist to require MFA.", "Annual"},
		[]interface{}{"", "", "", "", ""},
	)
	cat, err := LoadWorkbook(writeWorkbook(t, sheets, order), WorkbookOptions{ReleaseDate: "2026-09-30"})
	if err != nil {
		t.Fatalf("LoadWorkbook: %v", err)
	}
	if cat.SchemaVersion != SchemaVersion || cat.ReleaseVersion != "2026.3" || cat.ReleaseDate != "2026-09-30" {
		t.Fatalf("metadata = %q/%q/%q", cat.SchemaVersion, cat.ReleaseVersion, cat.ReleaseDate)
	}
	want := []Control{
		{SCFID: "GOV-01", Family: "GOV", Title: "Cybersecurity & Data Protection Governance Program", Description: "Mechanisms exist to facilitate governance."},
		{SCFID: "IAC-37", Family: "IAC", Title: "Multi-Factor Authentication (MFA)", Description: "Mechanisms exist to require MFA."},
	}
	if len(cat.Controls) != len(want) {
		t.Fatalf("controls = %+v", cat.Controls)
	}
	for i := range want {
		if cat.Controls[i].SCFID != want[i].SCFID || cat.Controls[i].Family != want[i].Family ||
			cat.Controls[i].Title != want[i].Title || cat.Controls[i].Description != want[i].Description {
			t.Fatalf("control[%d] = %+v; want %+v", i, cat.Controls[i], want[i])
		}
	}
}

func TestLoadWorkbook_ReleaseOverrideAndDomainFallback(t *testing.T) {
	t.Parallel()
	sheets := map[string][][]interface{}{
		"SCF Controls": {publishedHeaders, {"Governance", "Program", "GOV", "desc", ""}},
	}
	path := writeWorkbook(t, sheets, []string{"SCF Controls"})
	if _, err := LoadWorkbook(path, WorkbookOptions{}); err == nil || !strings.Contains(err.Error(), "--release-version") {
		t.Fatalf("err = %v; want a --release-version hint", err)
	}
	cat, err := LoadWorkbook(path, WorkbookOptions{ReleaseVersion: "2026.3"})
	if err != nil {
		t.Fatalf("LoadWorkbook: %v", err)
	}
	if cat.ReleaseVersion != "2026.3" || cat.Controls[0].Family != "Governance" {
		t.Fatalf("catalog = %+v", cat)
	}
}

func TestLoadWorkbook_Errors(t *testing.T) {
	t.Parallel()
	dupSheets, dupOrder := publishedLayout(
		[]interface{}{"D", "T", "GOV-01", "d", ""},
		[]interface{}{"D", "T", "GOV-01", "d", ""},
	)
	noTitleSheets, noTitleOrder := publishedLayout([]interface{}{"D", "", "GOV-01", "d", ""})
	cases := []struct {
		name   string
		sheets map[string][][]interface{}
		order  []string
		want   string
	}{
		{"no control sheet", map[string][][]interface{}{"Other": {{"x"}}}, []string{"Other"}, `missing an "SCF <version>" sheet`},
		{"header only", map[string][][]interface{}{"SCF 2026.3": {publishedHeaders}}, []string{"SCF 2026.3"}, "no data rows"},
		{"missing description", map[string][][]interface{}{"SCF 2026.3": {{"SCF Domain", "SCF Control", "SCF #"}, {"D", "T", "GOV-01"}}}, []string{"SCF 2026.3"}, `missing required "Secure Controls Framework (SCF) Control Description"`},
		{"duplicate id", dupSheets, dupOrder, "repeats SCF # GOV-01 from row 2"},
		{"missing title", noTitleSheets, noTitleOrder, "missing title"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			_, err := LoadWorkbook(writeWorkbook(t, tc.sheets, tc.order), WorkbookOptions{})
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("err = %v; want %q", err, tc.want)
			}
		})
	}
	if _, err := LoadWorkbook(filepath.Join(t.TempDir(), "absent.xlsx"), WorkbookOptions{}); err == nil {
		t.Fatal("missing file: want error")
	}
}
