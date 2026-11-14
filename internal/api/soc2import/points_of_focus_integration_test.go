//go:build integration

package soc2import_test

import (
	"context"
	"encoding/json"
	"reflect"
	"testing"

	"github.com/mgoodric/security-atlas/internal/api/soc2import"
	"github.com/mgoodric/security-atlas/internal/dbtest"
)

const pofTestSlug = "pof-rollup-test"

// #1731: a workbook whose SOC 2 column holds CC1.1, two of its points of
// focus and a point of focus of the otherwise-absent CC2.1 imports as two
// requirements. requirement_count counts criteria, the rolled-up edges land
// on the criterion, and the points of focus are stored on it at insert time.
// A re-import is unchanged, which a frozen version requires.
func TestImport_WorkbookPointsOfFocusRollUp(t *testing.T) {
	pool := dbtest.NewMigratePool(t)
	ctx := context.Background()
	resetFrameworkCatalog(t, pool, pofTestSlug)
	t.Cleanup(func() { resetFrameworkCatalog(t, pool, pofTestSlug) })
	ensureSCFLoaded(t, pool)

	xlsx := writeSOC2WideWorkbook(t, map[string]string{
		"AST-01": "CC1.1",
		"BCD-02": "CC1.1-POF1\nCC2.1-POF1",
		"IAC-01": "CC1.1-POF2",
	})
	cw, err := soc2import.LoadSCFWorkbook(xlsx, soc2import.SCFWorkbookOptions{FrameworkSlug: pofTestSlug, FrameworkVersion: "2017", AllowCodeTitles: true})
	if err != nil {
		t.Fatalf("LoadSCFWorkbook: %v", err)
	}
	report, err := soc2import.Import(ctx, pool, cw)
	if err != nil {
		t.Fatalf("Import: %v", err)
	}
	if report.RequirementsCreated != 2 || report.EdgesCreated != 4 {
		t.Fatalf("report = %+v; want 2 requirements, 4 edges", report)
	}

	var requirementCount, rows int
	if err := pool.QueryRow(ctx, `
		SELECT fv.requirement_count, (SELECT count(*) FROM framework_requirements fr WHERE fr.framework_version_id = fv.id)
		FROM framework_versions fv JOIN frameworks f ON f.id = fv.framework_id
		WHERE f.slug = $1 AND fv.version = '2017'`, pofTestSlug).Scan(&requirementCount, &rows); err != nil {
		t.Fatalf("read requirement_count: %v", err)
	}
	if requirementCount != 2 || rows != 2 {
		t.Fatalf("requirement_count = %d, rows = %d; want 2 criteria, not criteria plus points of focus", requirementCount, rows)
	}

	var stored []byte
	var edges int
	if err := pool.QueryRow(ctx, `
		SELECT fr.points_of_focus, (SELECT count(*) FROM fw_to_scf_edges e WHERE e.framework_requirement_id = fr.id)
		FROM framework_requirements fr
		JOIN framework_versions fv ON fv.id = fr.framework_version_id
		JOIN frameworks f ON f.id = fv.framework_id
		WHERE f.slug = $1 AND fr.code = 'CC1.1'`, pofTestSlug).Scan(&stored, &edges); err != nil {
		t.Fatalf("read CC1.1: %v", err)
	}
	var got []soc2import.PointOfFocus
	if err := json.Unmarshal(stored, &got); err != nil {
		t.Fatalf("decode points_of_focus %s: %v", stored, err)
	}
	want := []soc2import.PointOfFocus{
		{Code: "CC1.1-POF1", SCFAnchors: []string{"BCD-02"}},
		{Code: "CC1.1-POF2", SCFAnchors: []string{"IAC-01"}},
	}
	if !reflect.DeepEqual(got, want) || edges != 3 {
		t.Fatalf("CC1.1 points_of_focus = %+v, edges = %d; want %+v and 3 edges", got, edges, want)
	}

	again, err := soc2import.Import(ctx, pool, cw)
	if err != nil {
		t.Fatalf("re-Import: %v", err)
	}
	if again.RequirementsUnchanged != 2 || again.RequirementsUpdated != 0 || again.EdgesUnchanged != 4 {
		t.Fatalf("re-import report = %+v; want everything unchanged", again)
	}
}
