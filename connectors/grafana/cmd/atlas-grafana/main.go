// Package main is the security-atlas Grafana connector binary. One subcommand
// per operation:
//
//	register    — announce this connector instance to the platform
//	run         — read Grafana alert-rule + contact-point inventory, push evidence records
//	permissions — print the least-privilege read-only Grafana role this connector needs
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
