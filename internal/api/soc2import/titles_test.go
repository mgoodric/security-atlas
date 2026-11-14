package soc2import_test

import (
	"errors"
	"path/filepath"
	"strings"
	"testing"

	"github.com/xuri/excelize/v2"

	"github.com/mgoodric/security-atlas/internal/api/soc2import"
)

// testColumn is the framework column header writeSingleColumnWorkbook uses.
const testColumn = "Framework Column"

// writeSingleColumnWorkbook builds a wide SCF workbook with one framework
// column named testColumn. rows pair an SCF anchor with the column's cell text.
// It has no Focal Documents sheet, so callers pass the column and the slug.
func writeSingleColumnWorkbook(t *testing.T, rows [][2]string) string {
	t.Helper()
	f := excelize.NewFile()
	mustSetSheetName(t, f, "Sheet1", "SCF 2026.3")
	data := [][]interface{}{{"SCF #", testColumn}}
	for _, r := range rows {
		data = append(data, []interface{}{r[0], r[1]})
	}
	mustSetRows(t, f, "SCF 2026.3", data)
	path := filepath.Join(t.TempDir(), "scf-single.xlsx")
	if err := f.SaveAs(path); err != nil {
		t.Fatalf("SaveAs: %v", err)
	}
	return path
}

func requirementByCode(t *testing.T, cw *soc2import.Crosswalk, code string) soc2import.Requirement {
	t.Helper()
	for _, r := range cw.Requirements {
		if r.Code == code {
			return r
		}
	}
	t.Fatalf("requirement %s not loaded", code)
	return soc2import.Requirement{}
}

func bundledSOC2(t *testing.T) *soc2import.Crosswalk {
	t.Helper()
	cw, err := soc2import.Load(filepath.Join("..", "..", "..", "data", "crosswalks", "soc2-tsc-2017.yaml"))
	if err != nil {
		t.Fatalf("Load bundled soc2: %v", err)
	}
	return cw
}

// #1730: a workbook import for a slug a bundled crosswalk covers takes that
// crosswalk's titles and bodies. A point of focus takes its parent's title.
func TestLoadSCFWorkbook_TitlesFromBundledYAML(t *testing.T) {
	t.Parallel()
	xlsx := writeSingleColumnWorkbook(t, [][2]string{
		{"IAC-01", "CC6.1\nCC6.1-POF1"},
		{"IAC-02", "CC6.2"},
	})
	cw, err := soc2import.LoadSCFWorkbook(xlsx, soc2import.SCFWorkbookOptions{
		FrameworkSlug: "soc2", FrameworkColumns: []string{testColumn},
		FrameworkVersion: "2017",
	})
	if err != nil {
		t.Fatalf("LoadSCFWorkbook: %v", err)
	}
	want := requirementByCode(t, bundledSOC2(t), "CC6.1")
	got := requirementByCode(t, cw, "CC6.1")
	if got.Title != want.Title || got.Body != want.Body {
		t.Fatalf("CC6.1 = %q / %q; want bundled %q / %q", got.Title, got.Body, want.Title, want.Body)
	}
	pof := requirementByCode(t, cw, "CC6.1-POF1")
	if pof.Title != want.Title || pof.Body != "" {
		t.Fatalf("CC6.1-POF1 = %q / %q; want parent title %q and no body", pof.Title, pof.Body, want.Title)
	}
	if n := soc2import.CodeOnlyTitleCount(cw); n != 0 {
		t.Fatalf("CodeOnlyTitleCount = %d; want 0", n)
	}
}

// #1730: --titles takes precedence over the bundled crosswalk, code by code,
// and its mappings are ignored.
func TestLoadSCFWorkbook_TitlesFileOverridesBundled(t *testing.T) {
	t.Parallel()
	xlsx := writeSingleColumnWorkbook(t, [][2]string{
		{"IAC-01", "CC6.1\nCC6.2"},
	})
	titles := writeTemp(t, `
schema_version: "1.0"
framework_slug: "anything"
requirements:
  - code: CC6.1
    title: "Custom access title"
    body: "Custom access body."
mappings:
  - requirement_code: CC6.1
    scf_anchor: NOT-AN-ANCHOR
`)
	cw, err := soc2import.LoadSCFWorkbook(xlsx, soc2import.SCFWorkbookOptions{
		FrameworkSlug: "soc2", FrameworkColumns: []string{testColumn},
		FrameworkVersion: "2017",
		TitlesPath:       titles,
	})
	if err != nil {
		t.Fatalf("LoadSCFWorkbook: %v", err)
	}
	if got := requirementByCode(t, cw, "CC6.1"); got.Title != "Custom access title" || got.Body != "Custom access body." {
		t.Fatalf("CC6.1 = %q / %q; want the --titles entry", got.Title, got.Body)
	}
	want := requirementByCode(t, bundledSOC2(t), "CC6.2")
	if got := requirementByCode(t, cw, "CC6.2"); got.Title != want.Title {
		t.Fatalf("CC6.2 title = %q; want bundled %q", got.Title, want.Title)
	}
	for _, m := range cw.Mappings {
		if m.SCFAnchor == "NOT-AN-ANCHOR" {
			t.Fatalf("--titles mappings must be ignored: %+v", m)
		}
	}
}

