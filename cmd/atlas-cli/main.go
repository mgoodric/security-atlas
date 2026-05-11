// Package main is the security-atlas-cli binary entrypoint. Slice 003
// implements `evidence push` and `credentials {issue,rotate,revoke,list}`.
package main

import (
	"fmt"
	"os"
)

func main() {
	root := newRootCmd()
	if err := root.Execute(); err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		os.Exit(1)
	}
}
