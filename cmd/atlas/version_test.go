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
