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
const ConnectorName = "manual-connector"

// SupportedKinds is the canonical list of evidence kinds slice 049 emits.
// One kind, three modes — every mode produces manual.upload.v1.
var SupportedKinds = []string{
	"manual.upload.v1",
}

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
		Short:         "security-atlas manual.upload connector (CSV / S3 / SFTP escape hatch)",
		Long:          longDescription,
		SilenceUsage:  true,
		SilenceErrors: true,
	}
	root.PersistentFlags().StringVar(&common.endpoint, "endpoint", "", "platform gRPC endpoint (env: SECURITY_ATLAS_ENDPOINT)")
	root.PersistentFlags().StringVar(&common.token, "token", "", "platform bearer token (env: SECURITY_ATLAS_TOKEN)")
	root.PersistentFlags().BoolVar(&common.insecure, "insecure", false, "disable TLS to platform (loopback endpoints only)")
	root.AddCommand(newRegisterCmd())
	root.AddCommand(newScopesCmd())
	root.AddCommand(newLocalCmd())
	root.AddCommand(newS3Cmd())
	root.AddCommand(newSFTPCmd())
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
// when not running from a tagged release.
func connectorVersion() string {
	if info, ok := debug.ReadBuildInfo(); ok {
		if info.Main.Version != "" && info.Main.Version != "(devel)" {
			return info.Main.Version
		}
	}
	return "dev"
}

// actorID formats the source_attribution.actor_id per the convention
// `connector:manual:<service>@<version>`. service is one of "local",
// "s3", or "sftp" so an analyst can tell at a glance which transport
// delivered the record.
func actorID(service string) string {
	return "connector:manual:" + service + "@" + connectorVersion()
}

func sdkOpts() []sdk.Option {
	if common.insecure {
		return []sdk.Option{sdk.WithInsecure()}
	}
	return nil
}

const longDescription = `security-atlas manual.upload connector

Three transports, one evidence kind (manual.upload.v1):

  local --file <path>          parse CSV; one record per row
  s3 --bucket <b> --prefix <p> list S3 prefix; one record per object
  sftp --host <h> --user <u> --path <glob> --known-hosts <p> --key-file <p>
                              pull SFTP files; one record per file

Auth posture (use 'scopes' subcommand for the full table):
  local : no auth — operator owns the file system
  s3    : standard AWS credential chain (env / profile / IRSA / IMDS)
  sftp  : SSH key from --key-file (never a flag), known_hosts mandatory.
          InsecureIgnoreHostKey is rejected at config build time.

CSV parser caps (DoS guards):
  --max-rows         100000 default
  --max-field-bytes  1048576 (1 MiB) default

Idempotency key shapes (all sha256 hex):
  local : "manual.upload|" + file_path + "|" + row_index + "|" + hour
  s3    : "manual.upload|" + bucket    + "|" + key       + "|" + etag
  sftp  : "manual.upload|" + host      + "|" + path      + "|" + mtime
`
