package main

import (
	"fmt"
	"strings"
	"text/tabwriter"

	"github.com/spf13/cobra"

	"github.com/mgoodric/security-atlas/connectors/intune/internal/intuneauth"
)

// newPermissionsCmd prints the documented least-privilege read-only Graph
// permission the connector requires. Lets ops surface the canonical minimum
// without grepping the source. The intuneauth test holds RequiredPermission to
// a read-only value (P0-490-2).
func newPermissionsCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "permissions",
		Short: "print the least-privilege read-only Graph permission this connector requires",
		Run: func(cmd *cobra.Command, _ []string) {
			tw := tabwriter.NewWriter(cmd.OutOrStdout(), 0, 2, 2, ' ', 0)
			_, _ = fmt.Fprintln(tw, "CREDENTIAL\tPERMISSION\tGATES")
			_, _ = fmt.Fprintf(tw, "%s\t%s\t%s\n",
				"Entra app (tenant + client + secret)", intuneauth.RequiredPermission, "endpoint.device_posture.v1 (device compliance summary)")
			_ = tw.Flush()
			_, _ = fmt.Fprintln(cmd.OutOrStdout(),
				strings.TrimSpace(`
NEVER grant a write/management permission (no ...ReadWrite.All, no
...PrivilegedOperations.All). A write-capable MDM credential can remote-wipe or
push configuration to employee endpoints — that is a remote-wipe risk and must
never be used. The connector issues only read GETs against
/deviceManagement/managedDevices (posture-relevant $select only).`))
		},
	}
}
