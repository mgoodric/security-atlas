//go:build integration

package soc2import_test

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/xuri/excelize/v2"

	"github.com/mgoodric/security-atlas/internal/api/soc2import"
	"github.com/mgoodric/security-atlas/internal/dbtest"
	"github.com/mgoodric/security-atlas/internal/scfrelease"
	"github.com/mgoodric/security-atlas/internal/scfrelease/scfreleasetest"
)

// #1771: crosswalk imports bind their scf_anchor codes within the SCF
// release they declare. scfreleasetest.LoadAB loads release A (legacy) and
// release B (current); AST-04 is in both with different meanings, and
// PRM-07 is in A only.

const releaseTestSlug = "test-1771-crosswalk"

// releaseCrosswalkYAML is a two-mapping crosswalk naming AST-04 and PRM-07,
// with scf_release set to release ("" leaves the key out).
func releaseCrosswalkYAML(release string) string {
	y := `schema_version: "1.0"
framework_slug: ` + releaseTestSlug + `
framework_name: Release test framework
framework_issuer: Test
framework_version: "1"
source_attribution: community_draft
`
	if release != "" {
		y += "scf_release: " + release + "\n"
	}
	return y + `requirements:
  - code: R-1
    title: Requirement one
  - code: R-2
    title: Requirement two
mappings:
  - requirement_code: R-1
    scf_anchor: AST-04
    relationship_type: intersects_with
    strength: 0.5
    rationale: test
  - requirement_code: R-2
    scf_anchor: PRM-07
    relationship_type: intersects_with
    strength: 0.5
    rationale: test
`
}

func loadReleaseCrosswalk(t *testing.T, yaml string) *soc2import.Crosswalk {
	t.Helper()
	path := filepath.Join(t.TempDir(), "crosswalk.yaml")
	if err := os.WriteFile(path, []byte(yaml), 0o600); err != nil {
		t.Fatalf("write crosswalk: %v", err)
	}
	cw, err := soc2import.Load(path)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	return cw
}

// edgeReleases maps each of the test framework's requirement codes to the
// "<release>/<scf_id>" of the anchor its edge points at.
func edgeReleases(t *testing.T, pool *pgxpool.Pool) map[string]string {
	t.Helper()
	rows, err := pool.Query(context.Background(), `
		SELECT fr.code, sv.version || '/' || a.scf_id
		FROM framework_requirements fr
		JOIN framework_versions fv ON fv.id = fr.framework_version_id
		JOIN frameworks f ON f.id = fv.framework_id
		JOIN fw_to_scf_edges e ON e.framework_requirement_id = fr.id
		JOIN scf_anchors a ON a.id = e.scf_anchor_id
		JOIN framework_versions sv ON sv.id = a.framework_version_id
		WHERE f.slug = $1 AND f.tenant_id IS NULL`, releaseTestSlug)
	if err != nil {
		t.Fatalf("read edges: %v", err)
	}
	defer rows.Close()
	out := map[string]string{}
	for rows.Next() {
		var code, bound string
		if err := rows.Scan(&code, &bound); err != nil {
			t.Fatalf("scan edge: %v", err)
		}
		out[code] = bound
	}
	return out
}

// setupReleases loads the A/B fixtures and removes the test framework both
// before the test and before LoadAB's own cleanup (which deletes A and B).
func setupReleases(t *testing.T) *pgxpool.Pool {
	t.Helper()
	pool := dbtest.NewMigratePool(t)
	scfreleasetest.LoadAB(t, pool)
	resetFrameworkCatalog(t, pool, releaseTestSlug)
	t.Cleanup(func() { resetFrameworkCatalog(t, pool, releaseTestSlug) })
	return pool
}

// A crosswalk YAML declaring release A binds its edges to A's anchors while
// B is current, including a code A has and B withdrew.
func TestImport_SCFRelease_YAMLBindsInDeclaredRelease(t *testing.T) {
	pool := setupReleases(t)

	report, err := soc2import.Import(context.Background(), pool, loadReleaseCrosswalk(t, releaseCrosswalkYAML(scfreleasetest.ReleaseA)))
	if err != nil {
		t.Fatalf("Import: %v", err)
	}
	if report.SCFRelease != scfreleasetest.ReleaseA || len(report.Warnings) != 0 {
		t.Fatalf("report release=%q warnings=%v; want %q and none", report.SCFRelease, report.Warnings, scfreleasetest.ReleaseA)
	}
	got := edgeReleases(t, pool)
	want := map[string]string{
		"R-1": scfreleasetest.ReleaseA + "/AST-04",
		"R-2": scfreleasetest.ReleaseA + "/PRM-07",
	}
	for code, w := range want {
		if got[code] != w {
			t.Errorf("edge %s bound to %q; want %q", code, got[code], w)
		}
	}
}

