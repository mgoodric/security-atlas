// Package main is the security-atlas Slack connector binary. One subcommand
// per operation:
//
//	register   — announce this connector instance to the platform
//	run        — read the Slack roster / admin audit-log / retention settings
//	             and push evidence records
//
// The connector is a separate process holding source-side Slack credentials
// (invariant #3): it reads from Slack with a least-privilege read-only OAuth
// token and emits to the platform exclusively via the single push API. It
// never reads message content (slice 443 threat-model I).
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
