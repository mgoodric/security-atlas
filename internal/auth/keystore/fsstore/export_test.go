package fsstore

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"path/filepath"
)

// WriteTestKeyFile generates a fresh ES256 keypair and writes it to
// <dir>/<kid>.key using the production write path. Exported to the
// external fsstore_test package via this in-package _test.go file so
// rotation/prune tests can plant keys with arbitrary KeyIDs (and thus
// arbitrary ages) without waiting wall-clock seconds between generations.
//
// Test-only: this symbol exists only in the test binary.
func WriteTestKeyFile(dir, kid string) error {
	priv, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return err
	}
	return writePrivateKey(filepath.Join(dir, kid+keyFileExt), priv)
}
