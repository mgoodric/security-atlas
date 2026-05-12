// Package main is the security-atlas Jira / Linear ticket connector
// binary. One binary covers both platforms (Atlassian's Jira Cloud
// and linear.app) because they emit the same evidence_kind —
// jira.ticket_evidence.v1 — and share the same canonical Ticket shape.
// The --platform flag on the run subcommand selects which API the
// connector pulls from.
//
// Subcommands:
//
//	register   — announce this connector instance to the platform
//	run        — pull Jira issues or Linear issues and push evidence
//	scopes     — print documented least-privilege scopes for both platforms
//
// Constitutional invariants honored:
//   - Two SDK profiles (canvas §4.1): pull is in scope this slice.
//     Webhook push is deferred to slice 049+ (Linear has webhooks; Jira
//     Cloud has webhooks; both add a stateful HTTP receiver that does
//     not fit the binary contract this slice ships).
//   - No proprietary collector agents (anti-pattern): uses Jira's
//     standard REST API v3 and Linear's standard GraphQL surface only.
//
// Anti-criteria (slice 048 P0):
//   - Least-privilege scopes documented + DocumentedScopes test
//     enforces; scope subcommand prints the canonical list.
//   - Tokens / API keys redacted via Credential.String; never appear
//     in process listings (env preferred), shell history, or logs.
//   - Every push carries an idempotency_key derived as
//     sha256("jira.ticket_evidence" + ticket_id + hour).
//   - Read-only: no transition / comment / update calls anywhere.
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
