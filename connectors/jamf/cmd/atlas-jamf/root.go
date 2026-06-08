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
const ConnectorName = "jamf-connector"

// SupportedKinds is the canonical list of evidence kinds the Jamf connector
// emits.
//   - endpoint.device_posture.v1     (managed-computer posture summary, pull; slice 490)
//   - endpoint.software_inventory.v1 (installed-software inventory, pull; slice 555)
//   - endpoint.config_profile.v1     (configuration-profile detail, pull; slice 556)
var SupportedKinds = []string{
	"endpoint.device_posture.v1",
	"endpoint.software_inventory.v1",
	"endpoint.config_profile.v1",
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
		Short:         "security-atlas Jamf Pro MDM endpoint-posture connector",
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
	root.AddCommand(newRunConfigProfilesCmd())
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
	return "connector:jamf:" + service + "@" + connectorVersion()
}

func sdkOpts() []sdk.Option {
	if common.insecure {
		return []sdk.Option{sdk.WithInsecure()}
	}
	return nil
}

const longDescription = `security-atlas Jamf Pro MDM endpoint connector

Emits three evidence kinds:
  - endpoint.device_posture.v1     (run subcommand, pull — managed-computer posture summary)
  - endpoint.software_inventory.v1 (run-software subcommand, pull — installed-software inventory)
  - endpoint.config_profile.v1     (run-config-profiles subcommand, pull — configuration-profile detail)

Profile: pull. Each invocation is one bounded read-and-push pass on an
operator-scheduled cadence (recommended 24h). This is NOT continuous
monitoring — the interval is named honestly.

Least-privilege Jamf Pro access (read-only):
  - an API client (client id + secret) bound to an API role with ONLY the
    read-inventory privileges (Read Computers, Read Mobile Devices).
  - NEVER grant a management/write privilege. A write-capable MDM credential
    can remote-wipe or push configuration to employee endpoints — that is a
    remote-wipe risk and must never be used.

The 'run' subcommand reads device POSTURE SUMMARY only — disk-encryption state
(FileVault), screen-lock/passcode-policy compliance, OS version, managed/
supervised/enrollment state, and the device->owner ASSIGNMENT identity (opaque
user id + display name). It NEVER collects device geolocation, installed-app
inventory, device contents, browsing data, or owner personal contact detail
(phone / personal email / address).

The 'run-software' subcommand reads the installed-software INVENTORY (the
APPLICATIONS section) for patch-/vulnerability-management evidence. Each item
carries the app name + version + bundle id + install date ONLY. It NEVER
collects executable file paths, per-user app-usage telemetry, license keys,
device contents, or owner personal contact detail.

The 'run-config-profiles' subcommand reads the configuration-profile DETAIL (the
CONFIGURATION_PROFILES section) for configuration-management evidence (SCF
CFG-02 / CFG-04). Each profile carries the name + identifier + type + assigned
scope + uuid + last-modified + a bounded list of compliance-relevant settings.
It NEVER collects the secrets configuration profiles routinely embed — Wi-Fi
PSKs, VPN shared secrets, certificate private keys, API tokens, SCEP challenges,
or raw payload-content blobs: the read requests profile metadata only and the
settings field is restricted to a non-secret compliance-relevant allow-list.

Auth: set JAMF_BASE_URL + JAMF_CLIENT_ID + JAMF_CLIENT_SECRET. The client
secret is read from the environment, never a CLI flag, and never logged or
placed into an evidence record.`
