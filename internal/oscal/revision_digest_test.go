package oscal

import "testing"

// #1742: the bundle signature covers the freeze revision and its hash, so a
// bundle cannot be relabelled with another revision; a bundle with no
// revision (exported before #1742) keeps the digest it was signed with.
func TestBundleDigest_CoversFreezeRevision(t *testing.T) {
	t.Parallel()
	members := []BundleMember{{Filename: "ssp.json", SHA256: "aa"}, {Filename: "ap.json", SHA256: "bb"}}
	legacy := &Bundle{Members: members}
	legacyLines := &Bundle{Members: members}
	if bundleDigest(legacy) != bundleDigest(legacyLines) {
		t.Fatal("digest is not deterministic")
	}
	rev1 := &Bundle{Members: members, FreezeRevision: 1, RevisionHash: "h1"}
	rev2 := &Bundle{Members: members, FreezeRevision: 2, RevisionHash: "h2"}
	relabelled := &Bundle{Members: members, FreezeRevision: 1, RevisionHash: "h2"}
	if bundleDigest(rev1) == bundleDigest(legacy) {
		t.Fatal("revision 1 digest equals the no-revision digest")
	}
	if bundleDigest(rev1) == bundleDigest(rev2) || bundleDigest(rev2) == bundleDigest(relabelled) {
		t.Fatal("changing the revision or its hash does not change the digest")
	}
	m := rev2.Manifest()
	if m.FreezeRevision != 2 || m.RevisionHash != "h2" || !m.AmendedByImport {
		t.Fatalf("manifest = %+v", m)
	}
	if rev1.Manifest().AmendedByImport {
		t.Fatal("revision 1 manifest reads amended")
	}
}
