package main

import (
	"fmt"
	"strings"
	"text/tabwriter"

	"github.com/spf13/cobra"

	"github.com/mgoodric/security-atlas/connectors/grafana/internal/grafanaauth"
)

// newPermissionsCmd prints the documented least-privilege read-only Grafana
// role the connector requires. The grafanaauth test holds RequiredRole to a
// read-only value (P0-488-2).
func newPermissionsCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "permissions",
		Short: "print the least-privilege read-only Grafana role this connector requires",
		Run: func(cmd *cobra.Command, _ []string) {
			tw := tabwriter.NewWriter(cmd.OutOrStdout(), 0, 2, 2, ' ', 0)
			_, _ = fmt.Fprintln(tw, "CREDENTIAL\tROLE\tGATES")
			_, _ = fmt.Fprintf(tw, "%s\t%s\t%s\n",
				"Service-account token", grafanaauth.RequiredRole,
				"monitoring.alert_config.v1 (alert rules + contact-point names)")
			_ = tw.Flush()
			_, _ = fmt.Fprintln(cmd.OutOrStdout(),
				strings.TrimSpace(`
NEVER grant Editor or Admin. The Viewer role can list alert rules + contact
points; the connector issues only read GETs against the provisioning API and
never reads a contact point's secret settings.`))
		},
	}
}
