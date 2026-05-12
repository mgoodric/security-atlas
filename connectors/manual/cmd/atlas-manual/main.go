// Package main is the security-atlas manual.upload connector binary —
// the universal escape hatch for evidence that doesn't fit a structured
// connector.
//
// Five subcommands:
//
//	register   — announce this connector instance to the platform
//	scopes     — print the per-mode auth posture
//	local      — parse a local CSV; emit one record per row
//	s3         — list an S3 prefix; emit one record per object
//	sftp       — pull an SFTP path; emit one record per file
//
// Constitutional invariants honored:
//   - Push profile (canvas §4.1) — every mode goes through pkg/sdk-go's
//     Push API and the slice 003 wire protocol.
//   - Manual evidence is first-class (canvas §4.5, invariant 9) — these
//     records carry the same provenance, lifecycle, and freshness fields
//     as automated connectors.
//   - No proprietary collector agents (anti-pattern) — every transport is
//     a universal protocol (local filesystem / S3 / SFTP).
//
// Anti-criteria (slice 049 P0): AWS credentials never logged; SSH key
// material never logged or echoed; CSV row/field caps prevent DoS; SFTP
// host-key verification mandatory (no InsecureIgnoreHostKey); per-mode
// idempotency key derivation matches slice 003 conventions.
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
