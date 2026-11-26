// Package bridgetest starts the Python oscal-bridge subprocess for the Go
// integration tests that drive it (internal/oscal and its catalogimport,
// componentimport and profileimport children).
//
// The bridge is optional on a developer machine: when Python or
// compliance-trestle is missing, Start skips the calling test (the
// slice-030 D2 pattern). CI sets ATLAS_REQUIRE_OSCAL_BRIDGE=1 on the legs
// that install the bridge, and there an unavailable bridge fails the test
// instead, so a broken bridge environment cannot pass as a green skip
// (issue #1875).
//
// Env:
//
//	OSCAL_BRIDGE_PYTHON        - interpreter with compliance-trestle + grpcio
//	                             (defaults to "python3").
//	ATLAS_REQUIRE_OSCAL_BRIDGE - "1" turns every skip into a failure.
package bridgetest

import (
	"bytes"
	"fmt"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"sync"
	"testing"
	"time"
)

// RequireEnv is the env var that makes an unavailable bridge fatal.
const RequireEnv = "ATLAS_REQUIRE_OSCAL_BRIDGE"

// readyTimeout bounds how long Start waits for the gRPC port to accept.
const readyTimeout = 20 * time.Second

// lockedBuffer collects the bridge's combined output for failure messages.
type lockedBuffer struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (b *lockedBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.Write(p)
}

func (b *lockedBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.String()
}

// unavailable skips the test, or fails it when RequireEnv is "1".
func unavailable(t testing.TB, format string, args ...any) {
	t.Helper()
	msg := fmt.Sprintf(format, args...)
	if os.Getenv(RequireEnv) == "1" {
		t.Fatalf("%s (%s=1, so the bridge is required)", msg, RequireEnv)
	}
	t.Skipf("%s — skipping bridge-dependent test", msg)
}

// bridgeDir returns <repo>/oscal-bridge, located from this source file so
// callers at any package depth resolve the same directory.
func bridgeDir(t testing.TB) string {
	t.Helper()
	_, file, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatalf("bridgetest: cannot locate source file")
	}
	// file = <repo>/internal/oscal/bridgetest/bridgetest.go
	return filepath.Join(filepath.Dir(file), "..", "..", "..", "oscal-bridge")
}

// Start launches the bridge on a free loopback port and returns its address
// and a stop func. It skips (or, with RequireEnv=1, fails) the test when the
// bridge cannot start.
func Start(t testing.TB) (addr string, stop func()) {
	t.Helper()
	py := os.Getenv("OSCAL_BRIDGE_PYTHON")
	if py == "" {
		py = "python3"
	}
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("pick port: %v", err)
	}
	port := ln.Addr().(*net.TCPAddr).Port
	_ = ln.Close()
	addr = fmt.Sprintf("127.0.0.1:%d", port)

	dir := bridgeDir(t)
	out := &lockedBuffer{}
	cmd := exec.Command(py, "-m", "atlas_oscal_bridge.server", "--address", addr)
	cmd.Dir = dir
	cmd.Env = append(os.Environ(), "PYTHONPATH="+dir)
	cmd.Stdout = out
	cmd.Stderr = out
	if err := cmd.Start(); err != nil {
		unavailable(t, "oscal-bridge could not start (%s): %v", py, err)
		return "", func() {}
	}
	exited := make(chan struct{})
	go func() { _ = cmd.Wait(); close(exited) }()
	stop = func() {
		_ = cmd.Process.Kill()
		<-exited
	}

	deadline := time.Now().Add(readyTimeout)
	for time.Now().Before(deadline) {
		select {
		case <-exited:
			unavailable(t, "oscal-bridge exited during startup (%s): %s", py, out.String())
			return "", func() {}
		default:
		}
		conn, derr := net.DialTimeout("tcp", addr, 250*time.Millisecond)
		if derr == nil {
			_ = conn.Close()
			return addr, stop
		}
		time.Sleep(200 * time.Millisecond)
	}
	stop()
	unavailable(t, "oscal-bridge did not become ready on %s: %s", addr, out.String())
	return "", func() {}
}
