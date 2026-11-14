package scfseed_test

// Fixture release pin (#1971). An imported SCF release is frozen (#1768,
// migration 20260916000000): re-importing a release_version that is already
// loaded with different anchor content is a *scfimport.ReleaseConflictError,
// and the docker bootstrap imports migrations/fixtures/scf-sample.json on
// every upgrade. So any change to the sample's anchor content must ship
// under a new release_version, or every upgraded install fails to boot.
//
// This guard pins a hash of the sample's anchor content to its
// release_version. Changing a row without bumping the label fails here.
// When you change the sample: bump release_version, then add the new label
// with the hash this test prints. Never edit an existing entry.

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"testing"

	"github.com/mgoodric/security-atlas/internal/api/scfimport"
)

// fixtureContentByRelease maps each release_version the sample has shipped
// under to the sha256 of its anchor content (see fixtureContentHash).
var fixtureContentByRelease = map[string]string{
	"test-2026.3": "ffa4d182efd136e46b2867d04449980c6a656d8141168001bccc67e332651739",
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
