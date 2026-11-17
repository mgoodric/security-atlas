//go:build integration

package soc2import_test

import (
	"context"
	"os"
	"path/filepath"
	"slices"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/mgoodric/security-atlas/internal/api/soc2import"
	"github.com/mgoodric/security-atlas/internal/dbtest"
)

// Issue #2014: boot converges every bundled crosswalk the install already
// imported, not only SOC 2. The install here imported an older ISO crosswalk
// before #1971 (one mapping moved to another anchor, no crosswalk_source)
// and never imported PCI DSS. After converge the ISO edges match the bundled
// YAML, PCI stays absent, an approved remap edge and a tenant overlay edge
// survive, and a second converge changes nothing.

// copyBundled copies the named bundled crosswalks into a fresh directory, so
// the test converges only them. The base names are kept, so each import's
// crosswalk_source is the bundled file's.
func copyBundled(t *testing.T, names ...string) string {
	t.Helper()
	dir := t.TempDir()
	for _, n := range names {
		b, err := os.ReadFile(filepath.Join("..", "..", "..", "data", "crosswalks", n))
		if err != nil {
			t.Fatalf("read %s: %v", n, err)
		}
		if err := os.WriteFile(filepath.Join(dir, n), b, 0o600); err != nil {
			t.Fatalf("write %s: %v", n, err)
		}
	}
	return dir
}

const isoEdgesWhere = `
	FROM fw_to_scf_edges e
	JOIN framework_requirements fr ON fr.id = e.framework_requirement_id
	JOIN framework_versions fv ON fv.id = fr.framework_version_id
	JOIN frameworks f ON f.id = fv.framework_id
	JOIN scf_anchors a ON a.id = e.scf_anchor_id
	WHERE f.slug = 'iso27001' AND f.tenant_id IS NULL`

// liveYAMLEdges returns "code->scf_id" for the live ISO edges a YAML import
// owns: those stamped with a yaml: source or with none.
func liveYAMLEdges(t *testing.T, pool *pgxpool.Pool) []string {
	t.Helper()
	return queryStrings(t, pool, `SELECT fr.code || '->' || a.scf_id `+isoEdgesWhere+`
		AND e.retired_at IS NULL
		AND (e.crosswalk_source IS NULL OR e.crosswalk_source LIKE 'yaml:%')
		ORDER BY 1`)
}

func wantEdges(cw *soc2import.Crosswalk) []string {
	out := make([]string, 0, len(cw.Mappings))
	for _, m := range cw.Mappings {
		out = append(out, m.RequirementCode+"->"+m.SCFAnchor)
	}
	slices.Sort(out)
	return slices.Compact(out)
}

// edgeSnapshot is every ISO catalog edge and every overlay edge with the
// columns an import could change, for the idempotency check.
func edgeSnapshot(t *testing.T, pool *pgxpool.Pool) []string {
	t.Helper()
	catalog := queryStrings(t, pool, `SELECT e.id::text || ' ' || fr.code || '->' || a.scf_id || ' ' ||
			e.relationship_type::text || ' ' || e.strength::text || ' ' || e.source_attribution::text || ' ' ||
			coalesce(e.crosswalk_source, '-') || ' ' || coalesce(e.retired_at::text, 'live') || ' ' ||
			e.updated_at::text `+isoEdgesWhere+` ORDER BY 1`)
	overlay := queryStrings(t, pool, `SELECT id::text || ' ' || status || ' ' || updated_at::text
		FROM fw_to_scf_overlay_edges ORDER BY 1`)
	return append(catalog, overlay...)
}

