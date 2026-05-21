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

// Rotate is the interface stub — see ADR-0003 § Key rotation strategy.
func (s *Store) Rotate(_ context.Context) error {
	return keystore.ErrRotateUnsupported
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

// newKeyID returns a deterministic-by-time KeyID that sorts in
// chronological order so the latest-generated key is also the
// alphabetically-last filename.
func newKeyID() string {
	// Format: yyyymmddThhmmssZ — RFC3339 minus separators. 16 ASCII
	// chars, lexicographically sortable, no slashes or spaces.
	return time.Now().UTC().Format("20060102T150405Z")
}
