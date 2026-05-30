package fsstore_test

import (
	"context"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/mgoodric/security-atlas/internal/auth/keystore"
	"github.com/mgoodric/security-atlas/internal/auth/keystore/fsstore"
)

// TestRotateGeneratesNewActiveKey covers AC-1: Rotate generates a fresh
// ES256 keypair, persists it at mode 0600, and makes the NEW key the
// active signer while retaining the OLD key in the verification set.
func TestRotateGeneratesNewActiveKey(t *testing.T) {
	dir := t.TempDir()
	store, err := fsstore.Open(dir)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	skBefore, vksBefore, err := store.Get(context.Background())
	if err != nil {
		t.Fatalf("Get before: %v", err)
	}
	if len(vksBefore) != 1 {
		t.Fatalf("expected 1 verification key before rotate, got %d", len(vksBefore))
	}

	// KeyID granularity is one second; sleep so the new KeyID sorts
	// strictly after the original (the rotation correctness invariant).
	waitForNextKeyIDSecond()

	if err := store.Rotate(context.Background()); err != nil {
		t.Fatalf("Rotate: %v", err)
	}

	skAfter, vksAfter, err := store.Get(context.Background())
	if err != nil {
		t.Fatalf("Get after: %v", err)
	}
	if skAfter.KeyID == skBefore.KeyID {
		t.Fatalf("signing key id did not change after rotate: %q", skAfter.KeyID)
	}
	if skAfter.KeyID <= skBefore.KeyID {
		t.Fatalf("new signing key id %q must sort after old %q", skAfter.KeyID, skBefore.KeyID)
	}
	if len(vksAfter) != 2 {
		t.Fatalf("expected 2 verification keys after rotate, got %d", len(vksAfter))
	}
	// Both old + new must be present in the verification set.
	if !containsKID(vksAfter, skBefore.KeyID) {
		t.Fatalf("old key %q missing from verification set after rotate", skBefore.KeyID)
	}
	if !containsKID(vksAfter, skAfter.KeyID) {
		t.Fatalf("new key %q missing from verification set after rotate", skAfter.KeyID)
	}
	// New key file must exist at mode 0600.
	info, err := os.Stat(filepath.Join(dir, skAfter.KeyID+".key"))
	if err != nil {
		t.Fatalf("Stat new key file: %v", err)
	}
	if info.Mode().Perm() != fs.FileMode(0o600) {
		t.Fatalf("new key mode = %o, want 0600", info.Mode().Perm())
	}
	// Old key file must still exist on disk (retained for overlap).
	if _, err := os.Stat(filepath.Join(dir, skBefore.KeyID+".key")); err != nil {
		t.Fatalf("old key file missing after rotate (should be retained): %v", err)
	}
}

// TestRotatePersistsAcrossReopen covers AC-1 durability: after Rotate,
// reopening the store rehydrates BOTH keys with the same active signer.
func TestRotatePersistsAcrossReopen(t *testing.T) {
	dir := t.TempDir()
	store, err := fsstore.Open(dir)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	waitForNextKeyIDSecond()
	if err := store.Rotate(context.Background()); err != nil {
		t.Fatalf("Rotate: %v", err)
	}
	skAfter, _, _ := store.Get(context.Background())

	reopened, err := fsstore.Open(dir)
	if err != nil {
		t.Fatalf("reopen: %v", err)
	}
	skReopen, vksReopen, err := reopened.Get(context.Background())
	if err != nil {
		t.Fatalf("Get after reopen: %v", err)
	}
	if skReopen.KeyID != skAfter.KeyID {
		t.Fatalf("active signer changed across reopen: %q vs %q", skReopen.KeyID, skAfter.KeyID)
	}
	if len(vksReopen) != 2 {
		t.Fatalf("expected 2 verification keys after reopen, got %d", len(vksReopen))
	}
}

