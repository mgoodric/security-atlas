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

// ConnectorName is logged in `connectors list`. ConnectorVersion is read at
// build time; falls back to "dev" when the binary isn't tagged.
const ConnectorName = "slack-connector"

// Evidence kinds slice 443 emits — one per Slack evidence surface
// (membership / admin audit-log / retention settings). The `.v1` suffix is
// part of the stable identifier; the schema version is a separate semver
// (EVIDENCE_SDK §4.5).
const (
	KindMember    = "slack.workspace_member.v1"
	KindAuditLog  = "slack.admin_audit_event.v1"
	KindRetention = "slack.retention_settings.v1"
)

// SupportedKinds is the set announced at registration.
var SupportedKinds = []string{KindMember, KindAuditLog, KindRetention}

// PullIntervalNote is the honest interval label for the pull profile (slice
// 443 AC-8 / anti-pattern: no "continuous monitoring"). The connector is run
// on a schedule by the operator's job runner; this is the recommended
// cadence, named honestly — it is NOT event-driven and NOT continuous.
const PullIntervalNote = "pull on a scheduled interval (recommended: daily); not event-driven, not continuous"

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
		Short:         "security-atlas Slack connector",
		SilenceUsage:  true,
		SilenceErrors: true,
	}
	root.PersistentFlags().StringVar(&common.endpoint, "endpoint", "", "platform gRPC endpoint (env: SECURITY_ATLAS_ENDPOINT)")
	root.PersistentFlags().StringVar(&common.token, "token", "", "platform bearer token (env: SECURITY_ATLAS_TOKEN)")
	root.PersistentFlags().BoolVar(&common.insecure, "insecure", false, "disable TLS (loopback endpoints only)")
	root.AddCommand(newRegisterCmd())
	root.AddCommand(newRunCmd())
	return root
}

// dialConnectorRegistry opens a gRPC connection scoped to the
// ConnectorRegistry service. The caller owns the returned conn.
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

// connectorVersion returns the build's module version. Falls back to "dev"
// when not running from a tagged release (e.g., `go run`).
func connectorVersion() string {
	if info, ok := debug.ReadBuildInfo(); ok {
		if info.Main.Version != "" && info.Main.Version != "(devel)" {
			return info.Main.Version
		}
	}
	return "dev"
}

// actorID formats the source_attribution.actor_id per the cross-connector
// convention `connector:<vendor>:<service>@<version>`. service is the Slack
// surface the record came from so the three kinds carry distinct actor ids
// (`connector:slack:members@…`, `connector:slack:auditlogs@…`,
// `connector:slack:retention@…`).
func actorID(service string) string {
	return "connector:slack:" + service + "@" + connectorVersion()
}
