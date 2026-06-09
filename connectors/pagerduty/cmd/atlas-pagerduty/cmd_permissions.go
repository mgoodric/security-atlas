package main

import (
	"fmt"
	"strings"
	"text/tabwriter"

	"github.com/spf13/cobra"

	"github.com/mgoodric/security-atlas/connectors/pagerduty/internal/pagerdutyauth"
)

// newPermissionsCmd prints the documented least-privilege read-only PagerDuty
// token the connector requires. Lets ops surface the canonical minimum without
// grepping the source. The pagerdutyauth test holds RequiredScope to a
// read-only value (P0-489-2).
func newPermissionsCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "permissions",
		Short: "print the least-privilege read-only PagerDuty token this connector requires",
		Run: func(cmd *cobra.Command, _ []string) {
			tw := tabwriter.NewWriter(cmd.OutOrStdout(), 0, 2, 2, ' ', 0)
			_, _ = fmt.Fprintln(tw, "CREDENTIAL\tSCOPE\tGATES")
			_, _ = fmt.Fprintf(tw, "%s\t%s\t%s\n",
				"REST API token", pagerdutyauth.RequiredScope, "pagerduty.oncall_coverage.v1 + pagerduty.incident_summary.v1 + pagerduty.postmortem_summary.v1 + pagerduty.response_metrics.v1")
			_ = tw.Flush()
			_, _ = fmt.Fprintln(cmd.OutOrStdout(),
				strings.TrimSpace(`
NEVER use a full-access / write / admin PagerDuty token.
The connector issues only read GETs against /escalation_policies, /incidents,
and /postmortems, and never reads responder personal contact details, incident
free-text, postmortem narrative / timeline / root-cause prose, or WHICH NAMED
RESPONDER acted on an incident (response metrics are service-level aggregates).`))
		},
	}
}
