package main

import "testing"

// TestRunCmd_NetpolDefaultIsRoutedByKind pins the NetworkPolicy record's
// default control id to none (#2924): k8s.networkpolicy_coverage.v1 is routed
// by evidence type, so a record on default flags reaches every control that
// queries the kind, network_segmentation_default_deny among them. Before, the
// default bound it to that one bundle (#2745).
func TestRunCmd_NetpolDefaultIsRoutedByKind(t *testing.T) {
	t.Parallel()
	fl := newRunCmd().Flags().Lookup("netpol-control")
	if fl == nil {
		t.Fatal("--netpol-control flag not registered")
	}
	if fl.DefValue != "" {
		t.Errorf("--netpol-control default = %q, want empty", fl.DefValue)
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
