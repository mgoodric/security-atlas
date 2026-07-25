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
const ConnectorName = "grafana-connector"

// SupportedKinds is the canonical list of evidence kinds the Grafana connector
// emits.
//   - monitoring.alert_config.v1 (Grafana alert-rule + contact-point inventory, pull) — slice 488
//   - grafana.access_config.v1   (Grafana SSO + RBAC configuration evidence, pull) — slice 534
var SupportedKinds = []string{
	"monitoring.alert_config.v1",
	"grafana.access_config.v1",
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
		Short:         "security-atlas Grafana monitoring connector",
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
	return "connector:grafana:" + service + "@" + connectorVersion()
}

func sdkOpts() []sdk.Option {
	if common.insecure {
		return []sdk.Option{sdk.WithInsecure()}
	}
	return nil
}

const longDescription = `security-atlas Grafana connector

Emits two evidence kinds:
  - monitoring.alert_config.v1  (run subcommand, pull — Grafana alert-rule +
    contact-point inventory)
  - grafana.access_config.v1    (run subcommand, pull — Grafana SSO + RBAC
    configuration: SSO enabled state, provider types, org-role mapping rules,
    team membership COUNTS, RBAC role-assignment COUNTS)

Profile: pull. Each invocation is one bounded read-and-push pass on an
operator-scheduled cadence (recommended 24h). This is NOT continuous
monitoring — the interval is named honestly.

Least-privilege Grafana access (read-only):
  - the alert-config surface needs a service-account token with the Viewer role.
  - the access-config surface needs, IN ADDITION to Viewer, two read-only
    fixed-role permissions: settings:read (scope settings:auth.*, via
    fixed:settings:reader) to read SSO settings, and roles:read +
    users.roles:read + teams.roles:read (via fixed:roles:reader) to enumerate
    RBAC assignments.
  - NEVER grant Editor or Admin "to be safe" — read-only is sufficient.

The connector reads CONFIGURATION + COUNTS only. For the alert-config surface:
rule title, type, enabled (not paused) state, folder, and the NAME of the
contact point each rule routes to. For the access-config surface: SSO enabled
state, provider types, org-role mapping rule names, team membership COUNTS, and
RBAC role-assignment COUNTS. It NEVER collects a contact point's secret
settings, a SAML private key, an OAuth client secret, an LDAP bind password, a
signing certificate, dashboard JSON, metric time-series, query results, or any
individual user / team-member / role-assignment identity (name / email / login).

Auth: set GRAFANA_URL + GRAFANA_TOKEN. The token is read from the environment,
never a CLI flag, and never logged or placed into an evidence record.`
