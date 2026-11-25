//go:build integration

package soc2import_test

// #3065: an edge the SCF edge remap carried onto another release
// (crosswalk_source 'scf-remap:<proposal>') is retired with its source when
// the crosswalk drops the mapping, and restored when it lists it again. Both
// shapes carry: a BYO install (#3061), where the bundled crosswalk binds in
// the legacy sample and the carry-forward copies its edges onto the
// operator's release, and an ordinary SCF upgrade, where the carry-forward
// copies them onto the new current release.

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
	carriedSlug   = "carried_3065"
	carriedSource = "yaml:carried-3065.yaml"
)

// carriedCrosswalk is a one-requirement crosswalk bound in release, mapping
// R-1 to each code.
func carriedCrosswalk(release string, codes ...string) *soc2import.Crosswalk {
	cw := &soc2import.Crosswalk{
		SchemaVersion:     "1",
		FrameworkSlug:     carriedSlug,
		FrameworkName:     "Carried-edge fixture (#3065)",
		FrameworkIssuer:   "security-atlas tests",
		FrameworkVersion:  "1.0",
		SourceAttribution: "community_draft",
		Source:            carriedSource,
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

// carriedEdges returns "scf_id@release:live|retired" for every edge of the
// fixture, carried or not.
func carriedEdges(t *testing.T, pool *pgxpool.Pool) []string {
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
		ORDER BY 1`, carriedSlug)
}

func carriedFixture(t *testing.T) *pgxpool.Pool {
	t.Helper()
	pool := dbtest.NewMigratePool(t)
	wipe := func() {
		wipeSCFReleases(t, pool)
		for _, stmt := range []string{
			`DELETE FROM framework_versions WHERE framework_id IN (SELECT id FROM frameworks WHERE slug = 'carried_3065')`,
			`DELETE FROM frameworks WHERE slug = 'carried_3065'`,
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

func importCarried(t *testing.T, pool *pgxpool.Pool, cw *soc2import.Crosswalk) soc2import.Report {
	t.Helper()
	rep, err := soc2import.Import(context.Background(), pool, cw)
	if err != nil {
		t.Fatalf("import crosswalk bound in %s: %v", cw.SCFRelease, err)
	}
	return rep
}

// carry runs the edge-remap queue and its carry-forward (#2315), as
// `atlas-cli catalog import-scf` does after every import, and checks it
// carried want edges.
func carry(t *testing.T, pool *pgxpool.Pool, want int) {
	t.Helper()
	rep, err := scfimport.QueueEdgeRemapProposals(context.Background(), pool)
	if err != nil {
		t.Fatalf("QueueEdgeRemapProposals: %v", err)
	}
	if rep.Carried != want {
		t.Fatalf("carry-forward carried %d edges; want %d", rep.Carried, want)
	}
}

func assertEdges(t *testing.T, pool *pgxpool.Pool, want ...string) {
	t.Helper()
	got := carriedEdges(t, pool)
	slices.Sort(want)
	if !slices.Equal(got, want) {
		t.Errorf("edges = %v; want %v", got, want)
	}
}

func carriedCodes(edges []soc2import.CatalogEdge) []string {
	var out []string
	for _, e := range edges {
		out = append(out, e.SCFAnchor)
	}
	return out
}

// byoCarried sets up a BYO-shaped install: the operator's release current,
// the sample loaded beside it as legacy (`import-scf --keep-current`), the
// crosswalk bound in the sample, and its edges carried onto the operator's
// release.
func byoCarried(t *testing.T, pool *pgxpool.Pool) (byo, sample string) {
	t.Helper()
	byo = "test-byo-3065"
	importSCF(t, pool, relabeledSample(t, byo))
	cat := loadSCFSample(t)
	if _, err := scfimport.ImportWithOptions(context.Background(), pool, cat, scfimport.Options{KeepCurrent: true}); err != nil {
		t.Fatalf("load sample with KeepCurrent: %v", err)
	}
	importCarried(t, pool, carriedCrosswalk(cat.ReleaseVersion, "GOV-02", "GOV-04"))
	carry(t, pool, 2)
	return byo, cat.ReleaseVersion
}

// On a BYO install, a re-import that drops a mapping retires the carried
// copy on the operator's release with the sample-anchor edge, and leaves the
// other carried edge live. Listing it again restores both.
func TestCarriedEdges_BYODroppedMappingRetiresTheCarriedCopy(t *testing.T) {
	pool := carriedFixture(t)
	byo, sample := byoCarried(t, pool)
	assertEdges(t, pool,
		"GOV-02@"+byo+":live", "GOV-02@"+sample+":live",
		"GOV-04@"+byo+":live", "GOV-04@"+sample+":live")

	rep := importCarried(t, pool, carriedCrosswalk(sample, "GOV-02"))
	assertEdges(t, pool,
		"GOV-02@"+byo+":live", "GOV-02@"+sample+":live",
		"GOV-04@"+byo+":retired", "GOV-04@"+sample+":retired")
	if got := carriedCodes(rep.EdgesCarriedRetired); !slices.Equal(got, []string{"GOV-04"}) {
		t.Errorf("EdgesCarriedRetired = %v; want [GOV-04]", got)
	}
	if got := carriedCodes(rep.EdgesUnlisted); slices.Contains(got, "GOV-04") {
		t.Errorf("EdgesUnlisted = %v; the retired carried copy is not unlisted", got)
	}
	if n := countRows(t, pool, `
		SELECT count(*) FROM fw_to_scf_edge_tier_transitions t
		JOIN fw_to_scf_edges e ON e.id = t.edge_id
		WHERE e.crosswalk_source LIKE 'scf-remap:%' AND t.note = $1`,
		"retired: carried from an edge dropped by crosswalk "+carriedSource); n != 1 {
		t.Errorf("%d carried-retire audit rows; want 1", n)
	}

	rep = importCarried(t, pool, carriedCrosswalk(sample, "GOV-02", "GOV-04"))
	assertEdges(t, pool,
		"GOV-02@"+byo+":live", "GOV-02@"+sample+":live",
		"GOV-04@"+byo+":live", "GOV-04@"+sample+":live")
	if got := carriedCodes(rep.EdgesCarriedRestored); !slices.Equal(got, []string{"GOV-04"}) {
		t.Errorf("EdgesCarriedRestored = %v; want [GOV-04]", got)
	}

	// The settled state is idempotent.
	rep = importCarried(t, pool, carriedCrosswalk(sample, "GOV-02", "GOV-04"))
	if len(rep.EdgesCarriedRetired)+len(rep.EdgesCarriedRestored) != 0 {
		t.Errorf("re-import moved carried edges: retired %v, restored %v", rep.EdgesCarriedRetired, rep.EdgesCarriedRestored)
	}
}

// On an ordinary install upgraded to a new SCF release, the carry-forward
// copies the crosswalk's edges onto the new current release; dropping a
// mapping retires that copy too.
func TestCarriedEdges_UpgradeDroppedMappingRetiresTheCarriedCopy(t *testing.T) {
	pool := carriedFixture(t)
	const old = "test-old-3065"
	importSCF(t, pool, relabeledSample(t, old))
	importCarried(t, pool, carriedCrosswalk(old, "GOV-02", "GOV-04"))
	sample := loadSCFSample(t)
	importSCF(t, pool, sample) // old is legacy now
	carry(t, pool, 2)

	rep := importCarried(t, pool, carriedCrosswalk(old, "GOV-02"))
	assertEdges(t, pool,
		"GOV-02@"+old+":live", "GOV-02@"+sample.ReleaseVersion+":live",
		"GOV-04@"+old+":retired", "GOV-04@"+sample.ReleaseVersion+":retired")
	if got := carriedCodes(rep.EdgesCarriedRetired); !slices.Equal(got, []string{"GOV-04"}) {
		t.Errorf("EdgesCarriedRetired = %v; want [GOV-04]", got)
	}
}

// A carried copy retired some other way is not revived when its source is
// listed: the restore only undoes the import's own retirement.
func TestCarriedEdges_RestoreLeavesAnEdgeRetiredOtherwise(t *testing.T) {
	pool := carriedFixture(t)
	byo, sample := byoCarried(t, pool)
	if _, err := pool.Exec(context.Background(), `
		UPDATE fw_to_scf_edges e SET retired_at = now()
		FROM scf_anchors a, framework_versions av
		WHERE a.id = e.scf_anchor_id AND av.id = a.framework_version_id
		  AND av.version = $1 AND a.scf_id = 'GOV-04' AND e.crosswalk_source LIKE 'scf-remap:%'`, byo); err != nil {
		t.Fatalf("retire the carried copy by hand: %v", err)
	}

	rep := importCarried(t, pool, carriedCrosswalk(sample, "GOV-02", "GOV-04"))
	assertEdges(t, pool,
		"GOV-02@"+byo+":live", "GOV-02@"+sample+":live",
		"GOV-04@"+byo+":retired", "GOV-04@"+sample+":live")
	if len(rep.EdgesCarriedRestored) != 0 {
		t.Errorf("EdgesCarriedRestored = %v; want none", rep.EdgesCarriedRestored)
	}
}

// An import from another source does not follow this crosswalk's copies:
// it owns neither the root nor the copy.
func TestCarriedEdges_AnotherSourceLeavesTheCopy(t *testing.T) {
	pool := carriedFixture(t)
	byo, sample := byoCarried(t, pool)
	other := carriedCrosswalk(sample, "GOV-02")
	other.Source = "yaml:other-3065.yaml"

	rep := importCarried(t, pool, other)
	if len(rep.EdgesCarriedRetired) != 0 {
		t.Errorf("EdgesCarriedRetired = %v; want none", rep.EdgesCarriedRetired)
	}
	assertEdges(t, pool,
		"GOV-02@"+byo+":live", "GOV-02@"+sample+":live",
		"GOV-04@"+byo+":live", "GOV-04@"+sample+":live")
}
