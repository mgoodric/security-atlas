package main

import (
	"fmt"
	"text/tabwriter"

	"github.com/spf13/cobra"

	"github.com/mgoodric/security-atlas/connectors/jira/internal/jiraauth"
)

// newScopesCmd prints the documented least-privilege scopes for both
// Jira and Linear. Lets ops surface the canonical scope list without
// grepping the source.
func newScopesCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "scopes",
		Short: "print the least-privilege scopes this connector requires (Jira + Linear)",
		Run: func(cmd *cobra.Command, _ []string) {
			tw := tabwriter.NewWriter(cmd.OutOrStdout(), 0, 2, 2, ' ', 0)
			_, _ = fmt.Fprintln(tw, "PLATFORM\tNAME\tACCESS\tGATES")
			for _, s := range jiraauth.DocumentedScopes() {
				_, _ = fmt.Fprintf(tw, "%s\t%s\t%s\t%s\n", s.Platform, s.Name, s.Access, s.Gates)
			}
			_ = tw.Flush()
		},
	}
}
