// Package fsstore is the filesystem-backed implementation of the
// keystore.KeyStore interface. Private keys are written as PKCS#8 PEM
// files at mode 0600; the matching public key for JWKS publication is
// derived on read.
//
// On first boot the directory is empty: Open creates a fresh ES256
// keypair and writes it to <dir>/<kid>.key. On subsequent boots Open
// rescans the directory and rehydrates every present keypair; the
// alphabetically-last KeyID is treated as the active signing key, with
// the rest retained as verification keys (the rotation overlap shape
// that lands end-to-end in a follow-on slice — see ADR-0003 § Key
// rotation strategy).
//
// Slice 187 anti-criterion P0-187-6: this file must NEVER log private
// key material. Only KeyIDs may appear in logs.
package fsstore

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"encoding/pem"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/mgoodric/security-atlas/internal/auth/keystore"
)

// DefaultPath is the compiled-in keystore directory used when
// ATLAS_KEYSTORE_PATH is unset and the caller does not pass an
// explicit override.
const DefaultPath = "/var/lib/security-atlas/keys/"

// keyFileExt is the suffix every private-key PEM file carries.
const keyFileExt = ".key"

// privateKeyFileMode is the only file mode the filesystem keystore
// accepts on its private-key files. Anti-criterion P0-187-5 makes this
// a load-bearing invariant.
const privateKeyFileMode os.FileMode = 0o600

// DefaultRotationOverlap is the slice-366 default overlap window: how
// long a rotated-out key is retained as a verification key beyond the
// access-token lifetime before it becomes eligible for pruning. 24h
// matches ADR-0003 § Key rotation strategy (24× the 1h access-token
// TTL). Operators can lengthen it; P0-366-3 forbids shrinking the
// effective retention below the access-token lifetime.
const DefaultRotationOverlap = 24 * time.Hour

// DefaultAccessTokenLifetime mirrors internal/api/oauth.AccessTokenLifetime
// (1h). Duplicated here as a const rather than imported because the
// keystore package must not depend on the api/oauth package (layering:
// oauth depends on keystore, never the reverse). The prune cutoff is
// now - AccessTokenLifetime - RotationOverlap, so a token in flight when
// its signing key rotates out keeps verifying for its full lifetime plus
// the overlap window (P0-366-3).
const DefaultAccessTokenLifetime = time.Hour

// PruneCutoff returns the timestamp before which keys are eligible for
// pruning: now - accessTokenLifetime - overlap. Keys whose KeyID
// timestamp is at or before the returned instant may be pruned (except
// the active signer — P0-366-2). Centralised so the CLI and the
// scheduled job compute the same cutoff.
func PruneCutoff(now time.Time, accessTokenLifetime, overlap time.Duration) time.Time {
	return now.Add(-accessTokenLifetime).Add(-overlap)
}

// Store is the concrete filesystem-backed KeyStore.
type Store struct {
	dir string

	mu      sync.RWMutex
	signing keystore.SigningKey
	verify  []keystore.VerificationKey
}

// ResolvePath chooses the keystore directory in this order:
//  1. explicit non-empty argument
//  2. ATLAS_KEYSTORE_PATH env var (if set + non-empty)
//  3. DefaultPath
func ResolvePath(explicit string) string {
	if explicit != "" {
		return explicit
	}
	if env := os.Getenv("ATLAS_KEYSTORE_PATH"); env != "" {
		return env
	}
	return DefaultPath
}

// Open initializes the store rooted at dir. If dir is empty,
// ResolvePath("") is used. The directory is created if absent; an
// existing keypair is rehydrated, and a fresh keypair is generated and
// persisted if none is found.
func Open(dir string) (*Store, error) {
	resolved := ResolvePath(dir)
	if err := os.MkdirAll(resolved, 0o700); err != nil {
		return nil, fmt.Errorf("fsstore: mkdir %s: %w", resolved, err)
	}
	s := &Store{dir: resolved}
	if err := s.load(); err != nil {
		return nil, err
	}
	if s.signing.Key == nil {
		if err := s.generate(); err != nil {
			return nil, err
		}
	}
	return s, nil
}

// Get returns the active signing key and the full verification set.
func (s *Store) Get(_ context.Context) (keystore.SigningKey, []keystore.VerificationKey, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if s.signing.Key == nil {
		return keystore.SigningKey{}, nil, errors.New("fsstore: no signing key loaded")
	}
	// Defensive copy of verify slice header (the keys themselves are
	// immutable public-key pointers).
	vks := make([]keystore.VerificationKey, len(s.verify))
	copy(vks, s.verify)
	return s.signing, vks, nil
}

