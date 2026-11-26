package main

import (
	"testing"

	"github.com/spf13/cobra"
)

// TestNetworkControlDefaultsStayOnBoundaryProtection pins the NSG and
// firewall records' default control id to SCF 2026.3 NET-03 Boundary
// Protection on both commands (#2396, Matt's call 2026-11-18). The NSG
// kind's default anchors add NET-06; the flag default stays NET-03.
func TestNetworkControlDefaultsStayOnBoundaryProtection(t *testing.T) {
	t.Parallel()
	for name, cmd := range map[string]*cobra.Command{"run": newRunCmd(), "eventgrid": newEventGridCmd()} {
		for _, flag := range []string{"nsg-control", "firewall-control"} {
			fl := cmd.Flags().Lookup(flag)
			if fl == nil {
				t.Errorf("%s: --%s flag not registered", name, flag)
				continue
			}
			if fl.DefValue != "scf:NET-03" {
				t.Errorf("%s: --%s default = %q, want %q", name, flag, fl.DefValue, "scf:NET-03")
			}
		}
	}
}
