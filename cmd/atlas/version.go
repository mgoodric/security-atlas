package main

import (
	"fmt"
	"runtime"

	"github.com/mgoodric/security-atlas/internal/api"
)

// Build-time variables populated via -ldflags by GoReleaser at release time.
// Mirror the contract in cmd/atlas-cli/version.go.
//
// Slice 072 — these same vars now back GET /v1/version via versionFields()
// below. Single source of truth for both the human-readable banner
// (versionInfo) and the structured JSON endpoint. No `internal/version`
// package: that would force a goreleaser ldflag rewrite
// (`-X main.version` → `-X internal/version.Version`), a Dockerfile
// rewrite, and break grep-stable downstream installer tests. Extending
// the existing surface preserves the contract.
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

// versionFields returns the structured form of the same metadata
// versionInfo prints. Used by GET /v1/version (slice 072). Note that
// `GoVersion` is read from runtime.Version() — not from a fourth ldflag
// — because the runtime Go version is always available at exec time and
// adding a fourth `-X` would just duplicate what runtime already knows.
func versionFields() api.VersionFields {
	return api.VersionFields{
		Version:   version,
		Commit:    commit,
		BuildTime: date,
		GoVersion: runtime.Version(),
	}
}