// Rotate generates a fresh ES256 keypair, persists it to disk at mode
// 0600 (atomic temp+rename, same discipline as generate), and reloads
// the in-memory state so the new key becomes the active signer while
// every prior key remains in the verification set for the overlap
// window (slice 366 AC-1).
//
// Rotate relies on the KeyID format (yyyymmddThhmmssZ, 1-second
// granularity) being chronological-lexicographic: writing a newer KeyID
// makes it the alphabetically-last filename, which load() treats as the
// active signer. Two rotations within the same wall-clock second would
// collide on the KeyID; the retry below regenerates with a bumped
// timestamp until the new KeyID sorts strictly after the current
// signer. P0-366-1: this method NEVER logs private key material.
func (s *Store) Rotate(_ context.Context) error {
	s.mu.RLock()
	prevKID := s.signing.KeyID
	s.mu.RUnlock()

	priv, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return fmt.Errorf("fsstore: rotate generate: %w", err)
	}
	kid := newKeyID()
	// Guarantee the new KeyID sorts strictly after the current signer so
	// load() promotes it to active. If a rotation lands in the same
	// second as the previous key's KeyID, bump to the next second.
	for kid <= prevKID {
		kid = nextKeyIDAfter(kid)
	}
	path := filepath.Join(s.dir, kid+keyFileExt)
	if err := writePrivateKey(path, priv); err != nil {
		return err
	}
	// Reload so the in-memory signing/verify reflect the new + retained
	// keys exactly as a fresh boot would see them.
	if err := s.load(); err != nil {
		return fmt.Errorf("fsstore: rotate reload: %w", err)
	}
	return nil
}

// Prune removes keypair files whose KeyID timestamp is at or before
// cutoff, EXCEPT the active signing key, which is never removed
// regardless of its age (P0-366-2). It returns the KeyIDs that were
// removed and reloads the in-memory state so pruned keys drop out of the
// verification set. Callers compute cutoff as
// now - AccessTokenLifetime - RotationOverlap (P0-366-3 guarantees the
// minimum retention exceeds any in-flight token's lifetime).
//
// A KeyID that does not parse as the expected timestamp format is left
// untouched (defensive: never delete a file we cannot reason about).
func (s *Store) Prune(_ context.Context, cutoff time.Time) ([]string, error) {
	s.mu.RLock()
	signingKID := s.signing.KeyID
	verify := make([]keystore.VerificationKey, len(s.verify))
	copy(verify, s.verify)
	s.mu.RUnlock()

	removed := make([]string, 0)
	for _, vk := range verify {
		if vk.KeyID == signingKID {
			// P0-366-2: never prune the active signer.
			continue
		}
		issued, ok := parseKeyID(vk.KeyID)
		if !ok {
			continue
		}
		if issued.After(cutoff) {
			continue
		}
		path := filepath.Join(s.dir, vk.KeyID+keyFileExt)
		if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
			return removed, fmt.Errorf("fsstore: prune remove %s: %w", vk.KeyID, err)
		}
		removed = append(removed, vk.KeyID)
	}
	if len(removed) > 0 {
		if err := s.load(); err != nil {
			return removed, fmt.Errorf("fsstore: prune reload: %w", err)
		}
	}
	return removed, nil
}

// KeyInfo is the operator-facing description of one stored keypair. It
// carries ONLY the KeyID, the key's age, and its role — never any
// private (or public) key material (P0-366-1). It is the shape the
// `atlas-cli keys list` command renders.
type KeyInfo struct {
	KeyID   string
	Age     time.Duration
	Signing bool
}

// List returns one KeyInfo per stored keypair, oldest first (the same
// chronological-lexicographic order load() uses). Exactly one entry has
// Signing=true — the alphabetically-last KeyID. Age is computed from the
// KeyID timestamp; a KeyID that does not parse reports Age 0.
func (s *Store) List(_ context.Context) ([]KeyInfo, error) {
	s.mu.RLock()
	signingKID := s.signing.KeyID
	verify := make([]keystore.VerificationKey, len(s.verify))
	copy(verify, s.verify)
	s.mu.RUnlock()

	now := time.Now()
	infos := make([]KeyInfo, 0, len(verify))
	for _, vk := range verify {
		var age time.Duration
		if issued, ok := parseKeyID(vk.KeyID); ok {
			age = now.Sub(issued)
		}
		infos = append(infos, KeyInfo{
			KeyID:   vk.KeyID,
			Age:     age,
			Signing: vk.KeyID == signingKID,
		})
	}
	return infos, nil
}

// load rescans the directory for .key files and rehydrates the keypair
// set. Files are sorted by filename so KeyID ordering is deterministic.
func (s *Store) load() error {
	entries, err := os.ReadDir(s.dir)
	if err != nil {
		return fmt.Errorf("fsstore: read dir %s: %w", s.dir, err)
	}
	names := make([]string, 0, len(entries))
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		if !strings.HasSuffix(e.Name(), keyFileExt) {
			continue
		}
		names = append(names, e.Name())
	}
	sort.Strings(names)
	verify := make([]keystore.VerificationKey, 0, len(names))
	var signing keystore.SigningKey
	for i, name := range names {
		kid := strings.TrimSuffix(name, keyFileExt)
		path := filepath.Join(s.dir, name)
		priv, err := readPrivateKey(path)
		if err != nil {
			return fmt.Errorf("fsstore: read %s: %w", kid, err)
		}
		verify = append(verify, keystore.VerificationKey{KeyID: kid, Key: &priv.PublicKey})
		if i == len(names)-1 {
			signing = keystore.SigningKey{KeyID: kid, Key: priv}
		}
	}
	s.mu.Lock()
	s.signing = signing
	s.verify = verify
	s.mu.Unlock()
	return nil
}

