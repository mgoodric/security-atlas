package main

import "testing"

// TestRunCmd_NetpolDefaultIsSegmentation pins the NetworkPolicy record's
// default control id to SCF 2026.3 NET-06 Network Segmentation
// (macrosegmentation) (#2396).
func TestRunCmd_NetpolDefaultIsSegmentation(t *testing.T) {
	t.Parallel()
	fl := newRunCmd().Flags().Lookup("netpol-control")
	if fl == nil {
		t.Fatal("--netpol-control flag not registered")
	}
	if fl.DefValue != "scf:NET-06" {
		t.Errorf("--netpol-control default = %q, want %q", fl.DefValue, "scf:NET-06")
	}
}
