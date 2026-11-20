package main

import "testing"

// TestRunCmd_NetpolDefaultIsStockSegmentationControl pins the NetworkPolicy
// record's default control id to the stock network_segmentation_default_deny
// bundle (#2745), anchored on SCF 2026.3 NET-06 Network Segmentation (#2648).
// Ingest resolves a bundle id to the tenant's active control (#2100), so
// records on default flags link to it; the free-form scf:NET-06 linked to
// nothing and left the control inconclusive.
func TestRunCmd_NetpolDefaultIsStockSegmentationControl(t *testing.T) {
	t.Parallel()
	const want = "network_segmentation_default_deny"
	fl := newRunCmd().Flags().Lookup("netpol-control")
	if fl == nil {
		t.Fatal("--netpol-control flag not registered")
	}
	if fl.DefValue != want {
		t.Errorf("--netpol-control default = %q, want %q", fl.DefValue, want)
	}
}

// TestRunCmd_NetpolControlOverride keeps the flag as an override.
func TestRunCmd_NetpolControlOverride(t *testing.T) {
	t.Parallel()
	cmd := newRunCmd()
	if err := cmd.Flags().Parse([]string{"--netpol-control", "scf:NET-06"}); err != nil {
		t.Fatalf("parse: %v", err)
	}
	got, err := cmd.Flags().GetString("netpol-control")
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if got != "scf:NET-06" {
		t.Errorf("--netpol-control = %q, want scf:NET-06", got)
	}
}
