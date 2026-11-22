package main

import (
	"testing"

	"github.com/spf13/cobra"
)

// TestRoutedKindFlagsDefaultToNoControl pins the control flags of the routed
// kinds (#2924) to an empty default, so a record on default flags reaches
// every control that queries its kind instead of one SCF anchor no control
// matches. The flags still accept an explicit value, kept as provenance.
func TestRoutedKindFlagsDefaultToNoControl(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		cmd  *cobra.Command
		flag string
	}{
		{newRunCmd(), "repo-protection-control"},
		{newWebhookCmd(), "control-id"},
	} {
		fl := tc.cmd.Flags().Lookup(tc.flag)
		if fl == nil {
			t.Fatalf("--%s not registered on %s", tc.flag, tc.cmd.Name())
		}
		if fl.DefValue != "" {
			t.Errorf("%s --%s default = %q, want empty", tc.cmd.Name(), tc.flag, fl.DefValue)
		}
	}
}
