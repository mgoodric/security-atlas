package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestResolveBearer_FromFile verifies the --token-file path is honored.
func TestResolveBearer_FromFile(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	path := filepath.Join(dir, "token")
	if err := os.WriteFile(path, []byte("test-atlas-mcp-bearer\n"), 0o600); err != nil {
		t.Fatalf("write token file: %v", err)
	}
	got, err := resolveBearer(path)
	if err != nil {
		t.Fatalf("resolveBearer: %v", err)
	}
	if got != "test-atlas-mcp-bearer" {
		t.Errorf("token = %q, want %q (trimmed)", got, "test-atlas-mcp-bearer")
	}
}

// TestResolveBearer_FromEnv verifies env-var fallback. P0-A1 — bearer
// comes from env or file, never from a CLI flag value.
func TestResolveBearer_FromEnv(t *testing.T) {
	t.Setenv(envBearer, "test-env-token")
	got, err := resolveBearer("")
	if err != nil {
		t.Fatalf("resolveBearer: %v", err)
	}
	if got != "test-env-token" {
		t.Errorf("token = %q, want %q", got, "test-env-token")
	}
}

// TestResolveBearer_MissingErrors verifies that with neither env nor
// file, the resolver errors with an actionable message.
func TestResolveBearer_MissingErrors(t *testing.T) {
	t.Setenv(envBearer, "")
	_, err := resolveBearer("")
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	if !strings.Contains(err.Error(), envBearer) {
		t.Errorf("error %q must mention %s env var (P0-A1 doc surface)", err.Error(), envBearer)
	}
}

// TestResolveBearer_EmptyFile rejects an empty token file.
func TestResolveBearer_EmptyFile(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	path := filepath.Join(dir, "empty")
	if err := os.WriteFile(path, []byte("\n\n"), 0o600); err != nil {
		t.Fatalf("write: %v", err)
	}
	_, err := resolveBearer(path)
	if err == nil || !strings.Contains(err.Error(), "empty") {
		t.Errorf("expected empty-file error, got: %v", err)
	}
}

// TestResolveBearer_MissingFile errors with a clear "not found" message.
func TestResolveBearer_MissingFile(t *testing.T) {
	t.Parallel()

	_, err := resolveBearer("/nonexistent/path/to/token-file-that-does-not-exist")
	if err == nil || !strings.Contains(err.Error(), "not found") {
		t.Errorf("expected not-found error, got: %v", err)
	}
}

// TestVersionFlagPrints verifies the --version flag prints + exits 0.
// We exercise run() directly so we don't have to spawn a subprocess.
func TestVersionFlagPrints(t *testing.T) {
	t.Parallel()

	stdout, err := os.CreateTemp(t.TempDir(), "stdout")
	if err != nil {
		t.Fatalf("temp stdout: %v", err)
	}
	defer func() { _ = stdout.Close() }()
	stderr, err := os.CreateTemp(t.TempDir(), "stderr")
	if err != nil {
		t.Fatalf("temp stderr: %v", err)
	}
	defer func() { _ = stderr.Close() }()

	// stdin is nil-ish; not used on --version path.
	exit, runErr := run([]string{"--version"}, os.Stdin, stdout, stderr)
	if runErr != nil {
		t.Fatalf("run: %v", runErr)
	}
	if exit != 0 {
		t.Errorf("exit = %d, want 0", exit)
	}
	if err := stdout.Sync(); err != nil {
		t.Fatalf("sync: %v", err)
	}
	if _, err := stdout.Seek(0, 0); err != nil {
		t.Fatalf("seek: %v", err)
	}
	body, err := os.ReadFile(stdout.Name())
	if err != nil {
		t.Fatalf("read stdout: %v", err)
	}
	if !strings.Contains(string(body), "atlas-mcp") {
		t.Errorf("stdout missing atlas-mcp marker: %q", string(body))
	}
}
