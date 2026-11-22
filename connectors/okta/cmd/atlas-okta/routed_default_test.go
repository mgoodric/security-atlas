package main

import "testing"

// TestRoutedKindFlagsDefaultToNoControl pins the control flags of the routed
// kinds (#2924) to an empty default, so a record on default flags reaches
// every control that queries its kind instead of one SCF anchor no control
// matches. The flags still accept an explicit value, kept as provenance.
func TestRoutedKindFlagsDefaultToNoControl(t *testing.T) {
	t.Parallel()
	cmd := newRunCmd()
	for _, flag := range []string{"mfa-policy-control", "app-assignment-control", "user-lifecycle-control"} {
		fl := cmd.Flags().Lookup(flag)
		if fl == nil {
			t.Fatalf("--%s not registered", flag)
		}
		if fl.DefValue != "" {
			t.Errorf("--%s default = %q, want empty", flag, fl.DefValue)
		}
	}
}
