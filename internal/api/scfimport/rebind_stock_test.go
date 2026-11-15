package scfimport

import (
	"os"
	"path/filepath"
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
		{"stock, pinned", row("CHG-02", "", "soc2_cc8_1_change_management", stock+"scf_release: test-2026.1\n"), OutcomePinned},
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
