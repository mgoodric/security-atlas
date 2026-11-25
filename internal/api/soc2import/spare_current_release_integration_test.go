//go:build integration

package soc2import_test

// #3061 D3: a crosswalk bound in a release that is not current spares the
// edge it wrote on the current release's anchor for a code it still lists.
// These tests pin the edges the retire pass must still retire on such an
// install: a mapping the crosswalk dropped, and an edge on another
// release's anchor with a listed code.

import (
	"context"
	"slices"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/mgoodric/security-atlas/internal/api/scfimport"
	"github.com/mgoodric/security-atlas/internal/api/soc2import"
	"github.com/mgoodric/security-atlas/internal/dbtest"
)

const (
	spareSlug   = "spare_3061"
	spareSource = "yaml:spare-3061.yaml"
)

// relabeledSample is the sample catalog under another release label.
func relabeledSample(t *testing.T, release string) *scfimport.Catalog {
	t.Helper()
	cat := loadSCFSample(t)
	cat.ReleaseVersion = release
	return cat
}

// spareCrosswalk is a one-requirement crosswalk bound in release, mapping
// R-1 to each code.
func spareCrosswalk(release string, codes ...string) *soc2import.Crosswalk {
	cw := &soc2import.Crosswalk{
		SchemaVersion:     "1",
		FrameworkSlug:     spareSlug,
		FrameworkName:     "Retire-pass fixture (#3061)",
		FrameworkIssuer:   "security-atlas tests",
		FrameworkVersion:  "1.0",
		SourceAttribution: "community_draft",
		Source:            spareSource,
		SCFRelease:        release,
		Requirements:      []soc2import.Requirement{{Code: "R-1", Title: "First", Body: "Fixture requirement."}},
	}
	for _, c := range codes {
		cw.Mappings = append(cw.Mappings, soc2import.Mapping{
			RequirementCode: "R-1", SCFAnchor: c, RelationshipType: "intersects_with", Strength: 0.7, Rationale: "R-1->" + c,
		})
	}
	return cw
}

// spareEdges returns "scf_id@release:live|retired" for every edge of the
// fixture.
func spareEdges(t *testing.T, pool *pgxpool.Pool) []string {
	t.Helper()
	return queryStrings(t, pool, `
		SELECT a.scf_id || '@' || av.version || ':' || CASE WHEN e.retired_at IS NULL THEN 'live' ELSE 'retired' END
		FROM fw_to_scf_edges e
		JOIN framework_requirements fr ON fr.id = e.framework_requirement_id
		JOIN framework_versions rv ON rv.id = fr.framework_version_id
		JOIN frameworks f ON f.id = rv.framework_id
		JOIN scf_anchors a ON a.id = e.scf_anchor_id
		JOIN framework_versions av ON av.id = a.framework_version_id
		WHERE f.slug = $1
		ORDER BY 1`, spareSlug)
}

func spareFixture(t *testing.T) *pgxpool.Pool {
	t.Helper()
	pool := dbtest.NewMigratePool(t)
	wipe := func() {
		wipeSCFReleases(t, pool)
		for _, stmt := range []string{
			`DELETE FROM framework_versions WHERE framework_id IN (SELECT id FROM frameworks WHERE slug = 'spare_3061')`,
			`DELETE FROM frameworks WHERE slug = 'spare_3061'`,
		} {
			if _, err := pool.Exec(context.Background(), stmt); err != nil {
				t.Fatalf("wipe %q: %v", stmt, err)
			}
		}
	}
	wipe()
	t.Cleanup(func() {
		wipe()
		ensureSCFLoaded(t, pool)
	})
	return pool
}

func importSpare(t *testing.T, pool *pgxpool.Pool, cw *soc2import.Crosswalk) {
	t.Helper()
	if _, err := soc2import.Import(context.Background(), pool, cw); err != nil {
		t.Fatalf("import crosswalk bound in %s: %v", cw.SCFRelease, err)
	}
}

// A crosswalk bound in the legacy sample keeps its listed mapping's
// current-release edge and still retires the one it no longer lists.
func TestSpareCurrentRelease_DroppedMappingIsStillRetired(t *testing.T) {
	pool := spareFixture(t)
	ctx := context.Background()
	const byo = "test-byo-3061"
	importSCF(t, pool, relabeledSample(t, byo))
	importSpare(t, pool, spareCrosswalk(byo, "GOV-02", "GOV-04"))

	sample := loadSCFSample(t)
	if _, err := scfimport.ImportWithOptions(ctx, pool, sample, scfimport.Options{KeepCurrent: true}); err != nil {
		t.Fatalf("load sample with KeepCurrent: %v", err)
	}
	importSpare(t, pool, spareCrosswalk(sample.ReleaseVersion, "GOV-02"))

	got := spareEdges(t, pool)
	for _, want := range []string{
		"GOV-02@" + byo + ":live",
		"GOV-04@" + byo + ":retired",
		"GOV-02@" + sample.ReleaseVersion + ":live",
	} {
		if !slices.Contains(got, want) {
			t.Errorf("edges = %v; want %s", got, want)
		}
	}
}

// An edge on an older release's anchor is not spared because the crosswalk
// lists its code: only the current release's anchor for that code is.
func TestSpareCurrentRelease_EdgeOnAnotherReleaseAnchorIsStillRetired(t *testing.T) {
	pool := spareFixture(t)
	ctx := context.Background()
	const old, byo = "test-old-3061", "test-byo-3061"
	importSCF(t, pool, relabeledSample(t, old))
	importSpare(t, pool, spareCrosswalk(old, "GOV-02"))
	importSCF(t, pool, relabeledSample(t, byo)) // old is legacy now

	sample := loadSCFSample(t)
	if _, err := scfimport.ImportWithOptions(ctx, pool, sample, scfimport.Options{KeepCurrent: true}); err != nil {
		t.Fatalf("load sample with KeepCurrent: %v", err)
	}
	importSpare(t, pool, spareCrosswalk(sample.ReleaseVersion, "GOV-02"))

	got := spareEdges(t, pool)
	for _, want := range []string{
		"GOV-02@" + old + ":retired",
		"GOV-02@" + sample.ReleaseVersion + ":live",
	} {
		if !slices.Contains(got, want) {
			t.Errorf("edges = %v; want %s", got, want)
		}
	}
}