// An unknown release and a code missing from the declared release are the
// resolver's named errors, and the import writes nothing.
func TestImport_SCFRelease_NamedErrors(t *testing.T) {
	pool := setupReleases(t)
	ctx := context.Background()

	if _, err := soc2import.Import(ctx, pool, loadReleaseCrosswalk(t, releaseCrosswalkYAML("no-such-release"))); !errors.Is(err, scfrelease.ErrUnknownRelease) {
		t.Fatalf("unknown release: err = %v; want ErrUnknownRelease", err)
	}
	// PRM-07 is not in release B.
	if _, err := soc2import.Import(ctx, pool, loadReleaseCrosswalk(t, releaseCrosswalkYAML(scfreleasetest.ReleaseB))); !errors.Is(err, scfrelease.ErrCodeNotInRelease) {
		t.Fatalf("code not in release: err = %v; want ErrCodeNotInRelease", err)
	}
	var n int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM frameworks WHERE slug = $1 AND tenant_id IS NULL`, releaseTestSlug).Scan(&n); err != nil {
		t.Fatalf("count frameworks: %v", err)
	}
	if n != 0 {
		t.Fatalf("a refused import left %d framework rows", n)
	}
}

// With no scf_release, codes bind in the current release as before and the
// report carries the deprecation warning.
func TestImport_SCFRelease_AbsentWarnsAndBindsCurrent(t *testing.T) {
	pool := setupReleases(t)

	yaml := releaseCrosswalkYAML("")
	cw := loadReleaseCrosswalk(t, yaml)
	cw.Mappings = cw.Mappings[:1] // PRM-07 is not in current release B.
	cw.Requirements = cw.Requirements[:1]
	report, err := soc2import.Import(context.Background(), pool, cw)
	if err != nil {
		t.Fatalf("Import: %v", err)
	}
	if len(report.Warnings) != 1 || report.Warnings[0] != soc2import.SCFReleaseDeprecationWarning {
		t.Fatalf("Warnings = %v; want the deprecation warning", report.Warnings)
	}
	if got := edgeReleases(t, pool)["R-1"]; got != scfreleasetest.ReleaseB+"/AST-04" {
		t.Fatalf("edge R-1 bound to %q; want current release B's AST-04", got)
	}
}

// workbookRelease is a release label in the "yyyy.n" form an "SCF
// <release>" sheet name carries. The fixture labels are not in that form,
// so the test relabels release A to it for its duration.
const workbookRelease = "1771.1"

// A wide SCF workbook takes its release from its "SCF <release>" sheet and
// binds within it, while another release is current.
func TestImport_SCFRelease_WorkbookTakesReleaseFromSheetName(t *testing.T) {
	pool := setupReleases(t)
	ctx := context.Background()
	if _, err := pool.Exec(ctx, `UPDATE framework_versions SET version = $2 WHERE tenant_id IS NULL AND version = $1`, scfreleasetest.ReleaseA, workbookRelease); err != nil {
		t.Fatalf("relabel release A: %v", err)
	}
	// Registered after LoadAB's cleanup, so it runs first and LoadAB finds
	// release A under its own label again.
	t.Cleanup(func() {
		resetFrameworkCatalog(t, pool, releaseTestSlug)
		if _, err := pool.Exec(ctx, `UPDATE framework_versions SET version = $2 WHERE tenant_id IS NULL AND version = $1`, workbookRelease, scfreleasetest.ReleaseA); err != nil {
			t.Errorf("restore release A label: %v", err)
		}
	})

	f := excelize.NewFile()
	mustSetSheetName(t, f, "Sheet1", "Focal Documents")
	mustSetRows(t, f, "Focal Documents", [][]interface{}{
		{"Geography", "SCF Column Header", "Focal Document Identifier (FDI)", "Source", "Focal Document Name (FDN)"},
		{"General", "Release Test 1", releaseTestSlug, "Test", "Release test framework"},
	})
	sheet := "SCF " + workbookRelease
	if _, err := f.NewSheet(sheet); err != nil {
		t.Fatalf("NewSheet: %v", err)
	}
	mustSetRows(t, f, sheet, [][]interface{}{
		{"SCF Domain", "SCF Control", "SCF #", "Release Test 1"},
		{"Asset", "Diagrams", "AST-04", "R-1"},
		{"Project", "SDLC", "PRM-07", "R-2"},
	})
	path := filepath.Join(t.TempDir(), "scf-release.xlsx")
	if err := f.SaveAs(path); err != nil {
		t.Fatalf("SaveAs: %v", err)
	}

	cw, err := soc2import.LoadSCFWorkbook(path, soc2import.SCFWorkbookOptions{
		FrameworkSlug:    releaseTestSlug,
		FrameworkVersion: "1",
		AllowCodeTitles:  true,
	})
	if err != nil {
		t.Fatalf("LoadSCFWorkbook: %v", err)
	}
	if cw.SCFRelease != workbookRelease {
		t.Fatalf("SCFRelease = %q; want %q from the sheet name", cw.SCFRelease, workbookRelease)
	}
	if _, err := soc2import.Import(ctx, pool, cw); err != nil {
		t.Fatalf("Import: %v", err)
	}
	got := edgeReleases(t, pool)
	for code, w := range map[string]string{"R-1": workbookRelease + "/AST-04", "R-2": workbookRelease + "/PRM-07"} {
		if got[code] != w {
			t.Errorf("edge %s bound to %q; want %q", code, got[code], w)
		}
	}
}
