package main

import (
	"fmt"
	"runtime"
)

// Build-time variables populated via -ldflags by GoReleaser at release time.
// Mirror the contract in cmd/atlas-cli/version.go.
var (
	version = "dev"
	commit  = "none"
	date    = "unknown"
)

// versionInfo renders the verbose --version output for the server binary.
// Format is grep-stable for downstream installers.
func versionInfo() string {
	return fmt.Sprintf(
		"security-atlas %s (commit %s, built %s, %s/%s, %s)",
		version, commit, date, runtime.GOOS, runtime.GOARCH, runtime.Version(),
	)
}