func TestConvergeImported_ConvergesImportedCrosswalksOnly(t *testing.T) {
	pool := dbtest.NewMigratePool(t)
	ctx := context.Background()
	ensureSCFLoaded(t, pool)
	resetISO(t, pool)
	resetPCI(t, pool)
	t.Cleanup(func() {
		resetISO(t, pool)
		resetPCI(t, pool)
	})

	bundled := loadISOCrosswalk(t)
	listed := map[string]bool{}
	for _, e := range wantEdges(bundled) {
		listed[e] = true
	}
	anchors := queryStrings(t, pool, `
		SELECT a.scf_id FROM scf_anchors a
		JOIN framework_versions fv ON fv.id = a.framework_version_id
		JOIN frameworks f ON f.id = fv.framework_id
		WHERE f.slug = 'scf' AND f.tenant_id IS NULL AND fv.status = 'current'
		ORDER BY 1`)
	// unlistedAnchor is a current anchor the bundled file never maps code to.
	unlistedAnchor := func(code string, not ...string) string {
		for _, a := range anchors {
			if !listed[code+"->"+a] && !slices.Contains(not, a) {
				return a
			}
		}
		t.Fatalf("no current SCF anchor left unmapped for %s", code)
		return ""
	}

	// The older crosswalk: the first mapping sat on another anchor.
	moved := bundled.Mappings[0]
	staleAnchor := unlistedAnchor(moved.RequirementCode)
	older := loadISOCrosswalk(t)
	older.Mappings[0].SCFAnchor = staleAnchor
	if _, err := soc2import.Import(ctx, pool, older); err != nil {
		t.Fatalf("import older ISO crosswalk: %v", err)
	}
	if _, err := pool.Exec(ctx, `UPDATE fw_to_scf_edges e SET crosswalk_source = NULL
		FROM framework_requirements fr, framework_versions fv, frameworks f
		WHERE fr.id = e.framework_requirement_id AND fv.id = fr.framework_version_id
		  AND f.id = fv.framework_id AND f.slug = 'iso27001' AND f.tenant_id IS NULL`); err != nil {
		t.Fatalf("clear crosswalk_source: %v", err)
	}

	// An approved remap's copy and a tenant's overlay edge, both on anchors
	// the bundled file does not list for that requirement.
	remapAnchor := unlistedAnchor(moved.RequirementCode, staleAnchor)
	remapID := uuid.New()
	if _, err := pool.Exec(ctx, `INSERT INTO fw_to_scf_edges (
				id, framework_requirement_id, scf_anchor_id, relationship_type, strength,
				source_attribution, rationale, mapping_tier, crosswalk_source)
			SELECT $1, e.framework_requirement_id, a2.id, e.relationship_type, e.strength,
				e.source_attribution, e.rationale, e.mapping_tier, $5
			FROM fw_to_scf_edges e
			JOIN framework_requirements fr ON fr.id = e.framework_requirement_id
			JOIN framework_versions fv ON fv.id = fr.framework_version_id
			JOIN frameworks f ON f.id = fv.framework_id
			JOIN scf_anchors a ON a.id = e.scf_anchor_id
			JOIN scf_anchors a2 ON a2.framework_version_id = a.framework_version_id AND a2.scf_id = $4
			WHERE f.slug = 'iso27001' AND f.tenant_id IS NULL AND fr.code = $2 AND a.scf_id = $3`,
		remapID, moved.RequirementCode, staleAnchor, remapAnchor, "scf-remap:"+remapID.String()); err != nil {
		t.Fatalf("seed approved remap edge: %v", err)
	}
	tenant := dbtest.SeedTenant(t, pool, "fw_to_scf_overlay_edges")
	overlayID := uuid.New()
	if _, err := pool.Exec(ctx, `INSERT INTO fw_to_scf_overlay_edges (
			id, tenant_id, framework_requirement_id, scf_anchor_id,
			relationship_type, strength, rationale, source_ref, created_by)
		SELECT $1, $2, e.framework_requirement_id, e.scf_anchor_id, 'equal', 1.0,
			'auditor tested this anchor', 'https://wiki.example/audit', 'test'
		FROM fw_to_scf_edges e WHERE e.id = $3`, overlayID, tenant, remapID); err != nil {
		t.Fatalf("seed overlay edge: %v", err)
	}

	dir := copyBundled(t, "iso27001-2022.yaml", "pci-dss-4.0.yaml")
	results, err := soc2import.ConvergeImported(ctx, pool, dir)
	if err != nil {
		t.Fatalf("ConvergeImported: %v", err)
	}
	if len(results) != 2 {
		t.Fatalf("results = %+v; want ISO and PCI", results)
	}
	iso, pci := results[0], results[1]
	if iso.File != "iso27001-2022.yaml" || iso.Skipped || iso.Err != nil {
		t.Fatalf("ISO result = %+v; want imported without error", iso)
	}
	if pci.File != "pci-dss-4.0.yaml" || !pci.Skipped || pci.Err != nil || pci.FrameworkSlug != "pcidss" {
		t.Fatalf("PCI result = %+v; want skipped (never imported)", pci)
	}
	if got := countRows(t, pool, `SELECT count(*) FROM frameworks WHERE slug = 'pcidss'`); got != 0 {
		t.Fatalf("converge imported PCI DSS, which this install never imported")
	}

	stale := moved.RequirementCode + "->" + staleAnchor
	if len(iso.Report.EdgesRetired) != 1 || iso.Report.EdgesRetired[0].RequirementCode+"->"+iso.Report.EdgesRetired[0].SCFAnchor != stale {
		t.Fatalf("retired = %+v; want only %s", iso.Report.EdgesRetired, stale)
	}
	if iso.Report.EdgesCreated != 1 {
		t.Fatalf("edges created = %d; want 1 (the bundled mapping the older file lacked)", iso.Report.EdgesCreated)
	}
	if got, want := liveYAMLEdges(t, pool), wantEdges(bundled); !slices.Equal(got, want) {
		t.Fatalf("live ISO edges after converge:\n got %v\nwant %v", got, want)
	}
	if got := countRows(t, pool, `SELECT count(*) FROM fw_to_scf_edges WHERE id = $1 AND retired_at IS NULL`, remapID); got != 1 {
		t.Fatalf("approved remap edge was retired by converge")
	}
	if got := countRows(t, pool, `SELECT count(*) FROM fw_to_scf_overlay_edges WHERE id = $1 AND status = 'active'`, overlayID); got != 1 {
		t.Fatalf("tenant overlay edge changed by converge")
	}

	// A second boot changes nothing.
	before := edgeSnapshot(t, pool)
	again, err := soc2import.ConvergeImported(ctx, pool, dir)
	if err != nil {
		t.Fatalf("second ConvergeImported: %v", err)
	}
	r := again[0].Report
	if again[0].Err != nil || r.EdgesCreated != 0 || r.EdgesUpdated != 0 || len(r.EdgesRetired) != 0 || len(r.EdgesRestored) != 0 || r.RequirementsCreated != 0 || r.RequirementsUpdated != 0 {
		t.Fatalf("second converge report = %+v (err %v); want a no-op", r, again[0].Err)
	}
	if after := edgeSnapshot(t, pool); !slices.Equal(before, after) {
		t.Fatalf("second converge changed edges:\nbefore %v\n after %v", before, after)
	}
}

