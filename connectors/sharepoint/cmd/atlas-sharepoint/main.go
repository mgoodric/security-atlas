// Package main is the security-atlas SharePoint policy connector (#2837),
// the SharePoint counterpart of the Confluence policy connector (#2856),
// on the same policy-sync contract (spec #2813).
//
// One subcommand, run: read every document in one SharePoint Online
// document library through Microsoft Graph, convert each .docx and its
// library columns, push the full set to POST /v1/policies:sync with a
// policy-sync API key, and print the per-document report. The operator
// schedules it (daily is recommended); there is no platform-side
// scheduler.
//
// Invariants:
//   - The platform stays push-only (invariant 3): the Entra app's client
//     secret lives here, and security-atlas never calls Microsoft Graph.
//   - Read-only against SharePoint: the token request aside, GET requests
//     only.
//   - All or nothing: a run that cannot read the full library pushes
//     nothing and exits non-zero, so an outage never reads as documents
//     going missing.
package main

import (
	"fmt"
	"os"
)

func main() {
	if err := newRootCmd(os.Stdout).Execute(); err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		os.Exit(exitCode(err))
	}
}
