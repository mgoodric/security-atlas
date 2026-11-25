//go:build integration

package soc2import_test

// Issue #3061: an install that sets SCF_CATALOG to its own SCF release
// (docs/SELF_HOSTING.md, "Bring your own SCF release") never loaded the
// bundled sample, but the stock controls, crosswalks and policies declare
// the sample's release (#1773, #3033), so the bootstrap failed. The
// bootstrap now loads the sample as a legacy release beside the operator's
// (`import-scf --keep-current`), uploads the stock content into it, then
// re-imports the operator's catalog so the re-bind (#1971) and edge carry
// (#2315) move the stock content onto the operator's release.
//
// bootSequence plays deploy/docker/bootstrap/bootstrap.sh's SCF, crosswalk
// and control-upload steps in the script's order, each through the same
// package call its atlas-cli command makes. The operator's release is SCF
// 2026.3 built from internal/api/scfseed/testdata/scf-pin.json: every real
// control ID and title, verbatim, with no descriptions (OQ #1).

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/mgoodric/security-atlas/internal/api/scfimport"
	"github.com/mgoodric/security-atlas/internal/api/soc2import"
	"github.com/mgoodric/security-atlas/internal/control"
	"github.com/mgoodric/security-atlas/internal/dbtest"
	"github.com/mgoodric/security-atlas/internal/policy/seed"
)

const byoRelease = "2026.3"

// realReleaseCatalog is SCF 2026.3 in the importer's JSON shape, from the
// pinned workbook extract.
func realReleaseCatalog(t *testing.T) *scfimport.Catalog {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join("..", "scfseed", "testdata", "scf-pin.json"))
	if err != nil {
		t.Fatal(err)
	}
	var pin struct {
		ReleaseVersion string `json:"release_version"`
		Controls       []struct {
			SCFID string `json:"scf_id"`
			Title string `json:"title"`
		} `json:"controls"`
	}
	if err := json.Unmarshal(raw, &pin); err != nil {
		t.Fatal(err)
	}
	if pin.ReleaseVersion != byoRelease {
		t.Fatalf("scf-pin.json is release %q; this test expects %q", pin.ReleaseVersion, byoRelease)
	}
	cat := scfimport.Catalog{SchemaVersion: scfimport.SchemaVersion, ReleaseVersion: byoRelease, ReleaseDate: "2026-09-01"}
	for _, c := range pin.Controls {
		family, _, _ := strings.Cut(c.SCFID, "-")
		cat.Controls = append(cat.Controls, scfimport.Control{SCFID: c.SCFID, Family: family, Title: c.Title})
	}
	// Write and Load it, so it passes the same validation SCF_CATALOG does.
	path := filepath.Join(t.TempDir(), "scf-2026.3.json")
	b, err := json.Marshal(cat)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, b, 0o600); err != nil {
		t.Fatal(err)
	}
	loaded, err := scfimport.Load(path)
	if err != nil {
		t.Fatalf("Load real-release fixture: %v", err)
	}
	return loaded
}

// importSCFLikeCLI is `atlas-cli catalog import-scf [--keep-current]`: the
// import, then the control re-bind, edge queue and overlay queue it runs
// after every import.
func importSCFLikeCLI(t *testing.T, pool *pgxpool.Pool, cat *scfimport.Catalog, keepCurrent bool) scfimport.Report {
	t.Helper()
	ctx := context.Background()
	rep, err := scfimport.ImportWithOptions(ctx, pool, cat, scfimport.Options{KeepCurrent: keepCurrent})
	if err != nil {
		t.Fatalf("import-scf %s (keep-current=%v): %v", cat.ReleaseVersion, keepCurrent, err)
	}
	if _, err := scfimport.RebindControls(ctx, pool); err != nil {
		t.Fatalf("RebindControls: %v", err)
	}
	if _, err := scfimport.QueueEdgeRemapProposals(ctx, pool); err != nil {
		t.Fatalf("QueueEdgeRemapProposals: %v", err)
	}
	if _, err := scfimport.QueueOverlayRemapProposals(ctx, pool); err != nil {
		t.Fatalf("QueueOverlayRemapProposals: %v", err)
	}
	return rep
}

