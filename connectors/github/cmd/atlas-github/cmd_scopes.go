package main

import (
	"fmt"
	"text/tabwriter"

	"github.com/spf13/cobra"

	"github.com/mgoodric/security-atlas/connectors/github/internal/githubauth"
)

// newScopesCmd prints the documented least-privilege scopes. Lets ops
// surface the canonical scope list without grepping the source.
func newScopesCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "scopes",
		Short: "print the least-privilege PAT scopes this connector requires",
		Run: func(cmd *cobra.Command, _ []string) {
			tw := tabwriter.NewWriter(cmd.OutOrStdout(), 0, 2, 2, ' ', 0)
			_, _ = fmt.Fprintln(tw, "TOKEN_KIND\tNAME\tACCESS\tGATES")
			for _, s := range githubauth.DocumentedScopes() {
				_, _ = fmt.Fprintf(tw, "%s\t%s\t%s\t%s\n", s.TokenKind, s.Name, s.Access, s.Gates)
			}
			_ = tw.Flush()
		},
	}
}
