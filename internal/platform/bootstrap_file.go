// Bootstrap-token file management (slice 073).
//
// The platform writes the bootstrap admin bearer token to a file at
// ${ATLAS_DATA_DIR}/bootstrap-token (mode 0600) so a self-host operator
// has three orthogonal ways to find it: stderr of the atlas process,
// `docker compose logs atlas | grep BOOTSTRAP_TOKEN`, or filesystem
// inspection.
//
// The file's reason to exist is exactly "make the FIRST sign-in
// discoverable" and no longer. On the first successful sign-in atlas
// itself deletes the file atomically (rename to a tmp path, then unlink).
// This is the load-bearing P0-A1 safety property: long-lived bootstrap
// tokens on disk are a credential leak shape this slice does not
// introduce.

package platform

import (
	"errors"
	"fmt"
	"io/fs"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
)

// BootstrapTokenFile is the basename of the token file written under
// ATLAS_DATA_DIR. Exported so tests can compose the same path the
// runtime uses.
const BootstrapTokenFile = "bootstrap-token"

// BootstrapTokenPath returns the absolute path to the bootstrap-token
// file. dataDir is taken from the ATLAS_DATA_DIR environment variable;
// when that is empty, falls back to /var/lib/atlas (FHS-correct for a
// daemon's mutable state). The Helm chart and the docker-compose bundle
// both set ATLAS_DATA_DIR explicitly, so the fallback is for bare-binary
// deployments (developers running `./atlas` directly).
//
// Documented paths the troubleshooting page lists:
//   - docker-compose (slice 037): ATLAS_DATA_DIR=/var/lib/atlas inside
//     the container; bind-mounted to ./atlas-data on the host
//   - Helm (slice 038): ATLAS_DATA_DIR=/var/lib/atlas on the PVC
//   - bare binary: /var/lib/atlas (requires the operator to create it
//     with the right owner; the file write will fail loudly otherwise)
func BootstrapTokenPath(dataDir string) string {
	if dataDir == "" {
		dataDir = "/var/lib/atlas"
	}
	return filepath.Join(dataDir, BootstrapTokenFile)
}

// WriteBootstrapToken writes token to path with mode 0600. The parent
// directory must already exist (the docker-compose bundle and the Helm
// chart both create it; the bare-binary path requires the operator to
// pre-create /var/lib/atlas). Returns an error if the write fails;
// callers log it but do not abort startup — the file is a convenience,
// not a hard dependency (the operator can still find the token in
// stderr or via `docker compose logs`).
//
// Token is written without a trailing newline so `cat $FILE` returns
// exactly the token bytes — that lets operators do
// `TOKEN=$(cat /var/lib/atlas/bootstrap-token)` cleanly.
func WriteBootstrapToken(path, token string) error {
	if token == "" {
		return errors.New("platform: refuse to write empty bootstrap token")
	}
	// O_TRUNC because re-issuing via --reset-bootstrap overwrites the
	// file; O_CREATE because the first boot creates it.
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0o600)
	if err != nil {
		return fmt.Errorf("platform: open bootstrap-token file: %w", err)
	}
	if _, err := f.WriteString(token); err != nil {
		_ = f.Close()
		return fmt.Errorf("platform: write bootstrap-token file: %w", err)
	}
	if err := f.Close(); err != nil {
		return fmt.Errorf("platform: close bootstrap-token file: %w", err)
	}
	// Re-chmod in case umask interfered (defense in depth — OpenFile mode
	// is masked by the process umask on most systems).
	if err := os.Chmod(path, 0o600); err != nil {
		return fmt.Errorf("platform: chmod bootstrap-token file: %w", err)
	}
	return nil
}

// DeleteBootstrapToken atomically deletes the bootstrap-token file at
// path. Returns nil if the file does not exist (the operator may have
// already deleted it manually, or the bare-binary deployment never
// wrote one). Returns nil and logs nothing when the file is absent.
//
// "Atomic" here means rename-then-unlink: a concurrent reader either
// sees the original file or gets ENOENT, never an empty file. POSIX
// rename(2) on the same filesystem is atomic; unlink(2) on a renamed-
// away path is then safe to fail without re-exposing the original.
//
// Logs at INFO level when the file is consumed:
//
//	"bootstrap-token file consumed and deleted"
//
// The truncated token hash is NOT logged (P0-A2 — atlas's own logs
// never echo the token plaintext, not even hashed). The log line just
// records the event.
func DeleteBootstrapToken(path string, logger *slog.Logger) error {
	tmpPath := path + ".consuming"
	err := os.Rename(path, tmpPath)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			// File never existed (bare-binary deployment without
			// pre-created dir, or operator deleted it manually). No-op.
			return nil
		}
		return fmt.Errorf("platform: rename bootstrap-token: %w", err)
	}
	if err := os.Remove(tmpPath); err != nil {
		// The rename succeeded so the file at path is gone — we are
		// already in the "deleted" state from any reader's perspective.
		// Failing to unlink the tmp leaves debris but does not violate
		// the safety property. Log and move on.
		if logger != nil {
			logger.Warn("platform: unlink bootstrap-token tmp failed",
				slog.String("path", tmpPath), slog.String("error", err.Error()))
		}
		// Return nil — the load-bearing safety property (the original
		// path no longer exists) is satisfied.
	}
	if logger != nil {
		logger.Info("bootstrap-token file consumed and deleted",
			slog.String("path", redactPath(path)))
	}
	return nil
}

// redactPath returns a stable, log-safe form of a filesystem path —
// just the basename plus the parent's basename, never the full prefix.
// This keeps `/var/lib/atlas/bootstrap-token` out of logs verbatim
// (defense in depth; the file's location is documented anyway, so
// redaction is overcautious-by-design).
func redactPath(path string) string {
	parent := filepath.Base(filepath.Dir(path))
	base := filepath.Base(path)
	if parent == "" || parent == "." || parent == "/" {
		return base
	}
	return parent + string(os.PathSeparator) + base
}

// SanitizeTokenForLogStdout returns the token bytes prefixed by
// "ATLAS_BOOTSTRAP_TOKEN=" — the canonical grep-friendly stdout line
// shape AC-6 specifies. Centralised here so cmd/atlas and bootstrap.sh
// agree on the wire shape.
//
// The line is NOT for the structured logger (P0-A2 — atlas's slog
// output never contains the plaintext). It is emitted ONCE to os.Stderr
// at boot, then the platform's structured logger takes over.
func SanitizeTokenForLogStdout(token string) string {
	// Defense in depth: refuse a multi-line token so the grep-friendly
	// single line is guaranteed. Bootstrap tokens are opaque opaque
	// random strings — a newline indicates corruption.
	if strings.ContainsAny(token, "\r\n") {
		return "ATLAS_BOOTSTRAP_TOKEN=<refused: token contains newline>"
	}
	return "ATLAS_BOOTSTRAP_TOKEN=" + token
}
