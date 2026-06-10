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
// the source. The DocumentedClusterRole test (in k8sauth) holds the base list to
// a get/list-only, no-Secrets, no-wildcard contract (P0-487-2 / P0-487-3).
//
// --secret-inventory prints the role INCLUDING the one extra `secrets` get/list
// rule that the OPT-IN k8s.secret_inventory.v1 mode requires (slice 525). The
// SecretInventoryClusterRole test pins that this adds EXACTLY that one rule and
// still rejects write verbs / wildcards. The secrets grant is the one access the
// base connector intentionally withholds.
func newPermissionsCmd() *cobra.Command {
	var secretInventory bool
	var admissionEvidence bool
	var subscribe bool
	cmd := &cobra.Command{
		Use:   "permissions",
		Short: "print the least-privilege read-only ClusterRole this connector requires",
		Run: func(cmd *cobra.Command, _ []string) {
			rules := k8sauth.DocumentedClusterRole()
			if subscribe {
				rules = k8sauth.SubscribeClusterRole()
				_, _ = fmt.Fprintln(cmd.OutOrStdout(),
					"# event-driven (subscribe) profile (slice 526): the base role with the `watch`")
				_, _ = fmt.Fprintln(cmd.OutOrStdout(),
					"# verb ADDED (alongside get,list) on EXACTLY the rbac + apps surfaces the watch")
				_, _ = fmt.Fprintln(cmd.OutOrStdout(),
					"# consumer streams. No new resource, never 'secrets', never a write verb, never")
				_, _ = fmt.Fprintln(cmd.OutOrStdout(),
					"# a wildcard. Every other rule stays get,list-only.")
			}
			if admissionEvidence {
				rules = k8sauth.AdmissionEvidenceClusterRole()
				_, _ = fmt.Fprintln(cmd.OutOrStdout(),
					"# admission-evidence mode (slice 652): the base role PLUS the admission-webhook")
				_, _ = fmt.Fprintln(cmd.OutOrStdout(),
					"# get/list rule (admissionregistration.k8s.io) and the OPTIONAL policy-engine")
				_, _ = fmt.Fprintln(cmd.OutOrStdout(),
					"# get/list rules (templates.gatekeeper.sh + kyverno.io — only meaningful when the")
				_, _ = fmt.Fprintln(cmd.OutOrStdout(),
					"# engine is installed). CONFIG metadata only — NEVER a caBundle/TLS key or a")
				_, _ = fmt.Fprintln(cmd.OutOrStdout(),
					"# policy Rego/CEL body. Still get,list-only, no secrets, no wildcard.")
			}
			if secretInventory {
				rules = k8sauth.SecretInventoryClusterRole()
				_, _ = fmt.Fprintln(cmd.OutOrStdout(),
					"# secret-inventory mode (slice 525): includes the ONE extra 'secrets' get/list grant")
				_, _ = fmt.Fprintln(cmd.OutOrStdout(),
					"# the base connector withholds. Even with it, the connector reads Secret METADATA")
				_, _ = fmt.Fprintln(cmd.OutOrStdout(),
					"# ONLY (type/namespace/name/age/key-NAMES) — never a Secret value.")
			}
			tw := tabwriter.NewWriter(cmd.OutOrStdout(), 0, 2, 2, ' ', 0)
			_, _ = fmt.Fprintln(tw, "API GROUP\tRESOURCES\tVERBS\tGATES")
			for _, r := range rules {
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
	cmd.Flags().BoolVar(&secretInventory, "secret-inventory", false,
		"include the OPT-IN 'secrets' get/list rule the k8s.secret_inventory.v1 mode requires (slice 525)")
	cmd.Flags().BoolVar(&admissionEvidence, "admission", false,
		"include the admission-webhook + policy-engine (OPA/Gatekeeper + Kyverno) get/list rules the k8s.admission_webhook.v1 + k8s.admission_policy.v1 kinds require (slice 652)")
	cmd.Flags().BoolVar(&subscribe, "subscribe", false,
		"print the event-driven (subscribe) profile ClusterRole: the base role with the `watch` verb added on the rbac + apps surfaces (slice 526)")
	return cmd
}
