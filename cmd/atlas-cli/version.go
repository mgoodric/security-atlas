package main

import (
	"fmt"
	"runtime"

	"github.com/spf13/cobra"
)

// Build-time variables populated via -ldflags by GoReleaser at release time.
// When the binary is built from a working tree without ldflags (e.g. `go run`,
// `go build` for local dev, or `go test`), these stay at their zero-value
// placeholders. The CLI's --version output always renders a deterministic
// string regardless.
//
// Linker flag pattern:
//
//	-X main.version=v0.1.0 -X main.commit=<sha> -X main.date=<iso8601>
//
// Keep these as package-level vars (not consts) so the linker can override
// them. Do not assign at init() — that would defeat the override.
var (
	version = "dev"
	commit  = "none"
	date    = "unknown"
)

// versionInfo returns the human-readable version string printed by
// `security-atlas-cli --version` and `security-atlas-cli version`.
// Format is stable enough to grep in CI; reordering fields is a breaking
// change for downstream installers.
func versionInfo() string {
	return fmt.Sprintf(
		"security-atlas-cli %s (commit %s, built %s, %s/%s, %s)",
		version, commit, date, runtime.GOOS, runtime.GOARCH, runtime.Version(),
	)
}

// shortVersion returns the bare tag (or "dev"). Used by the --version flag
// short form; full info is available via the `version` subcommand.
func shortVersion() string {
	return version
}

// newVersionCmd is the `version` subcommand. Verbose by default; pair with
// the persistent `--version` flag for the short form that scripts expect.
func newVersionCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "version",
		Short: "Print the security-atlas-cli version, commit, and build metadata",
		RunE: func(cmd *cobra.Command, args []string) error {
			_, err := fmt.Fprintln(cmd.OutOrStdout(), versionInfo())
			return err
		},
	}
}
