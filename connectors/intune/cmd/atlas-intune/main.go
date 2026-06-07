// Package main is the security-atlas Microsoft Intune MDM connector binary. One
// subcommand per operation:
//
//	register    — announce this connector instance to the platform
//	run         — read Intune managed-device compliance posture, push evidence records
//	permissions — print the least-privilege read-only Graph permission this connector needs
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
