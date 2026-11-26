//go:build integration

package soc2import_test

import (
	"context"
	"reflect"
	"strings"
	"testing"

	"github.com/mgoodric/security-atlas/internal/api/soc2import"
	"github.com/mgoodric/security-atlas/internal/dbtest"
)

const pofLegacySlug = "pof-legacy-test"

// loadRolledUpWorkbook loads a SOC 2 workbook naming CC1.1 and two of its
// points of focus, which the loader rolls up onto CC1.1 (#1731).
func loadRolledUpWorkbook(t *testing.T, version string) *soc2import.Crosswalk {
	t.Helper()
	cw, err := soc2import.LoadSCFWorkbook(writeSOC2WideWorkbook(t, map[string]string{
		"AST-01": "CC1.1",
		"BCD-04": "CC1.1-POF1",
		"IAC-01": "CC1.1-POF2",
	}), soc2import.SCFWorkbookOptions{FrameworkSlug: pofLegacySlug, FrameworkVersion: version, AllowCodeTitles: true})
	if err != nil {
		t.Fatalf("LoadSCFWorkbook: %v", err)
	}
	return inSample(cw)
}

// preRollUp rebuilds cw the way the workbook importer wrote it before #1731:
// each point of focus its own requirement with its own edge, under the same
// crosswalk source, so the retire pass owns those edges.
func preRollUp(cw *soc2import.Crosswalk) *soc2import.Crosswalk {
	legacy := *cw
	parent := cw.Requirements[0]
	parent.PointsOfFocus = nil
	legacy.Requirements = []soc2import.Requirement{
		parent,
		{Code: "CC1.1-POF1", Title: "CC1.1-POF1"},
		{Code: "CC1.1-POF2", Title: "CC1.1-POF2"},
	}
	edge := cw.Mappings[0]
	legacy.Mappings = nil
	for code, anchor := range map[string]string{"CC1.1": "AST-01", "CC1.1-POF1": "BCD-04", "CC1.1-POF2": "IAC-01"} {
		m := edge
		m.RequirementCode, m.SCFAnchor, m.Rationale = code, anchor, "pre-roll-up edge"
		legacy.Mappings = append(legacy.Mappings, m)
	}
	legacy.PointsOfFocusRollUp = soc2import.PointsOfFocusRollUp{}
	return &legacy
}

// #2026: a workbook import into a version that already holds -POF<n>
// requirement rows from before the roll-up reports them, and how many of
// their workbook-written edges the retire pass (#1971) dropped, and points to
// a new framework version. A new version has none. CC1.1, stored without
// points of focus, now differs only in points_of_focus, and the report says
// so rather than "title or body".
func TestImport_ReportsLegacyPointsOfFocusRows(t *testing.T) {
	pool := dbtest.NewMigratePool(t)
	ctx := context.Background()
	resetFrameworkCatalog(t, pool, pofLegacySlug)
	t.Cleanup(func() { resetFrameworkCatalog(t, pool, pofLegacySlug) })
	ensureSCFLoaded(t, pool)

	cw := loadRolledUpWorkbook(t, "2017")
	first, err := soc2import.Import(ctx, pool, preRollUp(cw))
	if err != nil {
		t.Fatalf("pre-roll-up Import: %v", err)
	}
	if len(first.LegacyPointsOfFocus) != 0 {
		t.Fatalf("a crosswalk listing its point-of-focus rows reported them as legacy: %v", first.LegacyPointsOfFocus)
	}

	report, err := soc2import.Import(ctx, pool, cw)
	if err != nil {
		t.Fatalf("rolled-up Import: %v", err)
	}
	if want := []string{"CC1.1-POF1", "CC1.1-POF2"}; !reflect.DeepEqual(report.LegacyPointsOfFocus, want) {
		t.Fatalf("LegacyPointsOfFocus = %v; want %v", report.LegacyPointsOfFocus, want)
	}
	if report.LegacyPointsOfFocusEdgesRetired != 2 {
		t.Fatalf("LegacyPointsOfFocusEdgesRetired = %d; want 2 (the workbook-written edges on the -POF rows); retired = %+v",
			report.LegacyPointsOfFocusEdgesRetired, report.EdgesRetired)
	}
	w := report.LegacyPointsOfFocusWarning()
	for _, want := range []string{"2017", "CC1.1-POF1, CC1.1-POF2", "retired 2 edge(s)", "new --framework-version"} {
		if !strings.Contains(w, want) {
			t.Fatalf("warning %q does not contain %q", w, want)
		}
	}
	if got := report.RequirementsDifferedFields["CC1.1"]; !reflect.DeepEqual(got, []string{"points_of_focus"}) {
		t.Fatalf("RequirementsDifferedFields[CC1.1] = %v; want [points_of_focus]", got)
	}

	fresh, err := soc2import.Import(ctx, pool, loadRolledUpWorkbook(t, "2017-rollup"))
	if err != nil {
		t.Fatalf("Import into a new version: %v", err)
	}
	if len(fresh.LegacyPointsOfFocus) != 0 || fresh.LegacyPointsOfFocusWarning() != "" {
		t.Fatalf("a new version reported legacy rows: %v", fresh.LegacyPointsOfFocus)
	}
}

