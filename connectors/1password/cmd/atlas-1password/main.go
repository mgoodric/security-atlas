// Package main is the security-atlas 1Password connector binary. One
// subcommand per operation:
//
//	register   — announce this connector instance to the platform
//	run        — pull org-policy state and push an evidence record
//	scopes     — print the canonical least-privilege Service Account
//	             scopes this connector requires
//
// Constitutional invariants honored:
//   - Single SDK profile (canvas §4.2 marks 1Password as Query-only):
//     pull (run subcommand). No webhook surface exists on 1Password
//     Business for org-policy state.
//   - No proprietary collector agents (anti-pattern): uses the
//     standard 1Password public API with a Service Account bearer.
//
// Anti-criteria (slice 046 P0): least-privilege per-vault Service
// Account scopes documented and enforced; Service Account token never
// logged; idempotency_key derived per slice 044's
// `kind|<resource>|<hour>` convention; no mutation of 1Password data.
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
