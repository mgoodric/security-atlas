// Package main is the OSCAL bridge service entrypoint.
//
// The bridge wraps IBM compliance-trestle (Python) via gRPC for
// OSCAL JSON v1.1.x serialization. Real bridge lands when slice 030
// (OSCAL SSP+POA&M export) requires it. For slice 001 (skeleton),
// it prints a marker.
package main

import "fmt"

const binary = "atlas-oscal"

func main() {
	fmt.Printf("%s: OSCAL bridge — slice 001 skeleton (no gRPC server yet)\n", binary)
}
