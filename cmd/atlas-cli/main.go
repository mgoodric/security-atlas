// Package main is the security-atlas-cli binary entrypoint.
//
// The CLI grows in slice 003 (Evidence SDK proto + Go push client + CLI)
// with subcommands like `security-atlas evidence push` and
// `security-atlas credentials issue/rotate/revoke/list` (per D4 review
// resolution). For slice 001 (skeleton), it prints a marker.
package main

import "fmt"

const binary = "atlas-cli"

func main() {
	fmt.Printf("%s: CLI binary — slice 001 skeleton (no subcommands yet)\n", binary)
}
