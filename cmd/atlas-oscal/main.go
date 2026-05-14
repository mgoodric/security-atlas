// Package main is the OSCAL bridge supervisor / health probe.
//
// The OSCAL serialization itself lives in the Python `oscal-bridge`
// service (wrapping IBM compliance-trestle) — see oscal-bridge/README.md.
// That service is started independently (`python -m
// atlas_oscal_bridge.server`) as a sidecar to the platform binary.
//
// This Go entrypoint is a thin operational tool: `atlas-oscal health`
// dials the bridge's gRPC port and runs a trivial round-trip-validate
// call to confirm the bridge is reachable and trestle is importable.
// Deployments use it as a docker/Kubernetes readiness probe for the
// bridge sidecar. Slice 030.
package main

import (
	"context"
	"fmt"
	"os"
	"time"

	"github.com/mgoodric/security-atlas/internal/oscal"
)

const binary = "atlas-oscal"

// defaultBridgeAddr mirrors the Python server's DEFAULT_ADDRESS.
const defaultBridgeAddr = "127.0.0.1:50070"

func main() {
	args := os.Args[1:]
	if len(args) == 0 || args[0] == "help" || args[0] == "-h" || args[0] == "--help" {
		usage()
		return
	}
	switch args[0] {
	case "health":
		addr := defaultBridgeAddr
		if v := os.Getenv("OSCAL_BRIDGE_ADDR"); v != "" {
			addr = v
		}
		if len(args) > 1 {
			addr = args[1]
		}
		if err := health(addr); err != nil {
			fmt.Fprintf(os.Stderr, "%s: bridge unhealthy at %s: %v\n", binary, addr, err)
			os.Exit(1)
		}
		fmt.Printf("%s: bridge healthy at %s\n", binary, addr)
	default:
		fmt.Fprintf(os.Stderr, "%s: unknown command %q\n", binary, args[0])
		usage()
		os.Exit(2)
	}
}

func usage() {
	fmt.Printf(`%s — OSCAL bridge supervisor / health probe

The OSCAL serialization service is Python (oscal-bridge/); start it with:
  python -m atlas_oscal_bridge.server --address %s

Commands:
  health [addr]   dial the bridge and run a round-trip-validate probe
                  (addr defaults to $OSCAL_BRIDGE_ADDR or %s)
  help            show this message
`, binary, defaultBridgeAddr, defaultBridgeAddr)
}

// health dials the bridge and runs a RoundTripValidate against an
// intentionally-malformed document. A reachable bridge with a working
// trestle import returns valid=false (the document IS invalid) with no
// transport error — that is the success condition. A transport error
// means the bridge is down or trestle failed to import.
func health(addr string) error {
	bridge, err := oscal.DialBridge(addr)
	if err != nil {
		return err
	}
	defer func() { _ = bridge.Close() }()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	// A garbage document: a healthy bridge answers valid=false cleanly.
	valid, _, err := bridge.RoundTripValidate(ctx, "system-security-plan", []byte("{not-oscal"))
	if err != nil {
		return err
	}
	if valid {
		return fmt.Errorf("bridge reported a garbage document as valid — trestle wiring is broken")
	}
	return nil
}