// generate creates a new ES256 keypair, writes it to disk at mode 0600,
// and updates the in-memory state.
func (s *Store) generate() error {
	priv, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return fmt.Errorf("fsstore: ecdsa generate: %w", err)
	}
	kid := newKeyID()
	path := filepath.Join(s.dir, kid+keyFileExt)
	if err := writePrivateKey(path, priv); err != nil {
		return err
	}
	s.mu.Lock()
	s.signing = keystore.SigningKey{KeyID: kid, Key: priv}
	s.verify = []keystore.VerificationKey{{KeyID: kid, Key: &priv.PublicKey}}
	s.mu.Unlock()
	return nil
}

// readPrivateKey loads a PKCS#8-encoded PEM file. The file mode is NOT
// validated here — file-mode enforcement is a write-time concern (we
// chmod after every write); read-time validation lives in
// integration_test.go where it has the real filesystem to assert
// against.
func readPrivateKey(path string) (*ecdsa.PrivateKey, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	block, _ := pem.Decode(data)
	if block == nil {
		return nil, errors.New("fsstore: PEM decode failed")
	}
	key, err := x509.ParsePKCS8PrivateKey(block.Bytes)
	if err != nil {
		return nil, fmt.Errorf("fsstore: parse PKCS8: %w", err)
	}
	ec, ok := key.(*ecdsa.PrivateKey)
	if !ok {
		return nil, fmt.Errorf("fsstore: unexpected key type %T (want *ecdsa.PrivateKey)", key)
	}
	if ec.Curve != elliptic.P256() {
		return nil, fmt.Errorf("fsstore: unexpected curve %v (want P-256)", ec.Curve)
	}
	return ec, nil
}

// writePrivateKey marshals priv as a PKCS#8 PEM file and chmods the
// file to 0600. The write is atomic via a temp-file + rename — partial
// writes never leave a half-written key on disk.
func writePrivateKey(path string, priv *ecdsa.PrivateKey) error {
	der, err := x509.MarshalPKCS8PrivateKey(priv)
	if err != nil {
		return fmt.Errorf("fsstore: marshal PKCS8: %w", err)
	}
	pemBytes := pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: der})
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, pemBytes, privateKeyFileMode); err != nil {
		return fmt.Errorf("fsstore: write tmp: %w", err)
	}
	// Belt + suspenders: chmod after write in case the OS umask
	// widened the mode.
	if err := os.Chmod(tmp, privateKeyFileMode); err != nil {
		_ = os.Remove(tmp)
		return fmt.Errorf("fsstore: chmod tmp: %w", err)
	}
	if err := os.Rename(tmp, path); err != nil {
		_ = os.Remove(tmp)
		return fmt.Errorf("fsstore: rename: %w", err)
	}
	// Some filesystems (notably the cross-mount path) re-apply umask
	// on rename. Re-chmod the final path for safety.
	if err := os.Chmod(path, privateKeyFileMode); err != nil {
		return fmt.Errorf("fsstore: chmod final: %w", err)
	}
	return nil
}

// keyIDLayout is the time layout for KeyID strings. 16 ASCII chars,
// lexicographically sortable, no slashes or spaces.
const keyIDLayout = "20060102T150405Z"

// newKeyID returns a deterministic-by-time KeyID that sorts in
// chronological order so the latest-generated key is also the
// alphabetically-last filename.
func newKeyID() string {
	return time.Now().UTC().Format(keyIDLayout)
}

// parseKeyID parses a KeyID back into the UTC instant it encodes. The
// second return value is false for any string that does not match the
// keyIDLayout (e.g. an operator-renamed file); callers treat an
// unparseable KeyID as "do not reason about its age" — never prune it.
func parseKeyID(kid string) (time.Time, bool) {
	t, err := time.Parse(keyIDLayout, kid)
	if err != nil {
		return time.Time{}, false
	}
	return t, true
}

// nextKeyIDAfter returns the KeyID one second after the given KeyID.
// Used by Rotate to break a same-second collision so the freshly
// generated key sorts strictly after the current signer. If kid does
// not parse, it falls back to the current time (which is necessarily
// later than any sub-current KeyID in practice).
func nextKeyIDAfter(kid string) string {
	t, ok := parseKeyID(kid)
	if !ok {
		return newKeyID()
	}
	return t.Add(time.Second).Format(keyIDLayout)
}
