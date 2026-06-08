package main

import (
	"fmt"
	"strings"
	"text/tabwriter"

	"github.com/spf13/cobra"

	"github.com/mgoodric/security-atlas/connectors/bamboohr/internal/bamboohrauth"
)

// newPermissionsCmd prints the documented least-privilege read-only BambooHR API
// scope the connector requires. Lets ops surface the canonical minimum without
// grepping the source. The bamboohrauth test holds RequiredScope to a read-only
// value (P0-491-2).
func newPermissionsCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "permissions",
		Short: "print the least-privilege read-only BambooHR API scope this connector requires",
		Run: func(cmd *cobra.Command, _ []string) {
			tw := tabwriter.NewWriter(cmd.OutOrStdout(), 0, 2, 2, ' ', 0)
			_, _ = fmt.Fprintln(tw, "CREDENTIAL\tSCOPE\tGATES")
			_, _ = fmt.Fprintf(tw, "%s\t%s\t%s\n",
				"API key", bamboohrauth.RequiredScope, "hris.worker_lifecycle.v1 (worker roster + employment status)")
			_ = tw.Flush()
			_, _ = fmt.Fprintln(cmd.OutOrStdout(),
				strings.TrimSpace(`
NEVER use an API key for a role that can see compensation, SSN, bank, benefits,
home address, or performance, or that can EDIT employees (write scope). The HRIS
holds the most sensitive PII in the customer's stack; the connector requests ONLY
the worker-lifecycle fields via a scoped custom report (roster + employment
status + dates + title + department + manager + work email) and pushes lifecycle
facts only into the append-only evidence ledger.`))
		},
	}
}
