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
const ConnectorName = "datadog-connector"

// SupportedKinds is the canonical list of evidence kinds the Datadog connector
// emits.
//   - monitoring.alert_config.v1 (Datadog monitor inventory, pull — slice 488)
//   - datadog.siem_rule.v1        (Cloud-SIEM detection-rule inventory, pull —
//     slice 533; the slice-488 D1 sibling-kind split)
var SupportedKinds = []string{
	"monitoring.alert_config.v1",
	"datadog.siem_rule.v1",
}

// PullInterval names the connector's pull cadence HONESTLY (P0-488-6). The
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
		Short:         "security-atlas Datadog monitoring connector",
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
	return "connector:datadog:" + service + "@" + connectorVersion()
}

func sdkOpts() []sdk.Option {
	if common.insecure {
		return []sdk.Option{sdk.WithInsecure()}
	}
	return nil
}

const longDescription = `security-atlas Datadog monitoring connector

Emits two evidence kinds:
  - monitoring.alert_config.v1  (run subcommand, pull — Datadog monitor inventory)
  - datadog.siem_rule.v1        (run subcommand, pull — Cloud-SIEM detection-rule
                                 inventory; the slice-488 D1 sibling-kind split)

Profile: pull. Each invocation is one bounded read-and-push pass on an
operator-scheduled cadence (recommended 24h). This is NOT continuous
monitoring — the interval is named honestly.

Least-privilege Datadog access (read-only):
  - an API key + an Application key scoped EXACTLY 'monitors_read' +
    'security_monitoring_rules_read'.
  - NEVER grant a write/admin scope (no monitors_write, no
    security_monitoring_rules_write, no admin).

The connector reads CONFIGURATION only — monitor / detection-rule name, type /
detection class, enabled state, severity (SIEM rules only), and the
notification-target HANDLES (e.g. @slack-sec-oncall). It NEVER collects the
secret webhook URL behind an integration, an integration token, a recipient
email address (PII), the monitor query, the detection query, firing signals,
matched log samples, matched-event payloads, dashboard JSON, or metric
time-series. Email-recipient mentions are dropped.

Auth: set DATADOG_API_KEY + DATADOG_APP_KEY (and optionally DATADOG_SITE). The
keys are read from the environment, never a CLI flag, and never logged or placed
into an evidence record.`
