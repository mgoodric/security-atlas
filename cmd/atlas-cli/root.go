package main

import (
	"context"
	"crypto/tls"
	"fmt"
	"os"
	"time"

	"github.com/spf13/cobra"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/metadata"
	"google.golang.org/protobuf/encoding/protojson"
	"google.golang.org/protobuf/proto"

	adminv1 "github.com/mgoodric/security-atlas/gen/proto/admin/v1"
	sdk "github.com/mgoodric/security-atlas/pkg/sdk-go"
)

// common holds the persistent connection flags shared by every subcommand.
// Sourced from --endpoint / --token / --insecure or SECURITY_ATLAS_ENDPOINT
// / SECURITY_ATLAS_TOKEN env vars.
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

func newSDKClient() (*sdk.Client, error) {
	opts := []sdk.Option{}
	if common.insecure {
		opts = append(opts, sdk.WithInsecure())
	}
	return sdk.NewClient(common.endpoint, common.token, opts...)
}

func newAdminClient() (adminv1.AdminCredentialsServiceClient, *grpc.ClientConn, error) {
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
	return adminv1.NewAdminCredentialsServiceClient(conn), conn, nil
}

func newAdminContext(timeout time.Duration) (context.Context, context.CancelFunc) {
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	ctx = metadata.AppendToOutgoingContext(ctx, sdk.MetadataAuthorization, sdk.BearerPrefix+common.token)
	return ctx, cancel
}

// runAdminRPC opens an admin client, calls fn under an authenticated context,
// and prints the response as JSON (when non-nil). Used by each credentials
// subcommand so the wiring stays out of the RunE body.
func runAdminRPC(fn func(ctx context.Context, c adminv1.AdminCredentialsServiceClient) (proto.Message, error)) error {
	ctx, cancel := newAdminContext(10 * time.Second)
	defer cancel()

	client, conn, err := newAdminClient()
	if err != nil {
		return err
	}
	defer func() { _ = conn.Close() }()

	resp, err := fn(ctx, client)
	if err != nil {
		return err
	}
	if resp == nil {
		return nil
	}
	return printJSON(resp)
}

func printJSON(m proto.Message) error {
	b, err := protojson.MarshalOptions{Multiline: true, Indent: "  "}.Marshal(m)
	if err != nil {
		return fmt.Errorf("marshal: %w", err)
	}
	fmt.Println(string(b))
	return nil
}

func newRootCmd() *cobra.Command {
	root := &cobra.Command{
		Use:           "security-atlas-cli",
		Short:         "security-atlas CLI",
		Version:       shortVersion(),
		SilenceUsage:  true,
		SilenceErrors: true,
	}
	// Cobra renders --version using the Version field. Override the template
	// so scripts can grep a stable single-line short form. The `version`
	// subcommand (registered below) emits the verbose multi-field form.
	root.SetVersionTemplate("{{.Version}}\n")

	root.PersistentFlags().StringVar(&common.endpoint, "endpoint", "", "gRPC endpoint (env: SECURITY_ATLAS_ENDPOINT)")
	root.PersistentFlags().StringVar(&common.token, "token", "", "bearer token (env: SECURITY_ATLAS_TOKEN)")
	root.PersistentFlags().BoolVar(&common.insecure, "insecure", false, "disable TLS (loopback endpoints only)")

	root.AddCommand(newEvidenceCmd())
	root.AddCommand(newCredentialsCmd())
	root.AddCommand(newCatalogCmd())
	root.AddCommand(newControlsCmd())
	root.AddCommand(newPolicyCmd())
	root.AddCommand(newOscalCmd())
	root.AddCommand(newFeaturesCmd())
	root.AddCommand(newBootstrapCmd())
	root.AddCommand(newVersionCmd())
	return root
}
