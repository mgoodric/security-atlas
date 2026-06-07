package main

import (
	"fmt"
	"strings"
	"text/tabwriter"

	"github.com/spf13/cobra"

	"github.com/mgoodric/security-atlas/connectors/k8s/internal/k8sauth"
)

// newPermissionsCmd prints the documented least-privilege read-only ClusterRole
// the connector requires. Lets ops surface the canonical list without grepping
// the source. The DocumentedClusterRole test (in k8sauth) holds this list to a
// get/list-only, no-Secrets, no-wildcard contract (P0-487-2 / P0-487-3).
func newPermissionsCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "permissions",
		Short: "print the least-privilege read-only ClusterRole this connector requires",
		Run: func(cmd *cobra.Command, _ []string) {
			tw := tabwriter.NewWriter(cmd.OutOrStdout(), 0, 2, 2, ' ', 0)
			_, _ = fmt.Fprintln(tw, "API GROUP\tRESOURCES\tVERBS\tGATES")
			for _, r := range k8sauth.DocumentedClusterRole() {
				group := strings.Join(r.APIGroups, ",")
				if group == "" {
					group = "(core)"
				}
				_, _ = fmt.Fprintf(tw, "%s\t%s\t%s\t%s\n",
					group, strings.Join(r.Resources, ","), strings.Join(r.SortedVerbs(), ","), r.Gates)
			}
			_ = tw.Flush()
		},
	}
}
