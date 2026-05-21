package main

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestRunDeviceCodeLogin_HappyPath ends-to-end exercises the device
// flow against a fake issuer that responds:
//
//  1. POST /oauth/device_authorization -> 200 with device + user codes
//  2. POST /oauth/token (first call)    -> 400 authorization_pending
//  3. POST /oauth/token (second call)   -> 200 with access_token
//
// After success the credentials file at $HOME/.config/atlas/credentials.json
// is asserted to exist with mode 0600.
func TestRunDeviceCodeLogin_HappyPath(t *testing.T) {
	tmpHome := t.TempDir()
	t.Setenv("HOME", tmpHome)

	pollCount := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/oauth/device_authorization":
			_ = json.NewEncoder(w).Encode(map[string]any{
				"device_code":               "dev-code-xyz",
				"user_code":                 "ABCD-2345",
				"verification_uri":          "https://test/atlas/oauth/device",
				"verification_uri_complete": "https://test/atlas/oauth/device?user_code=ABCD-2345",
				"expires_in":                900,
				// Use 1 to keep the test fast; production uses 5.
				"interval": 1,
			})
		case "/oauth/token":
			pollCount++
			if pollCount == 1 {
				w.WriteHeader(http.StatusBadRequest)
				_ = json.NewEncoder(w).Encode(map[string]string{"error": "authorization_pending"})
				return
			}
			_ = json.NewEncoder(w).Encode(map[string]any{
				"access_token": "jwt.payload.sig",
				"token_type":   "Bearer",
				"expires_in":   3600,
			})
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(srv.Close)

	var buf bytes.Buffer
	if err := runDeviceCodeLogin(context.Background(), &buf, srv.URL, "client-id-1"); err != nil {
		t.Fatalf("runDeviceCodeLogin: %v", err)
	}

	out := buf.String()
	if !strings.Contains(out, "ABCD-2345") && !strings.Contains(out, "Visit") {
		t.Errorf("expected user-facing instructions in output, got %q", out)
	}
	if pollCount != 2 {
		t.Errorf("expected 2 token polls, got %d", pollCount)
	}

	credsPath := filepath.Join(tmpHome, ".config", "atlas", "credentials.json")
	info, err := os.Stat(credsPath)
	if err != nil {
		t.Fatalf("credentials file not written: %v", err)
	}
	// P0-191-5: mode 0600 — owner-read-write only.
	if mode := info.Mode().Perm(); mode != 0o600 {
		t.Errorf("credentials file mode = %o, want 0600", mode)
	}
	body, err := os.ReadFile(credsPath)
	if err != nil {
		t.Fatalf("read credentials: %v", err)
	}
	var got loginCredentials
	if err := json.Unmarshal(body, &got); err != nil {
		t.Fatalf("parse credentials: %v", err)
	}
	if got.AccessToken != "jwt.payload.sig" {
		t.Errorf("access_token = %q, want jwt.payload.sig", got.AccessToken)
	}
}

// TestRunDeviceCodeLogin_AccessDenied confirms the access_denied
// error path surfaces as a Go error (not a successful credentials
// write).
func TestRunDeviceCodeLogin_AccessDenied(t *testing.T) {
	tmpHome := t.TempDir()
	t.Setenv("HOME", tmpHome)

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/oauth/device_authorization":
			_ = json.NewEncoder(w).Encode(map[string]any{
				"device_code":      "dev-code",
				"user_code":        "ABCD-2345",
				"verification_uri": "https://test/atlas/oauth/device",
				"expires_in":       900,
				"interval":         1,
			})
		case "/oauth/token":
			w.WriteHeader(http.StatusBadRequest)
			_ = json.NewEncoder(w).Encode(map[string]string{"error": "access_denied"})
		}
	}))
	t.Cleanup(srv.Close)

	var buf bytes.Buffer
	err := runDeviceCodeLogin(context.Background(), &buf, srv.URL, "client-id-1")
	if err == nil {
		t.Fatal("expected access_denied error, got nil")
	}
	if !strings.Contains(err.Error(), "denied") {
		t.Errorf("error = %q, want to contain 'denied'", err)
	}
	if _, err := os.Stat(filepath.Join(tmpHome, ".config", "atlas", "credentials.json")); err == nil {
		t.Error("credentials file should NOT exist after access_denied")
	}
}