// bootSequence is one run of bootstrap.sh's phases 3 and 5b. byo is nil for
// the default install (SCF_CATALOG is the sample).
func bootSequence(t *testing.T, pool, app *pgxpool.Pool, tenant string, byo *scfimport.Catalog) {
	t.Helper()
	sample := loadSCFSample(t)
	if byo == nil {
		importSCFLikeCLI(t, pool, sample, false)
	} else {
		importSCFLikeCLI(t, pool, byo, false)
		if rep := importSCFLikeCLI(t, pool, sample, true); rep.Status != "legacy" {
			t.Fatalf("sample loaded with --keep-current has status %q; want legacy", rep.Status)
		}
	}
	if _, err := soc2import.Import(context.Background(), pool, loadCrosswalk(t)); err != nil {
		t.Fatalf("import SOC 2 crosswalk: %v", err)
	}
	store := control.NewStore(app)
	for _, dir := range stockBundleDirs(t) {
		b, err := control.ParseDirectory(dir)
		if err != nil {
			t.Fatalf("parse %s: %v", dir, err)
		}
		if _, err := store.Upload(tenantCtx(t, tenant), b, "oauth_client:bootstrap"); err != nil {
			t.Fatalf("%s: upload: %v", dir, err)
		}
	}
	if byo != nil {
		importSCFLikeCLI(t, pool, byo, false)
	}
}

func currentSCFRelease(t *testing.T, pool *pgxpool.Pool) []string {
	t.Helper()
	return queryStrings(t, pool, `
		SELECT fv.version || ':' || fv.status FROM framework_versions fv
		JOIN frameworks f ON f.id = fv.framework_id
		WHERE f.slug = 'scf' AND f.tenant_id IS NULL
		ORDER BY fv.version`)
}

// controlSnapshot is each stock control's id, version and anchor release,
// keyed by bundle id.
func controlSnapshot(t *testing.T, pool *pgxpool.Pool, tenant string) map[string]string {
	t.Helper()
	rows := queryStrings(t, pool, `
		SELECT c.bundle_id || ' ' || c.id::text || ' v' || c.version::text || ' ' || coalesce(fv.version, '-')
		FROM controls c
		LEFT JOIN scf_anchors a ON a.id = c.scf_anchor_id
		LEFT JOIN framework_versions fv ON fv.id = a.framework_version_id
		WHERE c.tenant_id = $1 AND c.superseded_by IS NULL
		ORDER BY 1`, tenant)
	out := make(map[string]string, len(rows))
	for _, r := range rows {
		bundle, rest, _ := strings.Cut(r, " ")
		out[bundle] = rest
	}
	return out
}

// soc2CurrentCoverage lists the SOC 2 requirement codes with a live edge on
// an anchor of the current SCF release, which default coverage reads (#2203).
func soc2CurrentCoverage(t *testing.T, pool *pgxpool.Pool) []string {
	t.Helper()
	return queryStrings(t, pool, `
		SELECT DISTINCT fr.code
		FROM fw_to_scf_edges e
		JOIN framework_requirements fr ON fr.id = e.framework_requirement_id
		JOIN framework_versions rv ON rv.id = fr.framework_version_id
		JOIN frameworks rf ON rf.id = rv.framework_id
		JOIN scf_anchors a ON a.id = e.scf_anchor_id
		JOIN framework_versions av ON av.id = a.framework_version_id
		WHERE rf.slug = 'soc2' AND e.retired_at IS NULL AND av.status = 'current'
		ORDER BY 1`)
}

// soc2MappedRequirements lists every SOC 2 requirement code the crosswalk
// maps.
func soc2MappedRequirements(t *testing.T) []string {
	t.Helper()
	seen := map[string]bool{}
	for _, m := range loadCrosswalk(t).Mappings {
		seen[m.RequirementCode] = true
	}
	var out []string
	for c := range seen {
		out = append(out, c)
	}
	return out
}

