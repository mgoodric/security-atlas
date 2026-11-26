package main

import (
	"context"
	"fmt"
	"os"
	"runtime/debug"
	"time"

	"github.com/spf13/cobra"
	"google.golang.org/grpc"
	"google.golang.org/grpc/metadata"

	connectorsv1 "github.com/mgoodric/security-atlas/gen/proto/connectors/v1"
	sdk "github.com/mgoodric/security-atlas/pkg/sdk-go"
)

// ConnectorName is the canonical name surfaced via the
// ConnectorRegistry. Slice 044 uses `<vendor>-connector`; we match.
const ConnectorName = "1password-connector"

// SupportedKinds is the canonical list of evidence kinds the connector
// emits. One kind, pulled (canvas §4.2: 1Password is Query-only). #2968
// moved it to v2, read from the documented Events and Users APIs.
var SupportedKinds = []string{
	"1password.org_policy.v2",
}

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
		return fmt.Errorf("--platform-token or SECURITY_ATLAS_TOKEN is required")
	}
	return nil
}

func newRootCmd() *cobra.Command {
	root := &cobra.Command{
		Use:           ConnectorName,
		Short:         "security-atlas 1Password connector",
		Long:          longDescription,
		SilenceUsage:  true,
		SilenceErrors: true,
	}
	root.PersistentFlags().StringVar(&common.endpoint, "endpoint", "", "platform gRPC endpoint (env: SECURITY_ATLAS_ENDPOINT)")
	// The flag is named `--platform-token` so it is never mistaken for a
	// 1Password secret, which the connector reads from the environment
	// only (#2968).
	root.PersistentFlags().StringVar(&common.token, "platform-token", "", "platform bearer token (env: SECURITY_ATLAS_TOKEN)")
	root.PersistentFlags().BoolVar(&common.insecure, "insecure", false, "disable TLS to platform (loopback endpoints only)")
	root.AddCommand(newRegisterCmd())
	root.AddCommand(newRunCmd())
	root.AddCommand(newScopesCmd())
	return root
}

// dialConnectorRegistry opens a gRPC connection scoped to the
// ConnectorRegistry service. The caller owns the returned conn.
func dialConnectorRegistry() (connectorsv1.ConnectorRegistryServiceClient, *grpc.ClientConn, error) {
	// #2257: sdk.Dial applies the same loopback-only rule to --insecure as
	// the push path, so the registry dial cannot go plaintext off-box.
	conn, err := sdk.Dial(common.endpoint, sdkOpts()...)
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

// connectorVersion returns the build's module version. Falls back to
// "dev" when not running from a tagged release.
func connectorVersion() string {
	if info, ok := debug.ReadBuildInfo(); ok {
		if info.Main.Version != "" && info.Main.Version != "(devel)" {
			return info.Main.Version
		}
	}
	return "dev"
}

// actorID formats source_attribution.actor_id per the convention
// `connector:<vendor>:<service>@<version>` so every connector across
// the platform follows the same shape.
func actorID(service string) string {
	return "connector:1password:" + service + "@" + connectorVersion()
}

func sdkOpts() []sdk.Option {
	if common.insecure {
		return []sdk.Option{sdk.WithInsecure()}
	}
	return nil
}

const longDescription = `security-atlas 1Password connector

Emits one evidence kind:
  - 1password.org_policy.v2 (run subcommand, pull)

It reads two documented 1Password APIs, each with its own credential,
both from the environment only (no flag carries a 1Password secret):
  - Events API, GET /api/v2/auth/introspect, with an Events Reporting
    integration's bearer token in $ONEPASSWORD_EVENTS_TOKEN. Grant the
    integration the audit events feature.
  - Users API, GET /v1/accounts/{account}/users, with an OAuth
    application's client credentials in $ONEPASSWORD_USERS_CLIENT_ID and
    $ONEPASSWORD_USERS_CLIENT_SECRET. Grant only users.view.

Use the 'scopes' subcommand to print the canonical list.`
