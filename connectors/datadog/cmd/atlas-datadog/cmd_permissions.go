package main

import (
	"fmt"
	"strings"
	"text/tabwriter"

	"github.com/spf13/cobra"

	"github.com/mgoodric/security-atlas/connectors/datadog/internal/datadogauth"
)

// newPermissionsCmd prints the documented least-privilege read-only Datadog
// scope the connector requires. Lets ops surface the canonical minimum without
// grepping the source. The datadogauth test holds RequiredScope to a read-only
// value (P0-488-2).
func newPermissionsCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "permissions",
		Short: "print the least-privilege read-only Datadog scope this connector requires",
		Run: func(cmd *cobra.Command, _ []string) {
			tw := tabwriter.NewWriter(cmd.OutOrStdout(), 0, 2, 2, ' ', 0)
			_, _ = fmt.Fprintln(tw, "CREDENTIAL\tSCOPE\tGATES")
			_, _ = fmt.Fprintf(tw, "%s\t%s\t%s\n",
				"Application key", datadogauth.RequiredScope, "monitoring.alert_config.v1 (monitor inventory)")
			_, _ = fmt.Fprintf(tw, "%s\t%s\t%s\n",
				"Application key", datadogauth.RequiredSIEMScope, "datadog.siem_rule.v1 (Cloud-SIEM detection-rule inventory)")
			_, _ = fmt.Fprintf(tw, "%s\t%s\t%s\n",
				"Application key", datadogauth.RequiredSignalScope, "datadog.siem_signal.v1 (Cloud-SIEM signal-history triage outcomes)")
			_, _ = fmt.Fprintf(tw, "%s\t%s\t%s\n",
				"Application key", datadogauth.RequiredFiringScope, "monitoring.alert_firing.v1 (monitor alert-firing history)")
			_, _ = fmt.Fprintf(tw, "%s\t%s\t%s\n",
				"API key", "(no scope — identifies the org)", "required alongside the Application key")
			_ = tw.Flush()
			_, _ = fmt.Fprintln(cmd.OutOrStdout(),
				strings.TrimSpace(`
NEVER grant a write/admin Application-key scope (no monitors_write, no
security_monitoring_rules_write, no security_monitoring_signals_write, no
events_write, no admin). The connector issues only read GETs against
/api/v1/monitor, /api/v2/security_monitoring/rules,
/api/v2/security_monitoring/signals, and /api/v1/events.`))
		},
	}
}
