// Package main is the security-atlas AWS connector binary. One subcommand
// per operation:
//
//	register   — announce this connector instance to the platform
//	run        — assume a role, query AWS, push evidence records
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
