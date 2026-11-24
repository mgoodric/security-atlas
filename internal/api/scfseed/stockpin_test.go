package scfseed_test

// Stock content release pin (issue #1773). The stock control bundles
// (controls/soc2/) and the bundled crosswalks (data/crosswalks/) declare the
// SCF release of the sample catalog the bootstrap imports
// (migrations/fixtures/scf-sample.json), and every code they name exists in
// it. So a fresh install binds every stock
// code in the release it ships with, never in whatever release is current,
// and never with the "declares no scf_release" deprecation warning.
//
// The sample's label is "test-" + the real SCF release it was regenerated
// from (scfPinRelease). The prefix marks a 77-row subset with
// security-atlas's own descriptions: labelling it "2026.3" would freeze
// that label (#1768) and refuse the real SCF 2026.3 workbook under its own
// name. docs/audit-log/1773-stock-scf-release-pin-decisions.md.

import (
	"path/filepath"
	"testing"

	"github.com/mgoodric/security-atlas/internal/api/scfimport"
	"github.com/mgoodric/security-atlas/internal/api/soc2import"
	"github.com/mgoodric/security-atlas/internal/control"
)

const repoRootRel = "../../.."

// sampleRelease loads the sample catalog and returns its release label and
// the set of its scf_ids.
func sampleRelease(t *testing.T) (string, map[string]bool) {
	t.Helper()
	cat, err := scfimport.Load(scfSamplePath)
	if err != nil {
		t.Fatalf("scfimport.Load(%s): %v", scfSamplePath, err)
	}
	ids := make(map[string]bool, len(cat.Controls))
	for _, c := range cat.Controls {
		ids[c.SCFID] = true
	}
	return cat.ReleaseVersion, ids
}

func TestSampleReleaseLabelNamesThePinnedSCFRelease(t *testing.T) {
	t.Parallel()
	release, _ := sampleRelease(t)
	if want := "test-" + scfPinRelease; release != want {
		t.Fatalf("%s release_version = %q; want %q (test- + the pinned SCF release)", scfSamplePath, release, want)
	}
}

func TestStockControlBundlesPinTheSampleRelease(t *testing.T) {
	t.Parallel()
	release, ids := sampleRelease(t)
	dirs, err := filepath.Glob(filepath.Join(repoRootRel, "controls", "soc2", "*", "control.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	if len(dirs) == 0 {
		t.Fatal("no stock control bundles found; the test would pass vacuously")
	}
	for _, path := range dirs {
		b, err := control.ParseDirectory(filepath.Dir(path))
		if err != nil {
			t.Errorf("%s: %v", path, err)
			continue
		}
		if b.Manifest.SCFRelease != release {
			t.Errorf("%s: scf_release = %q; want %q", path, b.Manifest.SCFRelease, release)
		}
		if code := b.Manifest.SCFAnchorID; code != "" && !ids[code] {
			t.Errorf("%s: scf_anchor_id %s is not in SCF release %s", path, code, release)
		}
	}
}

func TestBundledCrosswalksPinTheSampleRelease(t *testing.T) {
	t.Parallel()
	release, ids := sampleRelease(t)
	files, err := filepath.Glob(filepath.Join(repoRootRel, "data", "crosswalks", "*.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	if len(files) == 0 {
		t.Fatal("no bundled crosswalks found; the test would pass vacuously")
	}
	for _, path := range files {
		cw, err := soc2import.Load(path)
		if err != nil {
			t.Errorf("%s: %v", path, err)
			continue
		}
		if cw.SCFRelease != release {
			t.Errorf("%s: scf_release = %q; want %q", path, cw.SCFRelease, release)
		}
		for _, m := range cw.Mappings {
			if !ids[m.SCFAnchor] {
				t.Errorf("%s: %s maps to %s, which is not in SCF release %s", path, m.RequirementCode, m.SCFAnchor, release)
			}
		}
	}
}