func assertBYOEndState(t *testing.T, pool *pgxpool.Pool, tenant string) {
	t.Helper()
	sample := loadSCFSample(t).ReleaseVersion
	if got, want := strings.Join(currentSCFRelease(t, pool), ","), byoRelease+":current,"+sample+":legacy"; got != want {
		t.Fatalf("SCF releases = %s; want %s", got, want)
	}
	snap := controlSnapshot(t, pool, tenant)
	anchored := 0
	for bundle, s := range snap {
		release := s[strings.LastIndex(s, " ")+1:]
		if release == "-" {
			continue
		}
		anchored++
		if release != byoRelease {
			t.Errorf("%s: control on an anchor in %q; want %q (every sample title is the 2026.3 title)", bundle, release, byoRelease)
		}
	}
	if anchored == 0 {
		t.Fatal("no anchored stock control; the test would pass vacuously")
	}
	covered := map[string]bool{}
	for _, c := range soc2CurrentCoverage(t, pool) {
		covered[c] = true
	}
	mapped := soc2MappedRequirements(t)
	if len(mapped) == 0 {
		t.Fatal("the SOC 2 crosswalk maps nothing; the test would pass vacuously")
	}
	for _, c := range mapped {
		if !covered[c] {
			t.Errorf("SOC 2 %s has no live edge on a %s anchor", c, byoRelease)
		}
	}
}

func byoFixture(t *testing.T) (pool, app *pgxpool.Pool, tenant string) {
	t.Helper()
	pool = dbtest.NewMigratePool(t)
	app = dbtest.NewAppPool(t)
	wipeSCFReleases(t, pool)
	t.Cleanup(func() {
		wipeSCFReleases(t, pool)
		ensureSCFLoaded(t, pool)
	})
	tenant = dbtest.SeedTenant(t, pool, "control_manifest_revisions", "controls", "policies")
	return pool, app, tenant
}

// (1) A fresh install on its own release boots; the operator's release
// stays current and the stock content ends on its anchors.
func TestBYOBootstrap_FreshInstallBindsStockContentOnTheOperatorRelease(t *testing.T) {
	pool, app, tenant := byoFixture(t)
	bootSequence(t, pool, app, tenant, realReleaseCatalog(t))
	assertBYOEndState(t, pool, tenant)

	// `atlas-cli policy seed-stock`: the policies' declared release is loaded.
	policies, err := seed.LoadFromFS(os.DirFS(filepath.Join("..", "..", "..")), "policies/stock")
	if err != nil {
		t.Fatalf("LoadFromFS: %v", err)
	}
	rep, err := seed.Seed(context.Background(), app, uuid.MustParse(tenant), policies, seed.NewSQLAnchorResolver(pool))
	// Since #1774 a declared release that is not loaded is an error, so a
	// nil error means test-2026.3 resolved.
	if err != nil {
		t.Fatalf("seed-stock: %v", err)
	}
	if len(rep.Loaded) != len(policies) {
		t.Errorf("seed-stock loaded %d policies; want %d", len(rep.Loaded), len(policies))
	}
	// The known gap (docs/audit-log/3061 D5): the seed links a code to the
	// control bound in the declared release (#1772), and the stock controls
	// moved to the operator's release, so every link drops. Pinned here so a
	// fix to the resolver shows up as this test failing, and is updated with
	// it.
	codes := map[string]bool{}
	for _, p := range policies {
		for _, c := range p.FrontMatter.LinkedControlIDs {
			codes[c] = true
		}
	}
	for _, p := range rep.Loaded {
		if p.LinkedControlCount != 0 {
			t.Errorf("%s linked %d controls; D5 measured 0", p.Title, p.LinkedControlCount)
		}
	}
	if len(rep.MissingAnchors) != 15 || len(codes) != 15 {
		t.Errorf("MissingAnchors = %v (%d distinct codes linked); D5 measured all 15 missing", rep.MissingAnchors, len(codes))
	}
}

// stockCrosswalkCodes returns, sorted, the SCF codes both a stock control
// bundle and the SOC 2 crosswalk name.
func stockCrosswalkCodes(t *testing.T) []string {
	t.Helper()
	inCrosswalk := map[string]bool{}
	for _, m := range loadCrosswalk(t).Mappings {
		inCrosswalk[m.SCFAnchor] = true
	}
	seen := map[string]bool{}
	var out []string
	for _, dir := range stockBundleDirs(t) {
		b, err := control.ParseDirectory(dir)
		if err != nil {
			t.Fatalf("parse %s: %v", dir, err)
		}
		if c := b.Manifest.SCFAnchorID; c != "" && inCrosswalk[c] && !seen[c] {
			seen[c] = true
			out = append(out, c)
		}
	}
	slices.Sort(out)
	return out
}

