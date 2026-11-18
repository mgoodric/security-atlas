package main

import (
	"bytes"
	"encoding/base64"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mgoodric/security-atlas/internal/auth/idpconfig"
	"github.com/mgoodric/security-atlas/internal/netguard"
)

// TestOIDCResolverFromEnv: a set but malformed client-secret key is a boot
// error in every mode, local mode included (#2609); an unset key is not.
func TestOIDCResolverFromEnv(t *testing.T) {
	good := base64.StdEncoding.EncodeToString(bytes.Repeat([]byte{0x42}, 32))
	short := base64.StdEncoding.EncodeToString([]byte("too-short"))
	cases := []struct {
		name      string
		localMode string
		key       string
		keyFile   string // contents of a _FILE key; "" means unset
		wantErr   bool
		wantLocal bool
		wantLabel string
	}{
		{name: "local, key unset", localMode: "true", wantLocal: true},
		{name: "local, key valid", localMode: "true", key: good, wantLocal: true},
		{name: "local, key not base64", localMode: "true", key: "%%%", wantErr: true},
		{name: "local, key wrong length", localMode: "true", key: short, wantErr: true},
		{name: "local, key file wrong length", localMode: "true", keyFile: short, wantErr: true},
		{name: "db, key unset", wantLabel: "secret_key_configured=false"},
		{name: "db, key valid", key: good, wantLabel: "secret_key_configured=true"},
		{name: "db, key wrong length", key: short, wantErr: true},
		{name: "bad local-mode value", localMode: "yes", key: good, wantErr: true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			t.Setenv(idpconfig.LocalModeEnv, c.localMode)
			t.Setenv(idpconfig.SecretKeyEnv, c.key)
			t.Setenv(idpconfig.SecretKeyFileEnv, "")
			if c.keyFile != "" {
				path := filepath.Join(t.TempDir(), "oidc-key")
				if err := os.WriteFile(path, []byte(c.keyFile), 0o600); err != nil {
					t.Fatal(err)
				}
				t.Setenv(idpconfig.SecretKeyFileEnv, path)
			}

			resolver, label, err := oidcResolverFromEnv(nil, netguard.Policy{})
			if c.wantErr {
				if err == nil {
					t.Fatalf("got resolver %T (%s), want a boot error", resolver, label)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			_, isLocal := resolver.(idpconfig.LocalModeResolver)
			if isLocal != c.wantLocal {
				t.Errorf("resolver = %T, want local mode %t", resolver, c.wantLocal)
			}
			if c.wantLabel != "" && !strings.Contains(label, c.wantLabel) {
				t.Errorf("label = %q, want it to contain %q", label, c.wantLabel)
			}
		})
	}
}
