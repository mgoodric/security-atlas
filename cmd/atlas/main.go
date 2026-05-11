// Package main is the security-atlas platform server entrypoint.
//
// This binary becomes the main HTTP/gRPC server as subsequent slices
// (013 push API, 008 UCF traversal, etc.) add packages under internal/.
// For slice 001 (skeleton), it prints a marker so the build succeeds.
package main

import "fmt"

const binary = "atlas"

func main() {
	fmt.Printf("%s: platform binary — slice 001 skeleton (no service yet)\n", binary)
}
