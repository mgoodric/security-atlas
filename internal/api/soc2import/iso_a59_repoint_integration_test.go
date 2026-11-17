//go:build integration

package soc2import_test

import (
	"context"
	"slices"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/mgoodric/security-atlas/internal/api/soc2import"
	"github.com/mgoodric/security-atlas/internal/dbtest"
)

// Issue #2301: ISO A.5.9 (asset inventory) moves from AST-22 (secure
// disposal) to AST-04 (Asset Inventories). A fresh install gets only the new
// edge. An upgraded install that imported the old crosswalk has the old edge
// retired, not deleted, by boot convergence, and the new edge live. A
// tenant overlay edge on the old pair is untouched.

const a59 = "A.5.9"

// a59Edges returns "anchor relationship strength state" for every catalog
// edge on ISO A.5.9, retired or not.
func a59Edges(t *testing.T, pool *pgxpool.Pool) []string {
	t.Helper()
	return queryStrings(t, pool, `SELECT a.scf_id || ' ' || e.relationship_type::text || ' ' || e.strength::text || ' ' ||
			CASE WHEN e.retired_at IS NULL THEN 'live' ELSE 'retired' END `+isoEdgesWhere+`
		AND fr.code = $1 ORDER BY 1`, a59)
}

func TestISOA59_FreshInstallHasOnlyAST04(t *testing.T) {
	pool := dbtest.NewMigratePool(t)
	ctx := context.Background()
	ensureSCFLoaded(t, pool)
	resetISO(t, pool)
	t.Cleanup(func() { resetISO(t, pool) })

	if _, err := soc2import.Import(ctx, pool, loadISOCrosswalk(t)); err != nil {
		t.Fatalf("import ISO: %v", err)
	}
	if got, want := a59Edges(t, pool), []string{"AST-04 intersects_with 0.8 live"}; !slices.Equal(got, want) {
		t.Fatalf("A.5.9 edges on a fresh install = %v; want %v", got, want)
	}
}

func TestISOA59_UpgradeRetiresAST22AndKeepsOverlay(t *testing.T) {
	pool := dbtest.NewMigratePool(t)
	ctx := context.Background()
	ensureSCFLoaded(t, pool)
	resetISO(t, pool)
	t.Cleanup(func() { resetISO(t, pool) })

	// The crosswalk as shipped before #2301.
	older := loadISOCrosswalk(t)
	found := false
	for i, m := range older.Mappings {
		if m.RequirementCode == a59 {
			older.Mappings[i].SCFAnchor = "AST-22"
			older.Mappings[i].RelationshipType = "subset_of"
			older.Mappings[i].Strength = 0.7
			found = true
		}
	}
	if !found {
		t.Fatalf("bundled ISO crosswalk has no %s mapping", a59)
	}
	if _, err := soc2import.Import(ctx, pool, older); err != nil {
		t.Fatalf("import pre-#2301 ISO crosswalk: %v", err)
	}

	// A tenant asserted the old pair as an overlay edge.
	tenant := dbtest.SeedTenant(t, pool, "fw_to_scf_overlay_edges")
	overlayID := uuid.New()
	if _, err := pool.Exec(ctx, `INSERT INTO fw_to_scf_overlay_edges (
			id, tenant_id, framework_requirement_id, scf_anchor_id,
			relationship_type, strength, rationale, source_ref, created_by)
		SELECT $1, $2, e.framework_requirement_id, e.scf_anchor_id, 'intersects_with', 0.5,
			'disposal records feed our inventory', 'https://wiki.example/assets', 'test'
		`+isoEdgesWhere+` AND fr.code = $3 AND a.scf_id = 'AST-22'`, overlayID, tenant, a59); err != nil {
		t.Fatalf("seed overlay edge: %v", err)
	}

	results, err := soc2import.ConvergeImported(ctx, pool, copyBundled(t, "iso27001-2022.yaml"))
	if err != nil {
		t.Fatalf("ConvergeImported: %v", err)
	}
	if len(results) != 1 || results[0].Err != nil || results[0].Skipped {
		t.Fatalf("results = %+v; want ISO converged without error", results)
	}
	r := results[0].Report
	if len(r.EdgesRetired) != 1 || r.EdgesRetired[0].RequirementCode != a59 || r.EdgesRetired[0].SCFAnchor != "AST-22" {
		t.Fatalf("retired = %+v; want only %s->AST-22", r.EdgesRetired, a59)
	}
	if r.EdgesCreated != 1 {
		t.Fatalf("edges created = %d; want 1 (%s->AST-04)", r.EdgesCreated, a59)
	}

	want := []string{"AST-04 intersects_with 0.8 live", "AST-22 subset_of 0.7 retired"}
	if got := a59Edges(t, pool); !slices.Equal(got, want) {
		t.Fatalf("A.5.9 edges after upgrade = %v; want %v", got, want)
	}
	if got := countRows(t, pool, `SELECT count(*) FROM fw_to_scf_overlay_edges WHERE id = $1 AND status = 'active'`, overlayID); got != 1 {
		t.Fatalf("tenant overlay edge on %s->AST-22 changed by converge", a59)
	}
}
