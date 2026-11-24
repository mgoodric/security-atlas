package scfseed_test

// Fixture release pin (#1971). An imported SCF release is frozen (#1768,
// migration 20260916000000): re-importing a release_version that is already
// loaded with different anchor content is a *scfimport.ReleaseConflictError,
// and the docker bootstrap imports migrations/fixtures/scf-sample.json on
// every upgrade. So any change to the sample's anchor content must ship
// under a new release_version, or every upgraded install fails to boot.
//
// This guard pins a hash of the sample's anchor content to its
// release_version, so a content change is a deliberate act, not an accident.
//
// Released labels are immutable: once a release ships with a label, never
// change its entry; ship changed content under a new label instead.
//
// test-2026.3 is frozen (#1773): the SCF 2026.3 remap of #1717 is complete,
// and the stock control bundles and bundled crosswalks declare
// scf_release: test-2026.3 (stockpin_test.go). Changed anchor content ships
// under a new label, and the stock content moves to it in the same PR.

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"testing"

	"github.com/mgoodric/security-atlas/internal/api/scfimport"
)

// fixtureContentByRelease maps each release_version the sample has carried
// to the sha256 of its anchor content (see fixtureContentHash). Every entry
// is frozen.
var fixtureContentByRelease = map[string]string{
	"test-2026.3": "a62477f251c4f83b71394a51c3bd42b94e2a9077b3d2c7347dde13e6844c2845",
}

// fixtureContentHash hashes exactly the anchor content scfimport freezes:
// scf_id, family, title, description and subtopics, in file order.
func fixtureContentHash(t *testing.T, cat *scfimport.Catalog) string {
	t.Helper()
	b, err := json.Marshal(cat.Controls)
	if err != nil {
		t.Fatalf("marshal controls: %v", err)
	}
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:])
}

func TestSampleFixtureContentIsPinnedToItsRelease(t *testing.T) {
	t.Parallel()
	cat, err := scfimport.Load(scfSamplePath)
	if err != nil {
		t.Fatalf("scfimport.Load(%s): %v", scfSamplePath, err)
	}
	got := fixtureContentHash(t, cat)
	want, ok := fixtureContentByRelease[cat.ReleaseVersion]
	if !ok {
		t.Fatalf("%s release_version %q has no content pin; add %q: %q to fixtureContentByRelease",
			scfSamplePath, cat.ReleaseVersion, cat.ReleaseVersion, got)
	}
	if got != want {
		t.Fatalf("%s anchor content changed under release_version %q (sha256 %s, pinned %s). "+
			"An imported release is frozen, so upgraded installs would fail to boot: "+
			"bump release_version and pin the new label to %s",
			scfSamplePath, cat.ReleaseVersion, got, want, got)
	}
}
