package scfimport

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5/pgtype"

	"github.com/mgoodric/security-atlas/internal/db/dbx"
)

// stockCC81 reads the shipped soc2_cc8_1_change_management manifest, the
// stock control on CHG-02 that motivated #2321.
func stockCC81(t *testing.T) string {
	t.Helper()
	b, err := os.ReadFile(filepath.Join("..", "..", "..", "controls", "soc2", "soc2_cc8_1_change_management", "control.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

// stockBeforePin returns the soc2_cc8_1_change_management manifest as it
// shipped before #1773 pinned the stock content: the shipped bytes without
// their scf_release line. Every install bootstrapped before #1773 stores
// these bytes, and they are on the stock list.
func stockBeforePin(t *testing.T) string {
	t.Helper()
	m := stockCC81(t)
	out := strings.Replace(m, "scf_release: test-2026.3\n", "", 1)
	if out == m {
		t.Fatal("shipped soc2_cc8_1_change_management manifest declares no scf_release: test-2026.3")
	}
	if !IsStockManifest("soc2_cc8_1_change_management", out) {
		t.Fatal("pre-#1773 soc2_cc8_1_change_management bytes are not on the stock list")
	}
	return out
}

// Every control.yaml under controls/soc2/ must be on the generated stock
// list, or an edit to it ships a manifest the re-bind treats as tenant
// authored. Regenerate with scripts/gen-stock-control-hashes.sh.
func TestStockManifestHashes_CoverControlsDir(t *testing.T) {
	t.Parallel()
	paths, err := filepath.Glob(filepath.Join("..", "..", "..", "controls", "soc2", "*", "control.yaml"))
	if err != nil || len(paths) == 0 {
		t.Fatalf("glob controls/soc2: %v (%d files)", err, len(paths))
	}
	for _, p := range paths {
		b, err := os.ReadFile(p)
		if err != nil {
			t.Fatal(err)
		}
		bundleID := filepath.Base(filepath.Dir(p))
		if !IsStockManifest(bundleID, string(b)) {
			t.Errorf("%s is not on the stock list; run bash scripts/gen-stock-control-hashes.sh", p)
		}
	}
}

func TestIsStockManifest(t *testing.T) {
	t.Parallel()
	m := stockCC81(t)
	cases := []struct {
		name, bundleID, manifest string
		want                     bool
	}{
		{"shipped bytes", "soc2_cc8_1_change_management", m, true},
		{"shipped bytes under another bundle id", "tenant_change_management", m, false},
		{"edited by the tenant", "soc2_cc8_1_change_management", m + "# tenant edit\n", false},
		{"tenant manifest reusing the stock bundle id", "soc2_cc8_1_change_management", "bundle_id: soc2_cc8_1_change_management\nscf_anchor_id: CHG-02\n", false},
	}
	for _, c := range cases {
		if got := IsStockManifest(c.bundleID, c.manifest); got != c.want {
			t.Errorf("%s: IsStockManifest = %v; want %v", c.name, got, c.want)
		}
	}
}

// The bundled sample's label in code matches the fixture the bootstrap
// imports (#3061).
func TestBundledSampleReleaseMatchesTheFixture(t *testing.T) {
	t.Parallel()
	cat, err := Load(filepath.Join("..", "..", "..", "migrations", "fixtures", "scf-sample.json"))
	if err != nil {
		t.Fatal(err)
	}
	if cat.ReleaseVersion != BundledSampleRelease {
		t.Fatalf("scf-sample.json is release %q; BundledSampleRelease is %q", cat.ReleaseVersion, BundledSampleRelease)
	}
}

// The current sample label is recognised, and non-sample labels are not.
// That every past label stays recognised is held against the frozen fixture
// record by scfseed's TestBundledSampleReleases_CoverEveryFixtureLabel (#3076).
func TestBundledSampleReleases_KeepsEveryLabel(t *testing.T) {
	t.Parallel()
	if !IsBundledSampleRelease(BundledSampleRelease) {
		t.Errorf("IsBundledSampleRelease(%q) = false; the current sample label is a sample label", BundledSampleRelease)
	}
	for _, label := range []string{"2026.3", "2027.1", ""} {
		if IsBundledSampleRelease(label) {
			t.Errorf("IsBundledSampleRelease(%q) = true; it is not a sample label", label)
		}
	}
}

// #3061: a stock control whose title alone changed moves forward (#2321),
// except off the bundled sample, whose titles are SCF 2026.3's: there only an
// exact code and title match moves it.
func TestStockTitleOnlyRebindable_NeverOffTheBundledSample(t *testing.T) {
	t.Parallel()
	reworded := "Configuration Change Control (reworded)"
	row := func(legacyRelease string) dbx.ListControlsOnLegacySCFAnchorsRow {
		return dbx.ListControlsOnLegacySCFAnchorsRow{
			BundleID:           "soc2_cc8_1_change_management",
			BundleManifestYaml: stockCC81(t),
			LegacyScfID:        "CHG-02",
			LegacyAnchorTitle:  "Configuration Change Control",
			LegacyRelease:      legacyRelease,
			CurrentRelease:     "2027.1",
			CurrentAnchorID:    pgtype.UUID{Bytes: [16]byte{1}, Valid: true},
			CurrentAnchorTitle: &reworded,
		}
	}
	cases := []struct {
		legacyRelease string
		want          bool
	}{
		{"2026.3", true},
		{BundledSampleRelease, false},
	}
	for _, c := range cases {
		got, err := StockTitleOnlyRebindable(row(c.legacyRelease))
		if err != nil {
			t.Fatalf("%s: %v", c.legacyRelease, err)
		}
		if got != c.want {
			t.Errorf("legacy release %s: StockTitleOnlyRebindable = %v; want %v", c.legacyRelease, got, c.want)
		}
	}
}

// #2321: a stock control re-binds when only the anchor's title changed; a
// tenant-authored one keeps the #1971 D3 rule, and a code that SCF reused
// or the remap table moved is not a title-only change, stock or not.
func TestClassify_StockTitleOnlyChange(t *testing.T) {
	t.Parallel()
	stock := stockCC81(t)
	row := func(scfID, legacyID, bundleID, manifest string) dbx.ListControlsOnLegacySCFAnchorsRow {
		r := legacy(scfID, "Change Control Process", "Change Management Program", true, "")
		r.CurrentAnchorLegacyScfID = legacyID
		r.BundleID = bundleID
		r.BundleManifestYaml = manifest
		return r
	}
	const tenant = "bundle_id: tenant_chg\nscf_anchor_id: CHG-02\n"
	cases := []struct {
		name string
		row  dbx.ListControlsOnLegacySCFAnchorsRow
		want Outcome
	}{
		{"stock, title only", row("CHG-02", "", "soc2_cc8_1_change_management", stock), OutcomeRebindable},
		{"stock, SCF lists the code as its own legacy ID", row("CHG-02", "CHG-02", "soc2_cc8_1_change_management", stock), OutcomeRebindable},
		{"tenant, title only", row("CHG-02", "", "tenant_chg", tenant), OutcomeNeedsReanchoring},
		{"stock bundle id, tenant bytes", row("CHG-02", "", "soc2_cc8_1_change_management", tenant), OutcomeNeedsReanchoring},
		{"stock, code reused", row("CHG-02", "CHG-09", "soc2_cc8_1_change_management", stock), OutcomeNeedsReanchoring},
		{"stock, code remapped", row("IAC-10", "", "soc2_cc8_1_change_management", stock), OutcomeNeedsReanchoring},
		// #1773: the shipped manifest declares scf_release, and is still
		// classified as unpinned (the cases above). A tenant pinned
		// manifest stays pinned, and so does a copy of the stock bytes
		// with one byte changed: a tenant cannot opt in.
		{"stock, before its scf_release (#1773)", row("CHG-02", "", "soc2_cc8_1_change_management", stockBeforePin(t)), OutcomeRebindable},
		{"tenant, pinned", row("CHG-02", "", "tenant_chg", tenant+"scf_release: test-2026.1\n"), OutcomePinned},
		{"stock bundle id, stock bytes plus one byte", row("CHG-02", "", "soc2_cc8_1_change_management", stock+"\n"), OutcomePinned},
	}
	for _, c := range cases {
		_, got, err := Classify(c.row)
		if err != nil {
			t.Fatalf("%s: %v", c.name, err)
		}
		if got != c.want {
			t.Errorf("%s: Classify = %v; want %v", c.name, got, c.want)
		}
	}
	gone := row("CHG-02", "", "soc2_cc8_1_change_management", stock)
	gone.CurrentAnchorID, gone.CurrentAnchorTitle = pgtype.UUID{}, nil
	if _, got, err := Classify(gone); err != nil || got != OutcomeNeedsReanchoring {
		t.Errorf("stock, code gone: Classify = %v, %v; want needs re-anchoring", got, err)
	}
}
