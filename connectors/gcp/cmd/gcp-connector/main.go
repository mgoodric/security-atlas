// Package main is the security-atlas GCP connector binary. One subcommand
// per operation:
//
//	register   — announce this connector instance to the platform
//	run        — read the project IAM policy + service-account inventory and
//	             the Cloud Storage bucket configuration, and push evidence
//	             records
//
// The connector is a separate process holding source-side GCP credentials
// (invariant #3): it reads from GCP with a least-privilege read-only identity
// (Application Default Credentials or a service-account key) and emits to the
// platform exclusively via the single push API. It never reads stored object
// contents, service-account key material, or secret values (slice 442
// threat-model I).
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
