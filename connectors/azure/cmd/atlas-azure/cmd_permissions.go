package main

import (
	"fmt"
	"text/tabwriter"

	"github.com/spf13/cobra"

	"github.com/mgoodric/security-atlas/connectors/azure/internal/azureauth"
)

// newPermissionsCmd prints the documented least-privilege Azure permissions.
// Lets ops surface the canonical list without grepping the source. The
// DocumentedPermissions test (in azureauth) holds this list to a "Read"-only
// contract (P0-486-2).
func newPermissionsCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "permissions",
		Short: "print the least-privilege read-only Azure permissions this connector requires",
		Run: func(cmd *cobra.Command, _ []string) {
			tw := tabwriter.NewWriter(cmd.OutOrStdout(), 0, 2, 2, ' ', 0)
			_, _ = fmt.Fprintln(tw, "SURFACE\tNAME\tACCESS\tGATES")
			for _, p := range azureauth.DocumentedPermissions() {
				_, _ = fmt.Fprintf(tw, "%s\t%s\t%s\t%s\n", p.Surface, p.Name, p.Access, p.Gates)
			}
			_ = tw.Flush()
		},
	}
}
