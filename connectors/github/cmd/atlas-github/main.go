// Package main is the security-atlas GitHub connector binary. One
// subcommand per operation:
//
//	register   — announce this connector instance to the platform
//	run        — pull org repos + SCIM users, push evidence records
//	webhook    — start the HTTP receiver for github.audit_event.v1
//
// Constitutional invariants honored:
//   - Two SDK profiles (canvas §4.1): pull (run subcommand) + push
//     (webhook subcommand).
//   - No proprietary collector agents (anti-pattern): uses GitHub's
//     standard REST + webhook + SCIM v2 surfaces only.
//
// Anti-criteria (slice 044 P0): least-privilege PAT scopes documented and
// enforced; webhook signature verification mandatory; idempotency_key
// from X-GitHub-Delivery on every audit_event push; no secret material
// ever written to logs.
package main

import (
	"fmt"
	"os"
)

func main() {
	if err := newRootCmd().Execute(); err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		os.Exit(1)
	}
}