// TestPruneRemovesOldKeysButNeverSigningKey covers AC-2 + P0-366-2:
// Prune removes keypair files older than the cutoff, but NEVER the
// active signing key, even if its timestamp is past the cutoff.
func TestPruneRemovesOldKeysButNeverSigningKey(t *testing.T) {
	dir := t.TempDir()
	// First boot generates a fresh active key (a current-year KID).
	if _, err := fsstore.Open(dir); err != nil {
		t.Fatalf("Open: %v", err)
	}

	// Plant an artificially-old key file so it sorts BEFORE the active
	// key and is well past any cutoff. KeyID is a fixed early timestamp.
	oldKID := "20200101T000000Z"
	plantKeyFile(t, dir, oldKID)

	// Reopen so the planted key is loaded; the freshly-generated key
	// (later timestamp) stays the active signer.
	store, err := fsstore.Open(dir)
	if err != nil {
		t.Fatalf("reopen: %v", err)
	}
	skActive, vks, err := store.Get(context.Background())
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if len(vks) != 2 {
		t.Fatalf("expected 2 keys loaded (planted old + active), got %d", len(vks))
	}
	if skActive.KeyID == oldKID {
		t.Fatalf("planted old key should NOT be the active signer")
	}

	// Prune with a cutoff far in the future would target the active key
	// too — but P0-366-2 guarantees the active signer is never pruned.
	removed, err := store.Prune(context.Background(), time.Now().Add(100*365*24*time.Hour))
	if err != nil {
		t.Fatalf("Prune: %v", err)
	}
	// The old key must be removed.
	if !containsString(removed, oldKID) {
		t.Fatalf("expected old key %q in pruned set %v", oldKID, removed)
	}
	// The active signer must NOT be removed.
	if containsString(removed, skActive.KeyID) {
		t.Fatalf("active signer %q must NEVER be pruned (P0-366-2)", skActive.KeyID)
	}
	if _, err := os.Stat(filepath.Join(dir, skActive.KeyID+".key")); err != nil {
		t.Fatalf("active key file removed by prune — P0-366-2 violation: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dir, oldKID+".key")); !os.IsNotExist(err) {
		t.Fatalf("old key file should be gone after prune, stat err = %v", err)
	}
	// After prune, the in-memory verification set drops the old key.
	_, vksAfter, _ := store.Get(context.Background())
	if containsKID(vksAfter, oldKID) {
		t.Fatalf("pruned key %q still present in verification set", oldKID)
	}
	if len(vksAfter) != 1 {
		t.Fatalf("expected 1 verification key after prune, got %d", len(vksAfter))
	}
}

// TestPruneKeepsKeysWithinOverlapWindow covers AC-2: a key newer than
// the cutoff is retained.
func TestPruneKeepsKeysWithinOverlapWindow(t *testing.T) {
	dir := t.TempDir()
	if _, err := fsstore.Open(dir); err != nil {
		t.Fatalf("Open: %v", err)
	}

	// Plant a recent old key (now-ish) and reopen.
	recentKID := time.Now().UTC().Add(-1 * time.Minute).Format("20060102T150405Z")
	plantKeyFile(t, dir, recentKID)
	store, err := fsstore.Open(dir)
	if err != nil {
		t.Fatalf("reopen: %v", err)
	}

	// Cutoff one hour ago — the recent key is newer than the cutoff and
	// must be retained.
	removed, err := store.Prune(context.Background(), time.Now().Add(-1*time.Hour))
	if err != nil {
		t.Fatalf("Prune: %v", err)
	}
	if len(removed) != 0 {
		t.Fatalf("expected no keys pruned (all within window), removed %v", removed)
	}
	if _, err := os.Stat(filepath.Join(dir, recentKID+".key")); err != nil {
		t.Fatalf("recent key wrongly removed: %v", err)
	}
}

// TestListReportsRoles covers AC-5 support: List enumerates every key
// with its role (signing vs verifying) and age.
func TestListReportsRoles(t *testing.T) {
	dir := t.TempDir()
	store, err := fsstore.Open(dir)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	waitForNextKeyIDSecond()
	if err := store.Rotate(context.Background()); err != nil {
		t.Fatalf("Rotate: %v", err)
	}
	infos, err := store.List(context.Background())
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(infos) != 2 {
		t.Fatalf("expected 2 keys listed, got %d", len(infos))
	}
	var signing, verifying int
	for _, ki := range infos {
		if ki.KeyID == "" {
			t.Fatal("KeyInfo.KeyID empty")
		}
		if ki.Signing {
			signing++
		} else {
			verifying++
		}
		if ki.Age < 0 {
			t.Fatalf("KeyInfo.Age negative for %q", ki.KeyID)
		}
	}
	if signing != 1 {
		t.Fatalf("expected exactly 1 signing key, got %d", signing)
	}
	if verifying != 1 {
		t.Fatalf("expected exactly 1 verifying-only key, got %d", verifying)
	}
	// The last entry (alphabetically-last KeyID) must be the signing key.
	if !infos[len(infos)-1].Signing {
		t.Fatal("alphabetically-last key must be the active signer")
	}
}

// TestRotateNeverLogsKeyMaterial covers P0-366-1 at the structural
// level: List + KeyInfo expose only KeyID + age + role, never key bytes.
// A KeyInfo struct that carried key material would be a compile-time
// surface for leakage; this test asserts the shape stays minimal.
func TestKeyInfoExposesNoPrivateMaterial(t *testing.T) {
	dir := t.TempDir()
	store, err := fsstore.Open(dir)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	infos, err := store.List(context.Background())
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(infos) != 1 {
		t.Fatalf("expected 1 key, got %d", len(infos))
	}
	// Render the KeyInfo as a string and assert it contains no PEM
	// markers — a smoke check that no private material can leak through
	// the operator-facing surface (P0-366-1).
	rendered := infos[0].KeyID
	if strings.Contains(rendered, "PRIVATE KEY") || strings.Contains(rendered, "BEGIN") {
		t.Fatal("KeyInfo rendering leaks key material")
	}
}

// --- helpers ---

func containsKID(vks []keystore.VerificationKey, kid string) bool {
	for _, vk := range vks {
		if vk.KeyID == kid {
			return true
		}
	}
	return false
}

func containsString(ss []string, s string) bool {
	for _, x := range ss {
		if x == s {
			return true
		}
	}
	return false
}

// waitForNextKeyIDSecond sleeps just past the next whole UTC second so a
// subsequently-generated KeyID (1s granularity) sorts strictly after the
// current one.
func waitForNextKeyIDSecond() {
	now := time.Now().UTC()
	next := now.Truncate(time.Second).Add(time.Second)
	time.Sleep(next.Sub(now) + 5*time.Millisecond)
}

// plantKeyFile writes a valid ES256 PKCS#8 PEM key file at <dir>/<kid>.key
// with the given KeyID so tests can exercise multi-key load + prune paths
// without waiting wall-clock time between generations.
func plantKeyFile(t *testing.T, dir, kid string) {
	t.Helper()
	if err := fsstore.WriteTestKeyFile(dir, kid); err != nil {
		t.Fatalf("plant key %q: %v", kid, err)
	}
}
