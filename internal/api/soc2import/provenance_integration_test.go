//go:build integration

package soc2import_test

import (
	"context"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/mgoodric/security-atlas/internal/api/soc2import"
	"github.com/mgoodric/security-atlas/internal/dbtest"
)

// Issue #1754 — a crosswalk row from a weaker source must never overwrite an
// existing edge's provenance. Precedence: scf_official > community_draft >
// org_internal. Edges are global catalog data, so a downgrade would change
// provenance for every tenant.

const provenanceSlug = "provenance-1754"

func provenanceCrosswalk(attribution, rationale string) *soc2import.Crosswalk {
	return &soc2import.Crosswalk{
		SchemaVersion:     "1",
		FrameworkSlug:     provenanceSlug,
		FrameworkName:     "Provenance precedence fixture",
		FrameworkIssuer:   "security-atlas tests",
		FrameworkVersion:  "1.0",
		SourceAttribution: attribution,
		Requirements: []soc2import.Requirement{
			{Code: "P-1", Title: "Governance", Body: "Fixture requirement."},
		},
		Mappings: []soc2import.Mapping{{
			RequirementCode:  "P-1",
			SCFAnchor:        "GOV-01",
			RelationshipType: "equal",
			Strength:         0.9,
			Rationale:        rationale,
		}},
	}
}

type edgeState struct {
	attribution string
	tier        string
	rationale   string
}

func readProvenanceEdge(t *testing.T, pool *pgxpool.Pool) edgeState {
	t.Helper()
	var s edgeState
	if err := pool.QueryRow(context.Background(), `
		SELECT e.source_attribution::text, e.mapping_tier::text, e.rationale
		FROM fw_to_scf_edges e
		JOIN framework_requirements fr ON fr.id = e.framework_requirement_id
		JOIN framework_versions fv ON fv.id = fr.framework_version_id
		JOIN frameworks f ON f.id = fv.framework_id
		WHERE f.slug = $1 AND f.tenant_id IS NULL AND fr.code = 'P-1'`,
		provenanceSlug).Scan(&s.attribution, &s.tier, &s.rationale); err != nil {
		t.Fatalf("read edge: %v", err)
	}
	return s
}

func TestImport_WeakerSourceNeverDowngradesOfficialEdge(t *testing.T) {
	pool := dbtest.NewMigratePool(t)
	ctx := context.Background()
	resetFrameworkCatalog(t, pool, provenanceSlug)
	t.Cleanup(func() { resetFrameworkCatalog(t, pool, provenanceSlug) })
	ensureSCFLoaded(t, pool)

	if _, err := soc2import.Import(ctx, pool, provenanceCrosswalk("scf_official", "official rationale")); err != nil {
		t.Fatalf("official Import: %v", err)
	}
	// Pin a known tier so the assertion proves the importer leaves it alone.
	if _, err := pool.Exec(ctx, `
		UPDATE fw_to_scf_edges SET mapping_tier = 'verified'
		WHERE framework_requirement_id IN (
			SELECT fr.id FROM framework_requirements fr
			JOIN framework_versions fv ON fv.id = fr.framework_version_id
			JOIN frameworks f ON f.id = fv.framework_id
			WHERE f.slug = $1 AND f.tenant_id IS NULL)`, provenanceSlug); err != nil {
		t.Fatalf("pin tier: %v", err)
	}
	want := edgeState{attribution: "scf_official", tier: "verified", rationale: "official rationale"}

	for _, weaker := range []string{"community_draft", "org_internal"} {
		report, err := soc2import.Import(ctx, pool, provenanceCrosswalk(weaker, weaker+" rationale"))
		if err != nil {
			t.Fatalf("%s Import: %v", weaker, err)
		}
		if report.EdgesUpdated != 0 || report.EdgesCreated != 0 {
			t.Fatalf("%s re-import wrote an edge: %+v", weaker, report)
		}
		if len(report.EdgesSkipped) != 1 {
			t.Fatalf("%s re-import skipped = %+v; want exactly one", weaker, report.EdgesSkipped)
		}
		sk := report.EdgesSkipped[0]
		if sk.RequirementCode != "P-1" || sk.SCFAnchor != "GOV-01" ||
			sk.ExistingAttribution != "scf_official" || sk.IncomingAttribution != weaker {
			t.Fatalf("%s skipped entry = %+v", weaker, sk)
		}
		if report.MappingsByAttribution[weaker] != 0 {
			t.Fatalf("%s skipped row counted as applied: %+v", weaker, report.MappingsByAttribution)
		}
		if got := readProvenanceEdge(t, pool); got != want {
			t.Fatalf("after %s re-import edge = %+v; want %+v", weaker, got, want)
		}
	}

	// A same-source re-import still updates rationale as before.
	report, err := soc2import.Import(ctx, pool, provenanceCrosswalk("scf_official", "revised official rationale"))
	if err != nil {
		t.Fatalf("same-source Import: %v", err)
	}
	if report.EdgesUpdated != 1 || len(report.EdgesSkipped) != 0 {
		t.Fatalf("same-source re-import report = %+v; want one update, no skips", report)
	}
	want.rationale = "revised official rationale"
	if got := readProvenanceEdge(t, pool); got != want {
		t.Fatalf("after same-source re-import edge = %+v; want %+v", got, want)
	}
}

func TestImport_StrongerSourceReplacesWeakerEdge(t *testing.T) {
	pool := dbtest.NewMigratePool(t)
	ctx := context.Background()
	resetFrameworkCatalog(t, pool, provenanceSlug)
	t.Cleanup(func() { resetFrameworkCatalog(t, pool, provenanceSlug) })
	ensureSCFLoaded(t, pool)

	if _, err := soc2import.Import(ctx, pool, provenanceCrosswalk("org_internal", "org rationale")); err != nil {
		t.Fatalf("org Import: %v", err)
	}
	report, err := soc2import.Import(ctx, pool, provenanceCrosswalk("community_draft", "draft rationale"))
	if err != nil {
		t.Fatalf("draft Import: %v", err)
	}
	if report.EdgesUpdated != 1 || len(report.EdgesSkipped) != 0 {
		t.Fatalf("upgrade report = %+v; want one update, no skips", report)
	}
	if got := readProvenanceEdge(t, pool); got.attribution != "community_draft" || got.rationale != "draft rationale" {
		t.Fatalf("after upgrade edge = %+v", got)
	}
}
