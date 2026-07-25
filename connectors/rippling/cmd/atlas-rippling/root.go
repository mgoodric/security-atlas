package main

import (
	"context"
	"crypto/tls"
	"fmt"
	"os"
	"os/signal"
	"runtime/debug"
	"syscall"
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
const ConnectorName = "rippling-connector"

// SupportedKinds is the canonical list of evidence kinds the Rippling connector
// emits.
//   - hris.worker_lifecycle.v1 (worker roster + employment status, pull — slice 491)
//   - hris.manager_hierarchy.v1 (reporting tree derived from the roster — slice 571)
var SupportedKinds = []string{
	"hris.worker_lifecycle.v1",
	"hris.manager_hierarchy.v1",
}

// PullInterval names the connector's pull cadence HONESTLY (P0-491-6). The
// connector is run-on-a-schedule (cron / scheduler), not "continuous
// monitoring": each invocation is one bounded read-and-push pass.
const PullInterval = "24h (recommended; operator-scheduled — NOT continuous monitoring)"

// ProfilesSupported is how the connector retrieves data FROM Rippling: a
// scheduled read-only poll (pull) and an event-driven webhook the connector
// receives source-side (subscribe, slice 573). The platform-side wire is ALWAYS
// push (invariant #3).
var ProfilesSupported = []string{"pull", "subscribe"}

// SubscribeMechanism names the event-driven profile HONESTLY (P0-491-6 /
// slice 573): it is a real-time leaver signal driven by the Rippling webhook,
// NOT "continuous monitoring" and NOT a platform inbound API.
const SubscribeMechanism = "event-driven via the Rippling termination/status-change webhook (source-side receiver; NOT continuous monitoring)"

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
		Short:         "security-atlas Rippling HRIS worker-lifecycle connector",
		Long:          longDescription,
		SilenceUsage:  true,
		SilenceErrors: true,
	}
	root.PersistentFlags().StringVar(&common.endpoint, "endpoint", "", "platform gRPC endpoint (env: SECURITY_ATLAS_ENDPOINT)")
	root.PersistentFlags().StringVar(&common.token, "token", "", "platform bearer token (env: SECURITY_ATLAS_TOKEN)")
	root.PersistentFlags().BoolVar(&common.insecure, "insecure", false, "disable TLS to platform (loopback endpoints only)")
	root.AddCommand(newRegisterCmd())
	root.AddCommand(newRunCmd())
	root.AddCommand(newSubscribeCmd())
	root.AddCommand(newPermissionsCmd())
	return root
}

// commandContext returns a context cancelled on SIGINT / SIGTERM, so the
// long-lived `subscribe` receiver drains gracefully on Ctrl-C or a container
// stop signal (slice 573).
func commandContext() context.Context {
	ctx, _ := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	return ctx
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
	return "connector:rippling:" + service + "@" + connectorVersion()
}

func sdkOpts() []sdk.Option {
	if common.insecure {
		return []sdk.Option{sdk.WithInsecure()}
	}
	return nil
}

const longDescription = `security-atlas Rippling HRIS worker-lifecycle connector

Emits one evidence kind:
  - hris.worker_lifecycle.v1  (run subcommand, pull — worker roster + employment status)

Profile: pull. Each invocation is one bounded read-and-push pass on an
operator-scheduled cadence (recommended 24h). This is NOT continuous
monitoring — the interval is named honestly.

Least-privilege Rippling access (read-only):
  - an API token scoped to the read-only employee-directory / worker-lifecycle
    field group ONLY (roster + employment status).
  - NEVER grant a full-PII read group (compensation, SSN, bank, benefits) or any
    WRITE scope. The HRIS holds the most sensitive PII in the customer's stack;
    a broad scope risks over-collecting it into the append-only evidence ledger.

The connector reads WORKER-LIFECYCLE facts only — worker id, employment status
(active / terminated / on-leave / pending), start date, end/termination date,
title, department, the manager ASSIGNMENT id, and the work email (the only
contact field, needed for the access-review join). It NEVER collects SSN /
national id, compensation / salary, home address, bank / payment details,
benefits / health, performance-review fields, date of birth, personal phone, or
protected-class data.

Auth: set RIPPLING_API_TOKEN (and optionally RIPPLING_BASE_URL). The token is
read from the environment, never a CLI flag, and never logged or placed into an
evidence record.`
