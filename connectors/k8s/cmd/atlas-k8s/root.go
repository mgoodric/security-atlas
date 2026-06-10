package main

import (
	"context"
	"crypto/tls"
	"fmt"
	"os"
	"runtime/debug"
	"time"

	"github.com/spf13/cobra"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/metadata"

	connectorsv1 "github.com/mgoodric/security-atlas/gen/proto/connectors/v1"
	sdk "github.com/mgoodric/security-atlas/pkg/sdk-go"
)

// ConnectorName is logged in `connectors list`.
const ConnectorName = "k8s-connector"

// SupportedKinds is the canonical list of evidence kinds the connector emits.
//   - k8s.rbac_binding.v1                 (Kubernetes RBAC, pull — slice 487)
//   - k8s.workload_security_context.v1    (Kubernetes apps workloads, pull — slice 487)
//   - k8s.networkpolicy_coverage.v1       (Kubernetes NetworkPolicy posture, pull — slice 523)
//   - k8s.pod_security_admission.v1       (Kubernetes PSS admission config, pull — slice 524)
//   - k8s.secret_inventory.v1             (Kubernetes Secret METADATA inventory, pull — slice 525; OPT-IN)
//   - k8s.admission_webhook.v1            (Kubernetes admission-webhook CONFIG metadata, pull — slice 652)
//   - k8s.admission_policy.v1             (OPA/Gatekeeper + Kyverno policy CONFIG metadata, pull — slice 652)
var SupportedKinds = []string{
	"k8s.rbac_binding.v1",
	"k8s.workload_security_context.v1",
	"k8s.networkpolicy_coverage.v1",
	"k8s.pod_security_admission.v1",
	"k8s.secret_inventory.v1",
	"k8s.admission_webhook.v1",
	"k8s.admission_policy.v1",
}

// PullInterval names the connector's pull cadence HONESTLY (P0-487-6). The
// connector is run-on-a-schedule (cron / scheduler), not "continuous
// monitoring": each invocation is one bounded read-and-push pass. The
// recommended cadence is documented; the operator owns the actual schedule.
const PullInterval = "24h (recommended; operator-scheduled — NOT continuous monitoring)"

// common is the persistent flag set every subcommand needs.
var common struct {
	endpoint string
	token    string
	insecure bool
}

func resolveCommon() error {
	if common.endpoint == "" {
		common.endpoint = os.Getenv("SECURITY_ATLAS_ENDPOINT")
	}
	if common.endpoint == "" {
		return fmt.Errorf("--endpoint or SECURITY_ATLAS_ENDPOINT is required")
	}
	if common.token == "" {
		common.token = os.Getenv("SECURITY_ATLAS_TOKEN")
	}
	if common.token == "" {
		return fmt.Errorf("--token or SECURITY_ATLAS_TOKEN is required")
	}
	return nil
}

func newRootCmd() *cobra.Command {
	root := &cobra.Command{
		Use:           ConnectorName,
		Short:         "security-atlas Kubernetes connector",
		Long:          longDescription,
		SilenceUsage:  true,
		SilenceErrors: true,
	}
	root.PersistentFlags().StringVar(&common.endpoint, "endpoint", "", "platform gRPC endpoint (env: SECURITY_ATLAS_ENDPOINT)")
	root.PersistentFlags().StringVar(&common.token, "token", "", "platform bearer token (env: SECURITY_ATLAS_TOKEN)")
	root.PersistentFlags().BoolVar(&common.insecure, "insecure", false, "disable TLS to platform (loopback endpoints only)")
	root.AddCommand(newRegisterCmd())
	root.AddCommand(newRunCmd())
	root.AddCommand(newPermissionsCmd())
	return root
}

// dialConnectorRegistry opens a gRPC connection scoped to the ConnectorRegistry
// service. The caller owns the returned conn.
func dialConnectorRegistry() (connectorsv1.ConnectorRegistryServiceClient, *grpc.ClientConn, error) {
	var transport grpc.DialOption
	if common.insecure {
		transport = grpc.WithTransportCredentials(insecure.NewCredentials())
	} else {
		transport = grpc.WithTransportCredentials(credentials.NewTLS(&tls.Config{MinVersion: tls.VersionTLS12}))
	}
	conn, err := grpc.NewClient(common.endpoint, transport)
	if err != nil {
		return nil, nil, fmt.Errorf("dial: %w", err)
	}
	return connectorsv1.NewConnectorRegistryServiceClient(conn), conn, nil
}

