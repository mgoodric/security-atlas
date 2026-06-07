// Package main is the security-atlas BambooHR HRIS connector binary. One
// subcommand per operation:
//
//	register    — announce this connector instance to the platform
//	run         — read BambooHR worker-lifecycle records, push evidence records
//	permissions — print the least-privilege read-only BambooHR API scope this connector needs
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
