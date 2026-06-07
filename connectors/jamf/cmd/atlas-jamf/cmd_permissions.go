package main

import (
	"fmt"
	"strings"
	"text/tabwriter"

	"github.com/spf13/cobra"

	"github.com/mgoodric/security-atlas/connectors/jamf/internal/jamfauth"
)

// newPermissionsCmd prints the documented least-privilege read-only Jamf Pro
// API role the connector requires. Lets ops surface the canonical minimum
// without grepping the source. The jamfauth test holds RequiredRole to a
// read-only value (P0-490-2).
func newPermissionsCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "permissions",
		Short: "print the least-privilege read-only Jamf API role this connector requires",
		Run: func(cmd *cobra.Command, _ []string) {
			tw := tabwriter.NewWriter(cmd.OutOrStdout(), 0, 2, 2, ' ', 0)
			_, _ = fmt.Fprintln(tw, "CREDENTIAL\tROLE\tGATES")
			_, _ = fmt.Fprintf(tw, "%s\t%s\t%s\n",
				"API client (id + secret)", jamfauth.RequiredRole, "endpoint.device_posture.v1 (computer posture summary)")
			_ = tw.Flush()
			_, _ = fmt.Fprintln(cmd.OutOrStdout(),
				strings.TrimSpace(`
NEVER grant a management/write privilege. A write-capable MDM credential can
remote-wipe or push configuration to employee endpoints — that is a remote-wipe
risk and must never be used. The connector issues only read GETs against
/api/v1/computers-inventory (posture-relevant sections only).`))
		},
	}
}
