// Package main is the security-atlas Confluence policy connector (#2856,
// spec #2813).
//
// One subcommand, run: read every page labelled --label in --space from
// a Confluence Cloud site, convert each page's Page Properties table and
// body, push the full set to POST /v1/policies:sync with a policy-sync
// API key, and print the per-page report. The operator schedules it
// (daily is recommended); there is no platform-side scheduler.
//
// Invariants:
//   - The platform stays push-only (invariant 3): the Confluence
//     credential lives here, and security-atlas never calls Confluence.
//   - Read-only against Confluence: GET requests only.
//   - All or nothing: a run that cannot read the full set pushes nothing
//     and exits non-zero, so an outage never reads as pages going
//     missing.
package main

import (
	"errors"
	"fmt"
	"os"
)

func main() {
	if err := newRootCmd(os.Stdout).Execute(); err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		var ee *exitError
		if errors.As(err, &ee) {
			os.Exit(ee.code)
		}
		os.Exit(1)
	}
}