// openEdgeProposals counts the open edge-remap proposals for edges on the
// sample's anchor with this code.
func openEdgeProposals(t *testing.T, pool *pgxpool.Pool, release, code string) int {
	t.Helper()
	return countRows(t, pool, `
		SELECT count(*) FROM scf_edge_remap_proposals
		WHERE legacy_release = $1 AND legacy_scf_id = $2 AND status = 'open'`, release, code)
}

// (5) Where the operator's release differs from the sample, nothing moves on
// its own: a code missing from the release leaves the stock control for
// re-anchoring, and an edge whose title changed or whose code is gone waits
// in the edge-remap queue. A stock control whose title alone changed still
// moves (#2321).
func TestBYOBootstrap_DifferingAnchorsAreLeftForReview(t *testing.T) {
	pool, app, tenant := byoFixture(t)
	codes := stockCrosswalkCodes(t)
	if len(codes) < 2 {
		t.Fatalf("need two codes shared by a stock control and the SOC 2 crosswalk; have %v", codes)
	}
	retitled, removed := codes[0], codes[1]
	byo := realReleaseCatalog(t)
	var kept []scfimport.Control
	for _, c := range byo.Controls {
		switch c.SCFID {
		case removed:
			continue
		case retitled:
			c.Title += " (reworded)"
		}
		kept = append(kept, c)
	}
	if len(kept) != len(byo.Controls)-1 {
		t.Fatalf("%s is not in the 2026.3 fixture", removed)
	}
	byo.Controls = kept

	bootSequence(t, pool, app, tenant, byo)
	sample := loadSCFSample(t).ReleaseVersion

	rep, err := scfimport.RebindControls(context.Background(), pool)
	if err != nil {
		t.Fatalf("RebindControls: %v", err)
	}
	var reanchor []string
	for _, c := range rep.NeedsReanchoring {
		if c.TenantID.String() == tenant {
			reanchor = append(reanchor, c.LegacySCFID)
		}
	}
	if !slices.Contains(reanchor, removed) {
		t.Errorf("needs re-anchoring = %v; want the stock control on %s, which the release lacks", reanchor, removed)
	}
	if slices.Contains(reanchor, retitled) {
		t.Errorf("needs re-anchoring = %v; the stock control on %s changed title only and should have moved (#2321)", reanchor, retitled)
	}
	for _, code := range []string{retitled, removed} {
		if n := openEdgeProposals(t, pool, sample, code); n == 0 {
			t.Errorf("no open edge-remap proposal for the SOC 2 edge on %s", code)
		}
	}
	if n := openEdgeProposals(t, pool, sample, codes[len(codes)-1]); n != 0 && len(codes) > 2 {
		t.Errorf("%d open edge-remap proposals on %s, whose code and title match", n, codes[len(codes)-1])
	}
}

// (4) Every later boot is a no-op.
func TestBYOBootstrap_RerunIsIdempotent(t *testing.T) {
	pool, app, tenant := byoFixture(t)
	byo := realReleaseCatalog(t)
	bootSequence(t, pool, app, tenant, byo)
	controls := controlSnapshot(t, pool, tenant)
	edges := countRows(t, pool, `SELECT count(*) FROM fw_to_scf_edges`)
	live := countRows(t, pool, `SELECT count(*) FROM fw_to_scf_edges WHERE retired_at IS NULL`)
	proposals := countRows(t, pool, `SELECT count(*) FROM scf_edge_remap_proposals`)

	bootSequence(t, pool, app, tenant, byo)
	assertBYOEndState(t, pool, tenant)
	after := controlSnapshot(t, pool, tenant)
	for bundle, s := range controls {
		if after[bundle] != s {
			t.Errorf("%s: %s after the first boot, %s after the second", bundle, s, after[bundle])
		}
	}
	if got := countRows(t, pool, `SELECT count(*) FROM fw_to_scf_edges`); got != edges {
		t.Errorf("edges: %d after the first boot, %d after the second", edges, got)
	}
	if got := countRows(t, pool, `SELECT count(*) FROM fw_to_scf_edges WHERE retired_at IS NULL`); got != live {
		t.Errorf("live edges: %d after the first boot, %d after the second", live, got)
	}
	if got := countRows(t, pool, `SELECT count(*) FROM scf_edge_remap_proposals`); got != proposals {
		t.Errorf("edge remap proposals: %d after the first boot, %d after the second", proposals, got)
	}
}