// #1730: with no bundled crosswalk and no --titles, the load fails and names
// every code-only requirement; with AllowCodeTitles it loads and the count is
// reported.
func TestLoadSCFWorkbook_CodeOnlyTitlesGate(t *testing.T) {
	t.Parallel()
	xlsx := writeSingleColumnWorkbook(t, [][2]string{
		{"GOV-01", "EX-1\nEX-2"},
	})
	opts := soc2import.SCFWorkbookOptions{FrameworkSlug: "no-bundled-framework", FrameworkColumns: []string{testColumn}, FrameworkVersion: "1"}

	_, err := soc2import.LoadSCFWorkbook(xlsx, opts)
	var codeOnly *soc2import.CodeOnlyTitlesError
	if !errors.As(err, &codeOnly) {
		t.Fatalf("err = %v; want CodeOnlyTitlesError", err)
	}
	if got := strings.Join(codeOnly.Codes, ","); got != "EX-1,EX-2" {
		t.Fatalf("code-only codes = %s; want EX-1,EX-2", got)
	}
	if !strings.Contains(err.Error(), "--allow-code-titles") || !strings.Contains(err.Error(), "EX-1, EX-2") {
		t.Fatalf("error should list the codes and name the flag: %v", err)
	}

	opts.AllowCodeTitles = true
	cw, err := soc2import.LoadSCFWorkbook(xlsx, opts)
	if err != nil {
		t.Fatalf("LoadSCFWorkbook with AllowCodeTitles: %v", err)
	}
	if n := soc2import.CodeOnlyTitleCount(cw); n != 2 {
		t.Fatalf("CodeOnlyTitleCount = %d; want 2", n)
	}
}

// #1730: a point of focus whose parent no source covers is code-only too.
func TestLoadSCFWorkbook_PointOfFocusWithoutParentTitleIsCodeOnly(t *testing.T) {
	t.Parallel()
	xlsx := writeSingleColumnWorkbook(t, [][2]string{
		{"GOV-01", "EX-1-POF1"},
	})
	_, err := soc2import.LoadSCFWorkbook(xlsx, soc2import.SCFWorkbookOptions{FrameworkSlug: "no-bundled-framework", FrameworkColumns: []string{testColumn}, FrameworkVersion: "1"})
	var codeOnly *soc2import.CodeOnlyTitlesError
	if !errors.As(err, &codeOnly) || len(codeOnly.Codes) != 1 || codeOnly.Codes[0] != "EX-1-POF1" {
		t.Fatalf("err = %v; want CodeOnlyTitlesError for EX-1-POF1", err)
	}
}

// #1730: the OLIR STRM path resolves titles the same way.
func TestLoadSCFWorkbook_OLIRTitlesFile(t *testing.T) {
	t.Parallel()
	xlsx := writeOLIRWorkbook(t, "Subset Of", "10")
	titles := writeTemp(t, `
requirements:
  - code: AC-01
    title: "Policy and procedures"
`)
	cw, err := soc2import.LoadSCFWorkbook(xlsx, soc2import.SCFWorkbookOptions{
		FrameworkSlug: "nist-800-53",
		TitlesPath:    titles,
	})
	if err != nil {
		t.Fatalf("LoadSCFWorkbook: %v", err)
	}
	if got := requirementByCode(t, cw, "AC-01"); got.Title != "Policy and procedures" {
		t.Fatalf("AC-01 title = %q", got.Title)
	}
}

func TestLoadSCFWorkbook_TitlesFileErrors(t *testing.T) {
	t.Parallel()
	xlsx := writeSingleColumnWorkbook(t, [][2]string{{"IAC-01", "CC6.1"}})
	cases := map[string]string{
		"missing file":  filepath.Join(t.TempDir(), "absent.yaml"),
		"no entries":    writeTemp(t, `requirements: []`),
		"missing title": writeTemp(t, "requirements:\n  - code: CC6.1\n"),
		"not yaml":      writeTemp(t, "requirements: [unclosed"),
	}
	for name, path := range cases {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			_, err := soc2import.LoadSCFWorkbook(xlsx, soc2import.SCFWorkbookOptions{
				FrameworkSlug: "soc2", FrameworkColumns: []string{testColumn},
				FrameworkVersion: "2017",
				TitlesPath:       path,
			})
			if err == nil || !strings.Contains(err.Error(), "titles") {
				t.Fatalf("err = %v; want a titles error", err)
			}
		})
	}
}
