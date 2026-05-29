//go:build integration

package fsstore_test

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/mgoodric/security-atlas/internal/auth/jwt"
	"github.com/mgoodric/security-atlas/internal/auth/keystore/fsstore"
	"github.com/mgoodric/security-atlas/internal/auth/tokensign"
)

// copyFile copies src to dst at mode 0600 (the keystore file mode).
func copyFile(t *testing.T, src, dst string) {
	t.Helper()
	data, err := os.ReadFile(src)
	if err != nil {
		t.Fatalf("read %s: %v", src, err)
	}
	if err := os.WriteFile(dst, data, 0o600); err != nil {
		t.Fatalf("write %s: %v", dst, err)
	}
}

// neutralSubject is a deliberately non-token-shaped string. Test
// fixtures must never embed real/vendor token prefixes (eyJ.../ghp_/...)
// because the CI secret scanner flags them even in test files.
const neutralSubject = "user:rotation-integration-subject"

// TestIntegrationRotate_OldKeyStillVerifiesDuringOverlap covers AC-3: a
// JWT signed with the pre-rotation key still verifies after Rotate,
// because the old key remains in the verification set for the overlap
// window.
func TestIntegrationRotate_OldKeyStillVerifiesDuringOverlap(t *testing.T) {
	dir := t.TempDir()
	store, err := fsstore.Open(dir)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	signer := tokensign.New(store)

	// Sign a token with the ORIGINAL key.
	claims := jwt.AtlasClaims{}
	claims.Subject = neutralSubject
	tok, err := signer.Sign(context.Background(), claims)
	if err != nil {
		t.Fatalf("Sign pre-rotation: %v", err)
	}
	preKID, err := tokensign.PeekKeyID(tok)
	if err != nil {
		t.Fatalf("PeekKeyID: %v", err)
	}

	// Rotate. The new key becomes the active signer; the old key stays
	// in the verification set.
	waitForNextKeyIDSecond()
	if err := store.Rotate(context.Background()); err != nil {
		t.Fatalf("Rotate: %v", err)
	}

	// The pre-rotation token must STILL verify (AC-3).
	got, err := signer.Verify(context.Background(), tok)
	if err != nil {
		t.Fatalf("Verify pre-rotation token after rotate: %v", err)
	}
	if got.Subject != neutralSubject {
		t.Fatalf("verified subject = %q, want %q", got.Subject, neutralSubject)
	}

	// And a freshly-signed token must use the NEW key.
	tok2, err := signer.Sign(context.Background(), claims)
	if err != nil {
		t.Fatalf("Sign post-rotation: %v", err)
	}
	postKID, _ := tokensign.PeekKeyID(tok2)
	if postKID == preKID {
		t.Fatalf("post-rotation token still signed with old kid %q", preKID)
	}
}

// TestIntegrationRotate_PrunedKeyTokenRejected covers AC-4: a JWT signed
// with a key that has been pruned past the overlap window is rejected
// with the "no verification key for kid" error.
func TestIntegrationRotate_PrunedKeyTokenRejected(t *testing.T) {
	dir := t.TempDir()

	// Plant an artificially-old key in a SOLO directory so it is the
	// active signer there, sign a token with it, then copy the SAME key
	// file into the main store so both stores share the exact keypair.
	oldKID := "20200101T000000Z"
	soloDir := t.TempDir()
	if err := fsstore.WriteTestKeyFile(soloDir, oldKID); err != nil {
		t.Fatalf("plant solo old key: %v", err)
	}
	soloStore, err := fsstore.Open(soloDir)
	if err != nil {
		t.Fatalf("open solo store: %v", err)
	}
	soloSigner := tokensign.New(soloStore)
	claims := jwt.AtlasClaims{}
	claims.Subject = neutralSubject
	oldTok, err := soloSigner.Sign(context.Background(), claims)
	if err != nil {
		t.Fatalf("sign with old key: %v", err)
	}
	gotKID, _ := tokensign.PeekKeyID(oldTok)
	if gotKID != oldKID {
		t.Fatalf("old token kid = %q, want %q", gotKID, oldKID)
	}

	// Open the main store first so it generates a fresh ACTIVE key
	// (a 2026 KID that sorts after the planted 2020 key), then copy the
	// exact old key file in and reopen so the store loads BOTH: the
	// fresh key stays active, the old key is a verifying-only key.
	if _, err := fsstore.Open(dir); err != nil {
		t.Fatalf("open main store (fresh key): %v", err)
	}
	copyFile(t, filepath.Join(soloDir, oldKID+".key"), filepath.Join(dir, oldKID+".key"))
	store, err := fsstore.Open(dir)
	if err != nil {
		t.Fatalf("reopen main store: %v", err)
	}
	signer := tokensign.New(store)

	// Before prune, the main store (which loaded the old key) verifies
	// the old token.
	if _, err := signer.Verify(context.Background(), oldTok); err != nil {
		t.Fatalf("pre-prune verify of old token should succeed: %v", err)
	}

	// Prune the old key (cutoff far in the future so the old key is past
	// it; the active signer is protected by P0-366-2).
	removed, err := store.Prune(context.Background(), time.Now().Add(365*24*time.Hour))
	if err != nil {
		t.Fatalf("Prune: %v", err)
	}
	if !containsString(removed, oldKID) {
		t.Fatalf("expected old kid %q pruned, removed %v", oldKID, removed)
	}

	// After prune, the old token is REJECTED (AC-4).
	_, err = signer.Verify(context.Background(), oldTok)
	if err == nil {
		t.Fatal("expected verify of pruned-key token to fail, got nil")
	}
	if !strings.Contains(err.Error(), "no verification key for kid") {
		t.Fatalf("expected 'no verification key for kid' error, got %v", err)
	}
}

// TestIntegrationRotate_NoRaceUnderConcurrentVerify covers AC-3's
// concurrency clause: Rotate while JWTs are mid-validation must not race.
// Run with -race to catch data races on the signing/verify state.
func TestIntegrationRotate_NoRaceUnderConcurrentVerify(t *testing.T) {
	dir := t.TempDir()
	store, err := fsstore.Open(dir)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	signer := tokensign.New(store)
	claims := jwt.AtlasClaims{}
	claims.Subject = neutralSubject
	tok, err := signer.Sign(context.Background(), claims)
	if err != nil {
		t.Fatalf("Sign: %v", err)
	}

	done := make(chan struct{})
	errs := make(chan error, 64)
	// Verifier goroutines hammer Verify while the main goroutine rotates.
	for i := 0; i < 8; i++ {
		go func() {
			for {
				select {
				case <-done:
					return
				default:
					if _, vErr := signer.Verify(context.Background(), tok); vErr != nil {
						// The token was signed with the original key; it
						// stays valid until pruned, which this test never
						// does — any error here is a real failure.
						errs <- vErr
						return
					}
				}
			}
		}()
	}
	for i := 0; i < 5; i++ {
		waitForNextKeyIDSecond()
		if rErr := store.Rotate(context.Background()); rErr != nil {
			close(done)
			t.Fatalf("Rotate iteration %d: %v", i, rErr)
		}
	}
	close(done)
	select {
	case err := <-errs:
		t.Fatalf("concurrent verify failed during rotation: %v", err)
	default:
	}
}