// #2026: a point of focus naming an SCF anchor the release does not have
// fails the import, and nothing is written.
func TestImport_RejectsPointOfFocusWithUnknownAnchor(t *testing.T) {
	pool := dbtest.NewMigratePool(t)
	ctx := context.Background()
	resetFrameworkCatalog(t, pool, pofLegacySlug+"-anchor")
	t.Cleanup(func() { resetFrameworkCatalog(t, pool, pofLegacySlug+"-anchor") })
	ensureSCFLoaded(t, pool)

	cw := loadRolledUpWorkbook(t, "2017")
	cw.FrameworkSlug = pofLegacySlug + "-anchor"
	cw.Requirements[0].PointsOfFocus = append(cw.Requirements[0].PointsOfFocus,
		soc2import.PointOfFocus{Code: "CC1.1-POF9", SCFAnchors: []string{"NOPE-99"}})
	_, err := soc2import.Import(ctx, pool, cw)
	if err == nil || !strings.Contains(err.Error(), "point of focus CC1.1-POF9") || !strings.Contains(err.Error(), "NOPE-99") {
		t.Fatalf("Import err = %v; want the point of focus and its unknown anchor named", err)
	}
	var n int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM frameworks WHERE slug = $1 AND tenant_id IS NULL`, cw.FrameworkSlug).Scan(&n); err != nil {
		t.Fatalf("count frameworks: %v", err)
	}
	if n != 0 {
		t.Fatalf("a failed import left %d framework row(s)", n)
	}
}

// #2026: the database holds points_of_focus to a JSON array.
func TestFrameworkRequirements_PointsOfFocusMustBeArray(t *testing.T) {
	pool := dbtest.NewMigratePool(t)
	ctx := context.Background()
	resetFrameworkCatalog(t, pool, pofLegacySlug+"-check")
	t.Cleanup(func() { resetFrameworkCatalog(t, pool, pofLegacySlug+"-check") })
	ensureSCFLoaded(t, pool)

	cw := loadRolledUpWorkbook(t, "2017")
	cw.FrameworkSlug = pofLegacySlug + "-check"
	report, err := soc2import.Import(ctx, pool, cw)
	if err != nil {
		t.Fatalf("Import: %v", err)
	}
	for _, bad := range []string{`{"code": "CC1.1-POF1"}`, `"CC1.1-POF1"`, `null`, `1`} {
		_, err := pool.Exec(ctx, `
			INSERT INTO framework_requirements (id, framework_version_id, code, title, body, points_of_focus)
			VALUES (gen_random_uuid(), $1, 'CHECK-1', 'check', '', $2::jsonb)`, report.FrameworkVersionID, bad)
		if err == nil || !strings.Contains(err.Error(), "framework_requirements_points_of_focus_array") {
			t.Fatalf("insert points_of_focus=%s: err = %v; want the array CHECK to reject it", bad, err)
		}
	}
	if _, err := pool.Exec(ctx, `
		INSERT INTO framework_requirements (id, framework_version_id, code, title, body, points_of_focus)
		VALUES (gen_random_uuid(), $1, 'CHECK-2', 'check', '', '[]'::jsonb)`, report.FrameworkVersionID); err != nil {
		t.Fatalf("insert points_of_focus=[]: %v", err)
	}
}
