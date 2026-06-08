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
const ConnectorName = "intune-connector"

// SupportedKinds is the canonical list of evidence kinds the Intune connector
// emits.
//   - endpoint.device_posture.v1     (managed-device compliance posture summary, pull; slice 490)
//   - endpoint.software_inventory.v1 (detected-software inventory, pull; slice 555)
var SupportedKinds = []string{
	"endpoint.device_posture.v1",
	"endpoint.software_inventory.v1",
}

// PullInterval names the connector's pull cadence HONESTLY (P0-490-6). The
// connector is run-on-a-schedule (cron / scheduler), not "continuous
// monitoring": each invocation is one bounded read-and-push pass.
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
		Short:         "security-atlas Microsoft Intune MDM endpoint-posture connector",
		Long:          longDescription,
		SilenceUsage:  true,
		SilenceErrors: true,
	}
	root.PersistentFlags().StringVar(&common.endpoint, "endpoint", "", "platform gRPC endpoint (env: SECURITY_ATLAS_ENDPOINT)")
	root.PersistentFlags().StringVar(&common.token, "token", "", "platform bearer token (env: SECURITY_ATLAS_TOKEN)")
	root.PersistentFlags().BoolVar(&common.insecure, "insecure", false, "disable TLS to platform (loopback endpoints only)")
	root.AddCommand(newRegisterCmd())
	root.AddCommand(newRunCmd())
	root.AddCommand(newRunSoftwareCmd())
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
	return "connector:intune:" + service + "@" + connectorVersion()
}

func sdkOpts() []sdk.Option {
	if common.insecure {
		return []sdk.Option{sdk.WithInsecure()}
	}
	return nil
}

const longDescription = `security-atlas Microsoft Intune MDM endpoint connector

Emits two evidence kinds:
  - endpoint.device_posture.v1     (run subcommand, pull — managed-device compliance posture summary)
  - endpoint.software_inventory.v1 (run-software subcommand, pull — detected-software inventory)

Profile: pull. Each invocation is one bounded read-and-push pass on an
operator-scheduled cadence (recommended 24h). This is NOT continuous
monitoring — the interval is named honestly.

Least-privilege Microsoft Graph access (read-only):
  - an Entra (Azure AD) app registration granted ONLY the application permission
    DeviceManagementManagedDevices.Read.All (admin-consented).
  - NEVER grant a write/management permission (no ...ReadWrite.All, no
    ...PrivilegedOperations.All). A write-capable MDM credential can remote-wipe
    or push configuration to employee endpoints — that is a remote-wipe risk and
    must never be used.

The 'run' subcommand reads device compliance POSTURE SUMMARY only —
disk-encryption state (BitLocker), screen-lock/passcode-policy compliance (via
complianceState), OS version, management/enrollment state, the MDM's compliance
verdict, and the device->owner ASSIGNMENT identity (userPrincipalName + display
name). It NEVER collects device geolocation, the detectedApps inventory, device
contents, browsing data, or owner personal contact detail (phone / personal
email).

The 'run-software' subcommand reads the detected-software INVENTORY (the
detectedApps endpoint) for patch-/vulnerability-management evidence, using the
SAME read-only DeviceManagementManagedDevices.Read.All permission. Each item
carries the app name + version + Graph app id ONLY. It NEVER collects executable
file paths, per-user app-usage telemetry, license keys, device contents, or owner
personal contact detail.

Auth: set INTUNE_TENANT_ID + INTUNE_CLIENT_ID + INTUNE_CLIENT_SECRET. The
client secret is read from the environment, never a CLI flag, and never logged
or placed into an evidence record.`
