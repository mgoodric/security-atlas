// Package manualsftp builds the SSH client config and host-key callback the
// manual.upload connector uses for SFTP pulls.
//
// Anti-criterion P0: InsecureIgnoreHostKey is REJECTED at config-build
// time. The caller MUST supply a known_hosts-backed callback. The
// connector refuses to connect without one.
//
// Anti-criterion P0: SSH private key material is read from a file path,
// never from a flag value. The cmd layer never accepts --key-bytes — only
// --key-file. The returned PEM bytes are never logged.
package manualsftp

import (
	"errors"
	"fmt"
	"os"
	"reflect"
	"runtime"
	"strings"

	"golang.org/x/crypto/ssh"
	"golang.org/x/crypto/ssh/knownhosts"
)

// Errors returned by config builders.
var (
	ErrKnownHostsRequired = errors.New("manualsftp: --known-hosts path is required")
	ErrInsecureCallback   = errors.New("manualsftp: ssh.InsecureIgnoreHostKey is forbidden — pass a known_hosts file")
)

// BuildOpts configures the SSH client. The HostKeyCallback is mandatory
// and must not be ssh.InsecureIgnoreHostKey.
type BuildOpts struct {
	User            string
	PrivateKeyPEM   []byte
	HostKeyCallback ssh.HostKeyCallback
}

// NewHostKeyCallback loads a known_hosts file and returns a callback that
// verifies remote host keys against it. Empty path returns
// ErrKnownHostsRequired — the binary refuses to start with no callback.
func NewHostKeyCallback(knownHostsPath string) (ssh.HostKeyCallback, error) {
	if knownHostsPath == "" {
		return nil, ErrKnownHostsRequired
	}
	cb, err := knownhosts.New(knownHostsPath)
	if err != nil {
		return nil, fmt.Errorf("manualsftp: load known_hosts: %w", err)
	}
	return cb, nil
}

// BuildSSHConfig assembles the *ssh.ClientConfig. Validates non-empty
// user, non-empty private key, and a non-insecure HostKeyCallback.
func BuildSSHConfig(opts BuildOpts) (*ssh.ClientConfig, error) {
	if opts.User == "" {
		return nil, errors.New("manualsftp: user is required")
	}
	if len(opts.PrivateKeyPEM) == 0 {
		return nil, errors.New("manualsftp: private key bytes are required (load via --key-file)")
	}
	if opts.HostKeyCallback == nil {
		return nil, errors.New("manualsftp: HostKeyCallback is required")
	}
	if isInsecureIgnoreHostKey(opts.HostKeyCallback) {
		return nil, ErrInsecureCallback
	}
	signer, err := ssh.ParsePrivateKey(opts.PrivateKeyPEM)
	if err != nil {
		// Wrap without exposing key bytes.
		return nil, errors.New("manualsftp: parse private key: invalid PEM or unsupported format")
	}
	return &ssh.ClientConfig{
		User:            opts.User,
		Auth:            []ssh.AuthMethod{ssh.PublicKeys(signer)},
		HostKeyCallback: opts.HostKeyCallback,
	}, nil
}

// LoadPrivateKey reads a private key from disk. Returns an error that does
// NOT include the file contents — the caller can safely log err.Error().
func LoadPrivateKey(path string) ([]byte, error) {
	if path == "" {
		return nil, errors.New("manualsftp: key path is required")
	}
	b, err := os.ReadFile(path)
	if err != nil {
		// Strip the file content from the error chain — os.ReadFile errors
		// already do this, but be explicit.
		return nil, fmt.Errorf("manualsftp: read key file %q: %w", path, err)
	}
	return b, nil
}

// isInsecureIgnoreHostKey detects ssh.InsecureIgnoreHostKey by inspecting
// the underlying closure's function name. The runtime names the closure
// returned by ssh.InsecureIgnoreHostKey with a marker substring; we
// substring-match on it.
//
// Caveat: this catches the canonical anti-pattern. An operator who writes
// their own always-true callback isn't caught at runtime — that's a
// code-review concern, not a runtime guard.
func isInsecureIgnoreHostKey(cb ssh.HostKeyCallback) bool {
	if cb == nil {
		return false
	}
	fn := runtime.FuncForPC(reflect.ValueOf(cb).Pointer())
	if fn == nil {
		return false
	}
	return strings.Contains(fn.Name(), "InsecureIgnoreHostKey")
}
