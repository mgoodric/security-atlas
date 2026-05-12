// Package main is the security-atlas Okta connector binary. One
// subcommand per operation:
//
//	register  — announce this connector instance to the platform
//	run       — pull MFA policies, app assignments, and user lifecycle,
//	            push evidence records
//	scopes    — print the canonical least-privilege Okta admin scopes
//
// Constitutional invariants honored:
//   - Two SDK profiles (canvas §4.1): slice 045 ships the pull profile.
//     A push (event-hook) receiver is on the roadmap.
//   - No proprietary collector agents (anti-pattern): uses Okta's
//     standard REST surface only.
//
// Anti-criteria (slice 045 P0): least-privilege Okta admin scopes
// documented and enforced; API token never logged
// (oktaauth.Credential.String redacts); idempotency_key derived per
// emitter from sha256(prefix|id|hour); CLI help text never describes a
// flag-based path that would put the token in shell history.
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