// (2) An install bootstrapped on its own release before #3035 stored the
// unpinned stock manifests and crosswalk edges on that release's anchors.
// Its next boot loads the sample as legacy, never as current, and keeps
// every stock control's id and version.
func TestBYOBootstrap_UpgradeFromUnpinnedInstallKeepsTheOperatorReleaseCurrent(t *testing.T) {
	pool, app, tenant := byoFixture(t)
	ctx := context.Background()
	byo := realReleaseCatalog(t)
	importSCFLikeCLI(t, pool, byo, false)

	// The pre-#3035 crosswalk declared no release and bound in the current
	// one. A release is required since #1774, so name that release: the
	// same edges on the same anchors.
	cw := loadCrosswalk(t)
	cw.SCFRelease = byoRelease
	if _, err := soc2import.Import(ctx, pool, cw); err != nil {
		t.Fatalf("pre-#3035 crosswalk: %v", err)
	}

	// The pre-#1773 stock bytes bound in the current release. Upload each
	// bundle pinned to the operator's release, then store the unpinned
	// shipped manifest and its hash, as that install holds them.
	sample := loadSCFSample(t).ReleaseVersion
	pin := []byte("scf_release: " + sample + "\n")
	store := control.NewStore(app)
	for _, dir := range stockBundleDirs(t) {
		shipped, err := os.ReadFile(filepath.Join(dir, "control.yaml"))
		if err != nil {
			t.Fatal(err)
		}
		before := strings.Replace(string(shipped), string(pin), "", 1)
		if before == string(shipped) {
			t.Fatalf("%s: control.yaml does not declare %s", dir, pin)
		}
		prior, err := control.FinalizeBundleForHTTP([]byte(before))
		if err != nil {
			t.Fatalf("%s: parse pre-#1773 manifest: %v", dir, err)
		}
		pinnedBYO, err := control.FinalizeBundleForHTTP([]byte(strings.Replace(string(shipped), string(pin), "scf_release: \""+byoRelease+"\"\n", 1)))
		if err != nil {
			t.Fatalf("%s: parse manifest pinned to %s: %v", dir, byoRelease, err)
		}
		res, err := store.Upload(tenantCtx(t, tenant), pinnedBYO, "oauth_client:bootstrap")
		if err != nil {
			t.Fatalf("%s: upload: %v", dir, err)
		}
		if _, err := pool.Exec(ctx,
			`UPDATE controls SET bundle_manifest_yaml = $2, bundle_manifest_hash = $3 WHERE id = $1`,
			res.ControlID, before, prior.ManifestHashHex); err != nil {
			t.Fatalf("%s: store the pre-#1773 manifest: %v", dir, err)
		}
	}
	controls := controlSnapshot(t, pool, tenant)

	bootSequence(t, pool, app, tenant, byo)
	assertBYOEndState(t, pool, tenant)
	after := controlSnapshot(t, pool, tenant)
	for bundle, s := range controls {
		if after[bundle] != s {
			t.Errorf("%s: %s before the upgrade boot, %s after", bundle, s, after[bundle])
		}
	}
}

// (3) The default install (SCF_CATALOG is the sample) loads one release and
// binds the stock content in it, as before #3061.
func TestBYOBootstrap_DefaultInstallIsUnchanged(t *testing.T) {
	pool, app, tenant := byoFixture(t)
	bootSequence(t, pool, app, tenant, nil)
	sample := loadSCFSample(t).ReleaseVersion
	if got := strings.Join(currentSCFRelease(t, pool), ","); got != sample+":current" {
		t.Fatalf("SCF releases = %s; want %s:current", got, sample)
	}
	for bundle, s := range controlSnapshot(t, pool, tenant) {
		if release := s[strings.LastIndex(s, " ")+1:]; release != "-" && release != sample {
			t.Errorf("%s: control on an anchor in %q; want %q", bundle, release, sample)
		}
	}
}
