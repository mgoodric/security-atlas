package main

import "testing"

// TestHostPostureControlDefaultsToNoControl pins --host-posture-control to an
// empty default (#2924): osquery.host_posture.v1 is routed by evidence type,
// so one record reaches cc6_6 and cc4_1 alike instead of the scf:END-03
// anchor no control matches. An explicit value is kept as provenance.
func TestHostPostureControlDefaultsToNoControl(t *testing.T) {
	t.Parallel()
	fl := newRunCmd().Flags().Lookup("host-posture-control")
	if fl == nil {
		t.Fatal("--host-posture-control not registered")
	}
	if fl.DefValue != "" {
		t.Errorf("--host-posture-control default = %q, want empty", fl.DefValue)
	}
}
