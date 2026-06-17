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

// ConnectorName is logged in `connectors list`. The corresponding
// SupportedKinds list is what AC-1 references.
const ConnectorName = "azure-connector"

// SupportedKinds is the canonical list of evidence kinds the connector emits.
//   - azure.entra_role_assignment.v1   (Microsoft Graph, pull)        — slice 486
//   - azure.storage_account_config.v1  (Azure Resource Manager, pull) — slice 486
//   - azure.aks_cluster_config.v1      (Azure Resource Manager, pull) — slice 519
//   - azure.nsg_rules.v1               (Azure Resource Manager, pull) — slice 520
//   - azure.keyvault_access_config.v1  (Azure Resource Manager, pull) — slice 521
//   - azure.firewall_rules.v1          (Azure Resource Manager, pull) — slice 614
var SupportedKinds = []string{
	"azure.entra_role_assignment.v1",
	"azure.storage_account_config.v1",
	"azure.aks_cluster_config.v1",
	"azure.nsg_rules.v1",
	"azure.keyvault_access_config.v1",
	"azure.firewall_rules.v1",
}

// PullInterval names the connector's pull cadence HONESTLY (P0-486-6). The
// connector is run-on-a-schedule (cron / scheduler), not "continuous
// monitoring": each invocation is one bounded read-and-push pass. The
// recommended cadence is documented; the operator owns the actual schedule.
const PullInterval = "24h (recommended; operator-scheduled — NOT continuous monitoring)"

// ProfilesSupported is what the connector advertises at register time, named
// HONESTLY (slice 522 / P0-522-3):
//   - pull:      scheduled read-only Graph + ARM GETs (the `run` subcommand). One
//     bounded read-and-push pass per invocation; operator-scheduled (recommended
//     24h). The reconciliation backstop.
//   - subscribe: event-driven receipt of Azure Event Grid change events (the
//     `eventgrid` subcommand) — an in-scope resource change triggers a re-read of
//     that resource via the SAME read-only path the pull profile uses, emitting a
//     fresh record promptly. Event-driven means Event-Grid delivery latency
//     (typically seconds to a minute) plus the coalescing window — NOT
//     instantaneous, and NOT "continuous monitoring".
//
// Both values describe how the connector retrieves data FROM Azure. The
// platform-side wire is ALWAYS push (invariant #3) regardless of either value.
var ProfilesSupported = []string{"pull", "subscribe"}

// commonFlags is the persistent set every subcommand needs.
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
		Short:         "security-atlas Azure connector",
		Long:          longDescription,
		SilenceUsage:  true,
		SilenceErrors: true,
	}
	root.PersistentFlags().StringVar(&common.endpoint, "endpoint", "", "platform gRPC endpoint (env: SECURITY_ATLAS_ENDPOINT)")
	root.PersistentFlags().StringVar(&common.token, "token", "", "platform bearer token (env: SECURITY_ATLAS_TOKEN)")
	root.PersistentFlags().BoolVar(&common.insecure, "insecure", false, "disable TLS to platform (loopback endpoints only)")
	root.AddCommand(newRegisterCmd())
	root.AddCommand(newRunCmd())
	root.AddCommand(newEventGridCmd())
	root.AddCommand(newPermissionsCmd())
	root.AddCommand(newProvisionCmd())
	root.AddCommand(newDeprovisionCmd())
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
	return "connector:azure:" + service + "@" + connectorVersion()
}

func sdkOpts() []sdk.Option {
	if common.insecure {
		return []sdk.Option{sdk.WithInsecure()}
	}
	return nil
}

const longDescription = `security-atlas Azure connector

Emits six evidence kinds:
  - azure.entra_role_assignment.v1   (run subcommand, pull — Microsoft Graph)
  - azure.storage_account_config.v1  (run subcommand, pull — Azure Resource Manager)
  - azure.aks_cluster_config.v1      (run subcommand, pull — Azure Resource Manager)
  - azure.nsg_rules.v1               (run subcommand, pull — Azure Resource Manager)
  - azure.keyvault_access_config.v1  (run subcommand, pull — Azure Resource Manager)
  - azure.firewall_rules.v1          (run subcommand, pull — Azure Resource Manager)

Profile: pull. Each invocation is one bounded read-and-push pass on an
operator-scheduled cadence (recommended 24h). This is NOT continuous
monitoring — the interval is named honestly.

Least-privilege Azure permissions (read-only):
  - Microsoft Graph application permission: Directory.Read.All
  - Microsoft Graph application permission: Application.Read.All
  - Azure Resource Manager built-in role:   Reader (on the in-scope subscription)

Never grant *.ReadWrite.* / *.Manage / Owner / Contributor / Global
Administrator. Use the 'permissions' subcommand to print the canonical list.

Auth: an Entra app-registration (client-credentials) or a managed identity.
Set AZURE_TENANT_ID + AZURE_CLIENT_ID + AZURE_CLIENT_SECRET in this process
(client-credentials), or pass --auth-mode managed-identity. The client secret
is never logged and never enters an evidence record.`
