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
// The open working label is the exception. test-2026.3 is not released yet:
// each SCF 2026.3 family group of #1717 edits the sample and updates this
// entry to the hash the test prints, until the 2026.3 remap is complete and
// a release is cut, at which point the entry freezes. Between groups, a
// development install that already imported an earlier test-2026.3 state
// fails its next bootstrap with ReleaseConflictError (#1768) and needs a
// catalog reset (wipe the database or re-run `just scratch-pg-up`). No
// shipped install is affected, because none has imported test-2026.3.

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"testing"

	"github.com/mgoodric/security-atlas/internal/api/scfimport"
)

// fixtureContentByRelease maps each release_version the sample has carried
// to the sha256 of its anchor content (see fixtureContentHash). test-2026.3
// is the open working label (see above); every other entry is frozen.
var fixtureContentByRelease = map[string]string{
	"test-2026.3": "58b90d570ff34528aa1ad5a7d7f9a7d555fd0df0a211ae6219660bec3dcc852c",
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