func TestConvergeImported_ExcludeAndBadFileDoNotStopTheRest(t *testing.T) {
	pool := dbtest.NewMigratePool(t)
	ctx := context.Background()
	ensureSCFLoaded(t, pool)
	resetISO(t, pool)
	t.Cleanup(func() { resetISO(t, pool) })
	if _, err := soc2import.Import(ctx, pool, loadISOCrosswalk(t)); err != nil {
		t.Fatalf("import ISO: %v", err)
	}

	dir := copyBundled(t, "iso27001-2022.yaml", "pci-dss-4.0.yaml")
	if err := os.WriteFile(filepath.Join(dir, "broken.yaml"), []byte("schema_version: [\n"), 0o600); err != nil {
		t.Fatalf("write broken.yaml: %v", err)
	}
	results, err := soc2import.ConvergeImported(ctx, pool, dir, "pci-dss-4.0.yaml")
	if err != nil {
		t.Fatalf("ConvergeImported: %v", err)
	}
	if len(results) != 2 || results[0].File != "broken.yaml" || results[0].Err == nil {
		t.Fatalf("results = %+v; want broken.yaml failing first, PCI excluded", results)
	}
	if results[1].File != "iso27001-2022.yaml" || results[1].Err != nil || results[1].Skipped {
		t.Fatalf("ISO result = %+v; want imported after the broken file", results[1])
	}
}
