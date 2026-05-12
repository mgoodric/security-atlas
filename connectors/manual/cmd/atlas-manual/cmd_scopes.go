package main

import (
	"fmt"
	"text/tabwriter"

	"github.com/spf13/cobra"
)

// authPosture is the documented per-mode auth contract. Lets ops surface
// the canonical posture list without grepping the source.
type authPosture struct {
	Mode  string
	Auth  string
	Notes string
}

func documentedPostures() []authPosture {
	return []authPosture{
		{
			Mode:  "local",
			Auth:  "none",
			Notes: "operator owns the file system — no platform-side credentials",
		},
		{
			Mode:  "s3",
			Auth:  "standard AWS credential chain",
			Notes: "env > profile > IRSA > IMDS; flag-passed static access keys are NEVER accepted",
		},
		{
			Mode:  "sftp",
			Auth:  "SSH key + known_hosts",
			Notes: "private key read from --key-file (never a flag value); --known-hosts mandatory; InsecureIgnoreHostKey rejected",
		},
	}
}

func newScopesCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "scopes",
		Short: "print the per-mode auth posture this connector enforces",
		Run: func(cmd *cobra.Command, _ []string) {
			tw := tabwriter.NewWriter(cmd.OutOrStdout(), 0, 2, 2, ' ', 0)
			_, _ = fmt.Fprintln(tw, "MODE\tAUTH\tNOTES")
			for _, p := range documentedPostures() {
				_, _ = fmt.Fprintf(tw, "%s\t%s\t%s\n", p.Mode, p.Auth, p.Notes)
			}
			_ = tw.Flush()
		},
	}
}