func authedContext(timeout time.Duration) (context.Context, context.CancelFunc) {
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	ctx = metadata.AppendToOutgoingContext(ctx, sdk.MetadataAuthorization, sdk.BearerPrefix+common.token)
	return ctx, cancel
}

// connectorVersion returns the build's module version. Falls back to "dev" when
// not running from a tagged release.
func connectorVersion() string {
	if info, ok := debug.ReadBuildInfo(); ok {
		if info.Main.Version != "" && info.Main.Version != "(devel)" {
			return info.Main.Version
		}
	}
	return "dev"
}

// actorID formats source_attribution.actor_id per the cross-connector
// convention `connector:<vendor>:<service>@<version>`.
func actorID(service string) string {
	return "connector:k8s:" + service + "@" + connectorVersion()
}

func sdkOpts() []sdk.Option {
	if common.insecure {
		return []sdk.Option{sdk.WithInsecure()}
	}
	return nil
}

const longDescription = `security-atlas Kubernetes connector

Emits seven evidence kinds:
  - k8s.rbac_binding.v1               (run subcommand, pull — Kubernetes RBAC)
  - k8s.workload_security_context.v1  (run subcommand, pull — apps workloads)
  - k8s.networkpolicy_coverage.v1     (run subcommand, pull — NetworkPolicies)
  - k8s.pod_security_admission.v1     (run subcommand, pull — PSS admission cfg)
  - k8s.admission_webhook.v1          (run subcommand, pull — admission-webhook
    CONFIG metadata; slice 652)
  - k8s.admission_policy.v1           (run subcommand, pull — OPA/Gatekeeper +
    Kyverno policy CONFIG metadata; slice 652)
  - k8s.secret_inventory.v1           (run --collect-secret-inventory, pull —
    Secret METADATA inventory; OPT-IN, slice 525)

Profile: pull. Each invocation is one bounded read-and-push pass on an
operator-scheduled cadence (recommended 24h). This is NOT continuous
monitoring — the interval is named honestly.

Least-privilege Kubernetes access (read-only ClusterRole):
  - rbac.authorization.k8s.io: roles, clusterroles, rolebindings,
    clusterrolebindings  — verbs get,list
  - apps: deployments, daemonsets, statefulsets  — verbs get,list
  - networking.k8s.io: networkpolicies  — verbs get,list
  - admissionregistration.k8s.io: validatingwebhookconfigurations,
    mutatingwebhookconfigurations  — verbs get,list  (NEW in slice 652 — gates
    the k8s.admission_webhook.v1 kind; CONFIG metadata only, NEVER the caBundle)
  - core: namespaces  — verbs get,list  (also gates the PSS admission kind —
    PSS config is namespace pod-security.kubernetes.io/* labels; NO new rule)

OPTIONAL (only meaningful when the policy engine is installed; detected by
API-discovery probe — absence is never an error):
  - templates.gatekeeper.sh: constrainttemplates  — verbs get,list  (OPA/
    Gatekeeper policy catalog — name/kind, NEVER the Rego body; slice 652)
  - kyverno.io: clusterpolicies, policies  — verbs get,list  (Kyverno policy
    CONFIG metadata — name/scope/enforcement-action, NEVER the rule body; 652)

NEVER grant write verbs, cluster-admin, or wildcards.

By default the connector reads RBAC + security-context + NetworkPolicy +
Pod-Security-Standards CONFIGURATION only — never Secret values, ConfigMap
values, container env, NetworkPolicy peer/port contents, pod specs, arbitrary
namespace labels/annotations, or logs. The base ClusterRole deliberately
EXCLUDES 'secrets'.

ONE OPT-IN EXCEPTION (slice 525): 'run --collect-secret-inventory' emits the
k8s.secret_inventory.v1 kind, which requires adding EXACTLY ONE rule —
core 'secrets' verbs get,list — to the ClusterRole. Even then the connector
collects Secret METADATA ONLY (type / namespace / name / age / key-NAMES); it
NEVER reads, decodes, or records a Secret VALUE (.data / .stringData). Use
'permissions --secret-inventory' to print the ClusterRole that includes the
secrets grant.

Auth: a read-only ServiceAccount token. Set KUBERNETES_API_SERVER +
KUBECONFIG_TOKEN (out-of-cluster), or pass --auth-mode in-cluster to read the
projected ServiceAccount token. The token is never logged and never enters an
evidence record.`
