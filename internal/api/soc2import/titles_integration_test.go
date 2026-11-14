//go:build integration

package soc2import_test

import (
	"context"
	"strings"
	"testing"

	"github.com/mgoodric/security-atlas/internal/api/soc2import"
	"github.com/mgoodric/security-atlas/internal/dbtest"
)

const frozenTitlesSlug = "frozen-titles-example"

type storedRequirement struct{ title, body string }

// #1730: a workbook import into an existing frozen version whose titles
// differ succeeds, adds its new edges, keeps every stored title and body
// byte-for-byte, and reports the codes that differed. Before #1730 the
// immutability trigger rejected the UPDATE and rolled back the whole import.
func TestImport_FrozenVersionKeepsTitlesAndAddsEdges(t *testing.T) {
	pool := dbtest.NewMigratePool(t)
	resetFrameworkCatalog(t, pool, frozenTitlesSlug)
	t.Cleanup(func() { resetFrameworkCatalog(t, pool, frozenTitlesSlug) })
	ensureSCFLoaded(t, pool)
	ctx := context.Background()

	load := func(rows [][2]string, titlesYAML string) *soc2import.Crosswalk {
		t.Helper()
		cw, err := soc2import.LoadSCFWorkbook(writeSingleColumnWorkbook(t, rows), soc2import.SCFWorkbookOptions{
			FrameworkColumns: []string{testColumn},
			FrameworkSlug:    frozenTitlesSlug,
			FrameworkVersion: "1",
			TitlesPath:       writeTemp(t, titlesYAML),
		})
		if err != nil {
			t.Fatalf("LoadSCFWorkbook: %v", err)
		}
		return cw
	}

	first := load([][2]string{{"GOV-01", "EX-1\nEX-2"}}, `
requirements:
  - code: EX-1
    title: "Original one"
    body: "Original body one."
  - code: EX-2
    title: "Original two"
    body: "Original body two."
`)
	if _, err := soc2import.Import(ctx, pool, first); err != nil {
		t.Fatalf("first Import: %v", err)
	}

	read := func() map[string]storedRequirement {
		t.Helper()
		rows, err := pool.Query(ctx, `
			SELECT fr.code, fr.title, fr.body
			FROM framework_requirements fr
			JOIN framework_versions fv ON fv.id = fr.framework_version_id
			JOIN frameworks f ON f.id = fv.framework_id
			WHERE f.slug = $1 AND f.tenant_id IS NULL`, frozenTitlesSlug)
		if err != nil {
			t.Fatalf("query requirements: %v", err)
		}
		defer rows.Close()
		out := map[string]storedRequirement{}
		for rows.Next() {
			var code string
			var r storedRequirement
			if err := rows.Scan(&code, &r.title, &r.body); err != nil {
				t.Fatalf("scan: %v", err)
			}
			out[code] = r
		}
		if err := rows.Err(); err != nil {
			t.Fatalf("rows: %v", err)
		}
		return out
	}
	before := read()

	// The re-import changes EX-1's title, keeps EX-2's, and adds an edge.
	second := load([][2]string{{"GOV-01", "EX-1\nEX-2"}, {"IAC-01", "EX-2"}}, `
requirements:
  - code: EX-1
    title: "Retitled one"
    body: "Rewritten body one."
  - code: EX-2
    title: "Original two"
    body: "Original body two."
`)
	report, err := soc2import.Import(ctx, pool, second)
	if err != nil {
		t.Fatalf("re-import into frozen version: %v", err)
	}
	if got := strings.Join(report.RequirementsDiffered, ","); got != "EX-1" {
		t.Fatalf("RequirementsDiffered = %q; want EX-1", got)
	}
	if report.RequirementsUpdated != 0 || report.RequirementsUnchanged != 1 {
		t.Fatalf("requirements updated/unchanged = %d/%d; want 0/1", report.RequirementsUpdated, report.RequirementsUnchanged)
	}
	if report.EdgesCreated != 1 || report.EdgesUnchanged != 2 {
		t.Fatalf("edges created/unchanged = %d/%d; want 1/2", report.EdgesCreated, report.EdgesUnchanged)
	}

	after := read()
	if len(after) != len(before) {
		t.Fatalf("requirement count changed: %d -> %d", len(before), len(after))
	}
	for code, b := range before {
		if a := after[code]; a != b {
			t.Fatalf("requirement %s changed in a frozen version: %+v -> %+v", code, b, a)
		}
	}

	var edges int
	if err := pool.QueryRow(ctx, `
		SELECT count(*)
		FROM fw_to_scf_edges e
		JOIN framework_requirements fr ON fr.id = e.framework_requirement_id
		JOIN framework_versions fv ON fv.id = fr.framework_version_id
		JOIN frameworks f ON f.id = fv.framework_id
		WHERE f.slug = $1`, frozenTitlesSlug).Scan(&edges); err != nil {
		t.Fatalf("edge count: %v", err)
	}
	if edges != 3 {
		t.Fatalf("edges = %d; want 3 (the new IAC-01 edge landed)", edges)
	}
}
