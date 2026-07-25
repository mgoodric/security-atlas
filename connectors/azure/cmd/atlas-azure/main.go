// Package main is the security-atlas Azure connector binary. One subcommand per
// operation:
//
//	register   — announce this connector instance to the platform
//	run        — read Entra ID + Azure Storage, push evidence records
//	permissions — print the least-privilege Azure permissions this connector needs
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
