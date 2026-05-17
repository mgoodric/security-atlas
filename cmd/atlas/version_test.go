package main

import (
	"runtime"
	"strings"
	"testing"
)

func TestVersionInfo_ContainsExpectedFields(t *testing.T) {
	out := versionInfo()
	wantSubstrings := []string{
		"security-atlas",
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

func TestVersionLdflagsOverride(t *testing.T) {
	saved := version
	defer func() { version = saved }()
	version = "v9.9.9-test"
	if !strings.Contains(versionInfo(), "v9.9.9-test") {
		t.Fatalf("versionInfo() did not honor package-level override; got %q", versionInfo())
	}
}

// TestVersionFields_FourFields locks in the structured contract used by
// GET /v1/version (slice 072). All four fields must populate; reordering
// or omitting any breaks the BFF proxy + VersionFooter.
func TestVersionFields_FourFields(t *testing.T) {
	savedV, savedC, savedD := version, commit, date
	defer func() {
		version, commit, date = savedV, savedC, savedD
	}()
	version, commit, date = "v9.9.9-test", "deadbeef", "2026-05-15T15:00:00Z"

	got := versionFields()
	if got.Version != "v9.9.9-test" {
		t.Errorf("Version = %q; want v9.9.9-test", got.Version)
	}
	if got.Commit != "deadbeef" {
		t.Errorf("Commit = %q; want deadbeef", got.Commit)
	}
	if got.BuildTime != "2026-05-15T15:00:00Z" {
		t.Errorf("BuildTime = %q; want 2026-05-15T15:00:00Z", got.BuildTime)
	}
	if got.GoVersion == "" {
		t.Errorf("GoVersion is empty; runtime.Version() should always populate")
	}
	if !strings.HasPrefix(got.GoVersion, "go") {
		t.Errorf("GoVersion = %q; want go-prefixed (runtime.Version output)", got.GoVersion)
	}
}
