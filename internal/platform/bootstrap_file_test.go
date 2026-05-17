// Unit tests for the slice-073 bootstrap-token file management helpers.

package platform_test

import (
	"bytes"
	"errors"
	"io/fs"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mgoodric/security-atlas/internal/platform"
)

func TestBootstrapTokenPath_Default(t *testing.T) {
	got := platform.BootstrapTokenPath("")
	want := "/var/lib/atlas/bootstrap-token"
	if got != want {
		t.Fatalf("BootstrapTokenPath('') = %q; want %q", got, want)
	}
}

func TestBootstrapTokenPath_FromEnvDir(t *testing.T) {
	dir := t.TempDir()
	got := platform.BootstrapTokenPath(dir)
	want := filepath.Join(dir, "bootstrap-token")
	if got != want {
		t.Fatalf("BootstrapTokenPath(%q) = %q; want %q", dir, got, want)
	}
}

func TestWriteBootstrapToken_CreatesFile0600(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "bootstrap-token")
	if err := platform.WriteBootstrapToken(path, "test-bootstrap-token-value"); err != nil {
		t.Fatalf("WriteBootstrapToken: %v", err)
	}

	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("stat: %v", err)
	}
	mode := info.Mode().Perm()
	if mode != 0o600 {
		t.Fatalf("mode = %o; want 0600", mode)
	}

	data, err := os.ReadFile(path) //nolint:gosec // test fixture path
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}
	if string(data) != "test-bootstrap-token-value" {
		t.Fatalf("file body = %q; want exact token bytes without trailing newline", data)
	}
}

func TestWriteBootstrapToken_Overwrites(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "bootstrap-token")
	if err := platform.WriteBootstrapToken(path, "old"); err != nil {
		t.Fatalf("write old: %v", err)
	}
	if err := platform.WriteBootstrapToken(path, "new"); err != nil {
		t.Fatalf("write new: %v", err)
	}
	data, err := os.ReadFile(path) //nolint:gosec // test fixture path
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}
	if string(data) != "new" {
		t.Fatalf("body = %q; want 'new' (overwrite)", data)
	}
}

func TestWriteBootstrapToken_RefuseEmpty(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "bootstrap-token")
	err := platform.WriteBootstrapToken(path, "")
	if err == nil {
		t.Fatalf("WriteBootstrapToken(\"\") succeeded; want error")
	}
	if _, statErr := os.Stat(path); !errors.Is(statErr, fs.ErrNotExist) {
		t.Fatalf("file exists after empty-token refusal; should not be created")
	}
}

func TestDeleteBootstrapToken_RemovesFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "bootstrap-token")
	// Distinctive token plaintext that does not appear as a substring of
	// the basename or the redacted path the logger emits.
	tokenPlaintext := "ZZZ-DISTINCTIVE-PLAINTEXT-XYZ"
	if err := platform.WriteBootstrapToken(path, tokenPlaintext); err != nil {
		t.Fatalf("WriteBootstrapToken: %v", err)
	}

	var logBuf bytes.Buffer
	logger := slog.New(slog.NewTextHandler(&logBuf, nil))
	if err := platform.DeleteBootstrapToken(path, logger); err != nil {
		t.Fatalf("DeleteBootstrapToken: %v", err)
	}
	if _, err := os.Stat(path); !errors.Is(err, fs.ErrNotExist) {
		t.Fatalf("file still exists after delete")
	}
	// No tmp file lingers either.
	if _, err := os.Stat(path + ".consuming"); !errors.Is(err, fs.ErrNotExist) {
		t.Fatalf("tmp file lingers after delete")
	}
	logged := logBuf.String()
	if !strings.Contains(logged, "consumed and deleted") {
		t.Fatalf("log missing consumed line: %q", logged)
	}
	// P0-A2: the token plaintext must NEVER appear in the log.
	if strings.Contains(logged, tokenPlaintext) {
		t.Fatalf("log contains token plaintext: %q", logged)
	}
}

func TestDeleteBootstrapToken_AbsentIsNoOp(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "bootstrap-token")
	// Don't create the file.
	logger := slog.New(slog.NewTextHandler(&bytes.Buffer{}, nil))
	if err := platform.DeleteBootstrapToken(path, logger); err != nil {
		t.Fatalf("DeleteBootstrapToken on absent file: %v; want nil (no-op)", err)
	}
}

func TestSanitizeTokenForLogStdout_GrepFriendly(t *testing.T) {
	got := platform.SanitizeTokenForLogStdout("abc-123-token")
	want := "ATLAS_BOOTSTRAP_TOKEN=abc-123-token"
	if got != want {
		t.Fatalf("got %q; want %q", got, want)
	}
}

func TestSanitizeTokenForLogStdout_RefusesNewline(t *testing.T) {
	got := platform.SanitizeTokenForLogStdout("ab\ncd")
	if strings.Contains(got, "ab") {
		t.Fatalf("got %q; expected refusal that hides the token plaintext", got)
	}
	if !strings.Contains(got, "refused") {
		t.Fatalf("got %q; want a 'refused' marker", got)
	}
}
