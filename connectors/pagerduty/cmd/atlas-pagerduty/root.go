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

	"github.com/mgoodric/security-atlas/connectors/pagerduty/internal/pdrecord"
)

// ConnectorName is logged in `connectors list`.
const ConnectorName = "pagerduty-connector"

// SupportedKinds is the canonical list of evidence kinds the PagerDuty
// connector emits.
//   - pagerduty.oncall_coverage.v1     (escalation-policy + on-call coverage, pull — slice 489)
//   - pagerduty.incident_summary.v1    (bounded-window incident summaries, pull — slice 489)
//   - pagerduty.postmortem_summary.v1  (bounded-window postmortem METADATA, pull — slice 538)
//   - pagerduty.response_metrics.v1    (service-level MTTA/MTTR aggregates, pull — slice 539)
var SupportedKinds = []string{
	pdrecord.OnCallKind,
	pdrecord.IncidentKind,
	pdrecord.PostmortemKind,
	pdrecord.MetricsKind,
}

// PullInterval names the connector's pull cadence HONESTLY (P0-489-6). The
// connector is run-on-a-schedule (cron / scheduler), not "continuous
// monitoring": each invocation is one bounded read-and-push pass.
const PullInterval = "24h (recommended; operator-scheduled — NOT continuous monitoring)"

// ProfilesSupported is what the connector advertises at register time, named
// HONESTLY (P0-540):
//   - pull:      scheduled read-only REST GETs (the `run` subcommand).
//   - subscribe: event-driven PagerDuty v3 incident webhook receipt (the
//     `webhook` subcommand). NOT continuous monitoring; NOT a relabeled poll.
//
// Both describe how the connector retrieves data FROM the source. The platform
// wire is ALWAYS push (invariant #3) regardless of either value.
var ProfilesSupported = []string{"pull", "subscribe"}

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
		Short:         "security-atlas PagerDuty incident-response connector",
		Long:          longDescription,
		SilenceUsage:  true,
		SilenceErrors: true,
	}
	root.PersistentFlags().StringVar(&common.endpoint, "endpoint", "", "platform gRPC endpoint (env: SECURITY_ATLAS_ENDPOINT)")
	root.PersistentFlags().StringVar(&common.token, "token", "", "platform bearer token (env: SECURITY_ATLAS_TOKEN)")
	root.PersistentFlags().BoolVar(&common.insecure, "insecure", false, "disable TLS to platform (loopback endpoints only)")
	root.AddCommand(newRegisterCmd())
	root.AddCommand(newRunCmd())
	root.AddCommand(newWebhookCmd())
	root.AddCommand(newPermissionsCmd())
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

// actorID formats source_attribution.actor_id per the cross-connector
// convention `connector:<vendor>:<service>@<version>`.
func actorID(service string) string {
	return "connector:pagerduty:" + service + "@" + connectorVersion()
}

func sdkOpts() []sdk.Option {
	if common.insecure {
		return []sdk.Option{sdk.WithInsecure()}
	}
	return nil
}

const longDescription = `security-atlas PagerDuty incident-response connector

Emits four evidence kinds:
  - pagerduty.oncall_coverage.v1     (run subcommand, pull — escalation policy + on-call coverage)
  - pagerduty.incident_summary.v1    (run subcommand, pull — bounded-window incident summaries)
  - pagerduty.postmortem_summary.v1  (run subcommand, pull — bounded-window postmortem METADATA)
  - pagerduty.response_metrics.v1    (run subcommand, pull — service-level MTTA/MTTR aggregates)

Profile: pull. Each invocation is one bounded read-and-push pass on an
operator-scheduled cadence (recommended 24h). This is NOT continuous
monitoring — the interval is named honestly.

Least-privilege PagerDuty access (read-only):
  - a READ-ONLY REST API token.
  - NEVER use a full-access / write / admin token.

The connector reads COVERAGE + incident-SUMMARY + postmortem-METADATA +
service-level response-time AGGREGATES only:
  - escalation policies, their tiers, and the on-call IDENTITY (id + display
    name) needed to prove coverage;
  - incident id / number / urgency / status / service / created+resolved
    timestamps over a bounded look-back window;
  - per-postmortem META-FACTS: that a review EXISTS for an incident, its status,
    created+published timestamps, and the corrective-action COUNT + completed/open
    rollup; and
  - per-SERVICE incident-response AGGREGATES: MTTA / MTTR (mean + p50/p90/p95) and
    incident / acknowledged / resolved counts over the window.
It NEVER collects a responder's personal phone number or personal email, the
incident's free-text title/body/notes, the postmortem narrative / timeline /
root-cause prose, an action-item title, or WHICH NAMED RESPONDER acknowledged
or resolved an incident (response metrics are aggregated to the service grain —
they are a program-level posture, never a per-engineer scorecard).

Auth: set PAGERDUTY_TOKEN (read-only). The token is read from the environment,
never a CLI flag, and never logged or placed into an evidence record.`
