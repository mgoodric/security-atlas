package main

import (
	"bytes"
	"strings"
	"testing"
)

// Slice 196 — bootstrap OAuth migration. The atlas-bootstrap container
// drives `atlas-cli controls upload --client-id ... --client-secret ...`
// against the OAuth client_credentials grant. These tests pin the
// flag-parsing surface + the OAuth-vs-token branch selection in
// resolveControlsAuth. The full upload path against a live HTTP
// endpoint is exercised by the slice-196 self-host bundle e2e
// (deploy/docker/test-self-host-bundle.sh).

// TestControlsUpload_OAuthFlagsParse covers the flag-declaration
// surface — the cobra command must accept --client-id +
// --client-secret + --issuer. Without these flags declared, the
// bootstrap.sh invocation fails with "unknown flag" before any code
// runs. RED→GREEN this test before adding the flags.
func TestControlsUpload_OAuthFlagsParse(t *testing.T) {
	resetControlsAuth(t)
	// Mount via the root command so persistent --endpoint resolves.
	cmd := newRootCmd()
	cmd.SetArgs([]string{
		"controls", "upload",
		"--client-id", "0e3b4a7e-1111-2222-3333-444455556666",
		"--client-secret", "test-secret",
		"--issuer", "http://atlas:8080",
		"--endpoint", "http://atlas:8080",
		"/nonexistent/bundle/path/does/not/exist",
	})
	cmd.SilenceUsage = true
	cmd.SilenceErrors = true
	cmd.SetOut(&bytes.Buffer{})
	cmd.SetErr(&bytes.Buffer{})
	// We expect the bundle-load step to fail (the path doesn't exist),
	// but the flag-parsing step must succeed first. If the flags are
	// undeclared, cobra fails with "unknown flag" which is the failure
	// we're guarding against here.
	err := cmd.Execute()
	if err == nil {
		t.Fatal("expected an error (bundle path is bogus), got nil")
	}
	if strings.Contains(err.Error(), "unknown flag") {
		t.Errorf("OAuth flags not declared: %v", err)
	}
}

// TestControlsUpload_OAuthFlagsResolve covers the
// resolveControlsAuth branch: when --client-id + --client-secret are
// both set, the resolver must select the OAuth path (set
// controlsAuth.useOAuth = true) and NOT require --token.
func TestControlsUpload_OAuthFlagsResolve(t *testing.T) {
	resetControlsAuth(t)
	t.Setenv("SECURITY_ATLAS_TOKEN", "")
	controlsAuth.clientID = "0e3b4a7e-1111-2222-3333-444455556666"
	controlsAuth.clientSecret = "test-secret"
	controlsAuth.issuer = "http://atlas:8080"
	common.endpoint = "http://atlas:8080"
	common.token = ""

	if err := resolveControlsAuth(); err != nil {
		t.Fatalf("resolveControlsAuth: %v", err)
	}
	if !controlsAuth.useOAuth {
		t.Errorf("controlsAuth.useOAuth = false; want true when client-id + client-secret are set")
	}
}

// TestControlsUpload_OAuthIssuerDefaultsToEndpoint pins the small
// convenience that the OAuth issuer URL defaults to --endpoint when
// --issuer is unset. The bootstrap container passes the same URL
// twice otherwise.
func TestControlsUpload_OAuthIssuerDefaultsToEndpoint(t *testing.T) {
	resetControlsAuth(t)
	t.Setenv("SECURITY_ATLAS_TOKEN", "")
	controlsAuth.clientID = "0e3b4a7e-1111-2222-3333-444455556666"
	controlsAuth.clientSecret = "test-secret"
	controlsAuth.issuer = ""
	common.endpoint = "http://atlas:8080"
	common.token = ""

	if err := resolveControlsAuth(); err != nil {
		t.Fatalf("resolveControlsAuth: %v", err)
	}
	if controlsAuth.issuer != "http://atlas:8080" {
		t.Errorf("controlsAuth.issuer = %q; want http://atlas:8080 (defaulted from --endpoint)",
			controlsAuth.issuer)
	}
}

// TestControlsUpload_LegacyTokenStillWorks pins the AC-4 transitional
// invariant: when --client-id is NOT set, the resolver falls back to
// the slice-037 legacy --token path so existing scripts keep working.
func TestControlsUpload_LegacyTokenStillWorks(t *testing.T) {
	resetControlsAuth(t)
	common.endpoint = "http://atlas:8080"
	common.token = "test-legacy-token"

	if err := resolveControlsAuth(); err != nil {
		t.Fatalf("resolveControlsAuth: %v", err)
	}
	if controlsAuth.useOAuth {
		t.Errorf("controlsAuth.useOAuth = true; want false when only --token is set (legacy path)")
	}
}

// TestControlsUpload_MissingAuthRejected pins the negative case:
// neither OAuth flags NOR --token set → resolver fails with a clear
// message. Important so the operator sees a useful error instead of
// a confusing 401 from the server.
func TestControlsUpload_MissingAuthRejected(t *testing.T) {
	resetControlsAuth(t)
	t.Setenv("SECURITY_ATLAS_TOKEN", "")
	common.endpoint = "http://atlas:8080"
	common.token = ""

	err := resolveControlsAuth()
	if err == nil {
		t.Fatal("expected error when neither --token nor --client-id+--client-secret is set")
	}
}

// TestControlsUpload_PartialOAuthFlagsRejected pins the "both or
// neither" invariant — --client-id alone, or --client-secret alone,
// is a misconfiguration the CLI should call out explicitly.
func TestControlsUpload_PartialOAuthFlagsRejected(t *testing.T) {
	resetControlsAuth(t)
	t.Setenv("SECURITY_ATLAS_TOKEN", "")
	common.endpoint = "http://atlas:8080"
	common.token = ""
	controlsAuth.clientID = "0e3b4a7e-1111-2222-3333-444455556666"
	// clientSecret intentionally left empty

	err := resolveControlsAuth()
	if err == nil {
		t.Fatal("expected error when only --client-id is set (missing --client-secret)")
	}
	if !strings.Contains(err.Error(), "client-secret") {
		t.Errorf("error = %q; want it to mention client-secret", err)
	}
}

// resetControlsAuth clears the package-global controlsAuth + common
// state between tests so each one starts from a known baseline.
// Without this, parallel tests would race on the globals.
func resetControlsAuth(t *testing.T) {
	t.Helper()
	t.Cleanup(func() {
		controlsAuth = controlsAuthState{}
		common.endpoint = ""
		common.token = ""
		common.insecure = false
	})
	controlsAuth = controlsAuthState{}
	common.endpoint = ""
	common.token = ""
	common.insecure = false
}
