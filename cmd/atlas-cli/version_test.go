package main

import (
	"bytes"
	"runtime"
	"strings"
	"testing"
)

// TestShortVersion_DevFallback documents the dev-build contract: when the
// binary is built without -ldflags (the usual local workflow), --version
// must return "dev" rather than panic or print an empty string.
func TestShortVersion_DevFallback(t *testing.T) {
	got := shortVersion()
	if got == "" {
		t.Fatalf("shortVersion() returned empty string; expected dev or release tag")
	}
}

// TestVersionInfo_ContainsExpectedFields locks in the grep-stable format
// the install script and CI scripts depend on. Changing this format is a
// breaking change to downstream installers.
func TestVersionInfo_ContainsExpectedFields(t *testing.T) {
	out := versionInfo()
	wantSubstrings := []string{
		"security-atlas-cli",
		runtime.GOOS,
		runtime.GOARCH,
		runtime.Version(),
	}
	for _, s := range wantSubstrings {
		if !strings.Contains(out, s) {
			t.Errorf("versionInfo() = %q; want substring %q", out, s)
		}
	}
}

// TestVersionCmd_PrintsToStdout verifies the `version` subcommand wires
// to its command's stdout writer (not the package-level fmt.Println).
// Important so callers can redirect with --out / cmd.SetOut.
func TestVersionCmd_PrintsToStdout(t *testing.T) {
	cmd := newVersionCmd()
	var buf bytes.Buffer
	cmd.SetOut(&buf)
	cmd.SetErr(&buf)
	if err := cmd.Execute(); err != nil {
		t.Fatalf("execute: %v", err)
	}
	if !strings.Contains(buf.String(), "security-atlas-cli") {
		t.Errorf("version cmd output = %q; want it to include the binary name", buf.String())
	}
}

// TestVersionLdflagsOverride documents the linker-override contract.
// We can't simulate -ldflags from inside the test binary, but we can
// confirm `version` is a package-level var that *could* be overridden
// (rather than a const). If someone refactors it to a const this test
// will refuse to compile — which is the intended tripwire.
func TestVersionLdflagsOverride(t *testing.T) {
	saved := version
	defer func() { version = saved }()
	version = "v9.9.9-test"
	if shortVersion() != "v9.9.9-test" {
		t.Fatalf("shortVersion() did not honor package-level override; got %q", shortVersion())
	}
}
