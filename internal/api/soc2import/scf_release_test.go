package soc2import_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mgoodric/security-atlas/internal/api/soc2import"
)

// #1771: the loaders carry the crosswalk's SCF release. The binding itself
// is covered by scf_release_integration_test.go.

func TestLoad_ReadsSCFRelease(t *testing.T) {
	t.Parallel()
	path := filepath.Join(t.TempDir(), "cw.yaml")
	yaml := `schema_version: "1.0"
framework_slug: x
framework_version: "1"
source_attribution: community_draft
scf_release: "2026.3"
requirements:
  - code: R-1
    title: One
mappings:
  - requirement_code: R-1
    scf_anchor: GOV-01
    relationship_type: equal
    strength: 1
    rationale: r
`
	if err := os.WriteFile(path, []byte(yaml), 0o600); err != nil {
		t.Fatalf("write: %v", err)
	}
	cw, err := soc2import.Load(path)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cw.SCFRelease != "2026.3" {
		t.Fatalf("SCFRelease = %q; want 2026.3", cw.SCFRelease)
	}
}

func TestLoadSCFWorkbook_SCFReleaseFromSheetName(t *testing.T) {
	t.Parallel()
	xlsx := writePublishedLayoutSCFWorkbook(t)
	opts := soc2import.SCFWorkbookOptions{
		FrameworkColumns: []string{"Example\nStandard\n2026"},
		AllowCodeTitles:  true,
	}

	cw, err := soc2import.LoadSCFWorkbook(xlsx, opts)
	if err != nil {
		t.Fatalf("LoadSCFWorkbook: %v", err)
	}
	if cw.SCFRelease != "2026.3" || !cw.SCFReleaseFromSheet {
		t.Fatalf("SCFRelease = %q, from sheet = %v; want 2026.3 from sheet \"SCF 2026.3\"", cw.SCFRelease, cw.SCFReleaseFromSheet)
	}

	// An option that agrees with the sheet is accepted, and makes the
	// release explicit (strict at import).
	opts.SCFRelease = "2026.3"
	cw, err = soc2import.LoadSCFWorkbook(xlsx, opts)
	if err != nil {
		t.Fatalf("agreeing --scf-release: %v", err)
	}
	if cw.SCFReleaseFromSheet {
		t.Fatal("SCFReleaseFromSheet = true with --scf-release given; want explicit")
	}

	// One that contradicts it is refused.
	opts.SCFRelease = "2025.2"
	_, err = soc2import.LoadSCFWorkbook(xlsx, opts)
	if err == nil || !strings.Contains(err.Error(), "contradicts") {
		t.Fatalf("contradicting --scf-release: err = %v; want a contradiction error", err)
	}
}

// An OLIR STRM workbook has no "SCF <release>" sheet, so its release is the
// option, and without one it declares none.
func TestLoadSCFWorkbook_OLIRTakesSCFReleaseOption(t *testing.T) {
	t.Parallel()
	xlsx := writeOLIRWorkbook(t, "Subset Of", "10")
	opts := soc2import.SCFWorkbookOptions{FrameworkSlug: "nist-800-53", AllowCodeTitles: true}

	cw, err := soc2import.LoadSCFWorkbook(xlsx, opts)
	if err != nil {
		t.Fatalf("LoadSCFWorkbook: %v", err)
	}
	if cw.SCFRelease != "" {
		t.Fatalf("SCFRelease = %q; want none without the option", cw.SCFRelease)
	}

	opts.SCFRelease = "2026.2"
	cw, err = soc2import.LoadSCFWorkbook(xlsx, opts)
	if err != nil {
		t.Fatalf("LoadSCFWorkbook: %v", err)
	}
	if cw.SCFRelease != "2026.2" {
		t.Fatalf("SCFRelease = %q; want 2026.2 from the option", cw.SCFRelease)
	}
}
