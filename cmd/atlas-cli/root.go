package main

import (
	"context"
	"fmt"
	"io"
	"os"
	"time"

	"github.com/spf13/cobra"
	"google.golang.org/grpc"
	"google.golang.org/grpc/metadata"
	"google.golang.org/protobuf/encoding/protojson"
	"google.golang.org/protobuf/proto"

	"github.com/mgoodric/security-atlas/cmd/atlas-cli/cmdhttp"
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
	// allowInsecureHTTP is --allow-insecure-http: the opt-out from the
	// REST commands' refusal to send to a non-loopback http:// URL.
	allowInsecureHTTP bool
}

// applyAllowInsecureHTTP hands --allow-insecure-http to cmdhttp, whose
// transport guard every REST call goes through, and warns when it is set
// (#2257).
func applyAllowInsecureHTTP(w io.Writer) {
	cmdhttp.SetAllowInsecureHTTP(common.allowInsecureHTTP)
	if common.allowInsecureHTTP {
		_, _ = fmt.Fprintln(w, "WARNING: --allow-insecure-http is set: REST requests to a non-loopback http:// URL are sent unencrypted, bearer token included")
	}
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
	// #2165: --insecure is loopback-only. The admin client dials through
	// sdk.Dial, the same dial and WithInsecure guard as the push path.
	if common.insecure && !sdk.IsLoopbackEndpoint(common.endpoint) {
		return nil, nil, fmt.Errorf("--insecure refuses non-loopback endpoint %q (allowed: localhost, 127.0.0.0/8, ::1)", common.endpoint)
	}
	opts := []sdk.Option{}
	if common.insecure {
		opts = append(opts, sdk.WithInsecure())
	}
	conn, err := sdk.Dial(common.endpoint, opts...)
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
	root.PersistentFlags().BoolVar(&common.allowInsecureHTTP, "allow-insecure-http", false,
		"send REST requests, and the bearer they carry, over plain http:// to a non-loopback host (unsafe)")
	root.PersistentPreRun = func(cmd *cobra.Command, _ []string) {
		applyAllowInsecureHTTP(cmd.ErrOrStderr())
	}

	root.AddCommand(newEvidenceCmd())
	root.AddCommand(newCredentialsCmd())
	root.AddCommand(newCatalogCmd())
	root.AddCommand(newControlsCmd())
	root.AddCommand(newPolicyCmd())
	root.AddCommand(newOscalCmd())
	root.AddCommand(newOscalSignCmd())
	root.AddCommand(newFeaturesCmd())
	root.AddCommand(newTaxonomyCmd())
	root.AddCommand(newMappingsCmd())
	root.AddCommand(newBootstrapCmd())
	root.AddCommand(newOAuthCmd())
	root.AddCommand(newKeysCmd())
	root.AddCommand(newLoginCmd())
	root.AddCommand(newDemoCmd())
	root.AddCommand(newVersionCmd())
	return root
}
