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
			_, _ = fmt.Fprintln(tw, "SURFACE\tREAD PERMISSION (least privilege)\tGATES")
			_, _ = fmt.Fprintf(tw, "%s\t%s\t%s\n",
				"alert-config", grafanaauth.RequiredRole+" role",
				"monitoring.alert_config.v1 (alert rules + contact-point names)")
			_, _ = fmt.Fprintf(tw, "%s\t%s\t%s\n",
				"access-config", grafanaauth.SSOSettingsReadPermission,
				"grafana.access_config.v1 (SSO settings)")
			_, _ = fmt.Fprintf(tw, "%s\t%s\t%s\n",
				"access-config", grafanaauth.AccessControlReadPermission,
				"grafana.access_config.v1 (RBAC role assignments)")
			_, _ = fmt.Fprintf(tw, "%s\t%s\t%s\n",
				"alert-firing", grafanaauth.RequiredRole+" role",
				"monitoring.alert_firing.v1 (alert state-history)")
			_ = tw.Flush()
			_, _ = fmt.Fprintln(cmd.OutOrStdout(),
				strings.TrimSpace(`
NEVER grant Editor or Admin "to be safe". The Viewer role lists alert rules +
contact points. The access-config surface needs, IN ADDITION to Viewer, two
read-only fixed-role permissions — fixed:settings:reader (settings:read on
settings:auth.* to read SSO settings) and fixed:roles:reader (roles:read +
users.roles:read + teams.roles:read to enumerate RBAC assignments). Both are
strictly read-only; the connector issues only read GETs and never reads a
contact point's secret settings, a SAML private key, an OAuth client secret, an
LDAP bind password, or any user identity.`))
		},
	}
}
