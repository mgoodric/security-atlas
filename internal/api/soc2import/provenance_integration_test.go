//go:build integration

package soc2import_test

import (
	"context"
	"testing"
	"time"

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
	// Provenance and trust are separate axes (ADR 0018): the upgrade raises
	// source_attribution and leaves the tier where it was.
	want := edgeState{attribution: "community_draft", tier: "draft", rationale: "draft rationale"}
	if got := readProvenanceEdge(t, pool); got != want {
		t.Fatalf("after upgrade edge = %+v; want %+v", got, want)
	}
	if len(report.EdgesUpgradedOntoRejected) != 0 {
		t.Fatalf("upgrade onto a draft edge reported as rejected: %+v", report.EdgesUpgradedOntoRejected)
	}
}

func setProvenanceTier(t *testing.T, pool *pgxpool.Pool, tier string) {
	t.Helper()
	if _, err := pool.Exec(context.Background(), `
		UPDATE fw_to_scf_edges SET mapping_tier = $2::crosswalk_mapping_tier
		WHERE framework_requirement_id IN (
			SELECT fr.id FROM framework_requirements fr
			JOIN framework_versions fv ON fv.id = fr.framework_version_id
			JOIN frameworks f ON f.id = fv.framework_id
			WHERE f.slug = $1 AND f.tenant_id IS NULL)`, provenanceSlug, tier); err != nil {
		t.Fatalf("set tier %s: %v", tier, err)
	}
}

// An upgrade onto a 'rejected' edge keeps the tier (the importer never
// touches trust) and is listed in the report so an operator can act on it.
func TestImport_UpgradeOntoRejectedEdgeIsReported(t *testing.T) {
	pool := dbtest.NewMigratePool(t)
	ctx := context.Background()
	resetFrameworkCatalog(t, pool, provenanceSlug)
	t.Cleanup(func() { resetFrameworkCatalog(t, pool, provenanceSlug) })
	ensureSCFLoaded(t, pool)

	if _, err := soc2import.Import(ctx, pool, provenanceCrosswalk("community_draft", "draft rationale")); err != nil {
		t.Fatalf("draft Import: %v", err)
	}
	setProvenanceTier(t, pool, "rejected")

	report, err := soc2import.Import(ctx, pool, provenanceCrosswalk("scf_official", "official rationale"))
	if err != nil {
		t.Fatalf("official Import: %v", err)
	}
	if len(report.EdgesUpgradedOntoRejected) != 1 {
		t.Fatalf("upgraded-onto-rejected = %+v; want exactly one", report.EdgesUpgradedOntoRejected)
	}
	up := report.EdgesUpgradedOntoRejected[0]
	if up.RequirementCode != "P-1" || up.SCFAnchor != "GOV-01" ||
		up.PreviousAttribution != "community_draft" || up.IncomingAttribution != "scf_official" {
		t.Fatalf("upgraded entry = %+v", up)
	}
	want := edgeState{attribution: "scf_official", tier: "rejected", rationale: "official rationale"}
	if got := readProvenanceEdge(t, pool); got != want {
		t.Fatalf("after upgrade edge = %+v; want %+v", got, want)
	}
}

// A weaker import racing a concurrent stronger write must still skip. The
// test holds the edge's row lock in its own transaction, raises the edge to
// scf_official there, and starts a community_draft import. Once that import
// is waiting on the lock the test commits. With a plain read the import
// would have checked against the old org_internal row and then overwritten
// the official edge; with FOR UPDATE it re-reads after the commit and skips.
func TestImport_ConcurrentWeakerImportDoesNotOverwriteOfficialEdge(t *testing.T) {
	pool := dbtest.NewMigratePool(t)
	ctx := context.Background()
	resetFrameworkCatalog(t, pool, provenanceSlug)
	t.Cleanup(func() { resetFrameworkCatalog(t, pool, provenanceSlug) })
	ensureSCFLoaded(t, pool)

	if _, err := soc2import.Import(ctx, pool, provenanceCrosswalk("org_internal", "org rationale")); err != nil {
		t.Fatalf("org Import: %v", err)
	}

	tx, err := pool.Begin(ctx)
	if err != nil {
		t.Fatalf("begin: %v", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if _, err := tx.Exec(ctx, `
		UPDATE fw_to_scf_edges
		SET source_attribution = 'scf_official', rationale = 'official rationale'
		WHERE framework_requirement_id IN (
			SELECT fr.id FROM framework_requirements fr
			JOIN framework_versions fv ON fv.id = fr.framework_version_id
			JOIN frameworks f ON f.id = fv.framework_id
			WHERE f.slug = $1 AND f.tenant_id IS NULL)`, provenanceSlug); err != nil {
		t.Fatalf("raise edge in held tx: %v", err)
	}

	type result struct {
		report soc2import.Report
		err    error
	}
	done := make(chan result, 1)
	go func() {
		r, err := soc2import.Import(ctx, pool, provenanceCrosswalk("community_draft", "draft rationale"))
		done <- result{r, err}
	}()

	// Wait until the import is blocked on the held row lock.
	deadline := time.Now().Add(10 * time.Second)
	for {
		var waiting int
		if err := pool.QueryRow(ctx, `
			SELECT count(*) FROM pg_stat_activity
			WHERE datname = current_database() AND wait_event_type = 'Lock'`).Scan(&waiting); err != nil {
			t.Fatalf("poll lock waiters: %v", err)
		}
		if waiting > 0 {
			break
		}
		select {
		case r := <-done:
			t.Fatalf("import finished without waiting on the edge lock: %+v err=%v", r.report, r.err)
		default:
		}
		if time.Now().After(deadline) {
			t.Fatal("import never waited on the edge lock")
		}
		time.Sleep(20 * time.Millisecond)
	}
	if err := tx.Commit(ctx); err != nil {
		t.Fatalf("commit held tx: %v", err)
	}

	r := <-done
	if r.err != nil {
		t.Fatalf("draft Import: %v", r.err)
	}
	if len(r.report.EdgesSkipped) != 1 || r.report.EdgesUpdated != 0 {
		t.Fatalf("racing draft import report = %+v; want one skip, no update", r.report)
	}
	got := readProvenanceEdge(t, pool)
	if got.attribution != "scf_official" || got.rationale != "official rationale" {
		t.Fatalf("racing draft import overwrote the official edge: %+v", got)
	}
}
