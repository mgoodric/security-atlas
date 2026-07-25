package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/spf13/cobra"

	evidencev1 "github.com/mgoodric/security-atlas/gen/proto/evidence/v1"
	sdk "github.com/mgoodric/security-atlas/pkg/sdk-go"

	"github.com/mgoodric/security-atlas/connectors/pagerduty/internal/pagerdutyauth"
	"github.com/mgoodric/security-atlas/connectors/pagerduty/internal/webhook"
)

// Webhook seams: the receiver wiring reaches through these function variables so
// tests can swap in fakes for the sdk client constructor and the blocking Serve
// loop without binding a real socket or hitting a real platform. Production code
// paths are unchanged; only the call-site indirection moved.
var (
	newWebhookSDKClient = func(endpoint, bearer string, opts ...sdk.Option) (sdkPushClient, error) {
		return sdk.NewClient(endpoint, bearer, opts...)
	}
	webhookServe = webhook.Serve
)

type webhookFlags struct {
	environment     string
	service         string
	incidentControl string
	listen          string
	path            string
}

func newWebhookCmd() *cobra.Command {
	var f webhookFlags
	cmd := &cobra.Command{
		Use:   "webhook",
		Short: "run the PagerDuty event-driven (subscribe) webhook receiver",
		Long: `Run the source-side PagerDuty v3 webhook receiver: a long-lived HTTP
server (inside this connector process) that receives PagerDuty incident-lifecycle
webhook deliveries (incident.triggered / acknowledged / resolved / escalated /
...), VERIFIES the X-PagerDuty-Signature HMAC before doing any work, and pushes
the SAME pagerduty.incident_summary.v1 record the pull profile emits.

Profile: subscribe (event-driven via the PagerDuty v3 webhook). This is NOT continuous monitoring
and not a relabeled poll — the connector receives a webhook PagerDuty POSTs to
this process as incidents happen. The platform-side wire is still push
(invariant #3): this receiver is part of the CONNECTOR, not a new inbound
platform API.

Security (STRIDE Spoofing, DOMINANT): anyone can POST to a public webhook
endpoint, so the signature is verified BEFORE any record is built or pushed. An
unsigned, forged, or wrong-signature delivery is rejected 401 and never produces a
record. The body is size-bounded so a hostile POST cannot exhaust memory.

Dedup: a webhook-emitted record and a subsequent pull-emitted record for the SAME
incident within the SAME UTC hour collapse to one ledger row (the slice 489
idempotency key, reused unchanged).

Auth: set PAGERDUTY_WEBHOOK_SECRET to the per-subscription signing secret. It is
read from the environment (never a flag), and is never logged or placed into an
evidence record. No PagerDuty REST token is needed for this profile — the
verified webhook payload carries the incident SUMMARY fields directly.

Bind: defaults to loopback (127.0.0.1). Terminate TLS at a reverse proxy in front
of this process and forward the verbatim request body (the signature is over the
raw body).`,
		SilenceErrors: true,
		SilenceUsage:  true,
		PreRunE: func(_ *cobra.Command, _ []string) error {
			if f.environment == "" {
				return errors.New("--environment is required (records must be scoped)")
			}
			return resolveCommon()
		},
		RunE: func(_ *cobra.Command, _ []string) error {
			return doWebhook(signalContext(), f)
		},
	}
	cmd.Flags().StringVar(&f.environment, "environment", "", "environment scope tag [required]")
	cmd.Flags().StringVar(&f.service, "service", "pagerduty", "service scope tag")
	cmd.Flags().StringVar(&f.incidentControl, "incident-control", "scf:IRO-02", "control_id to attach to pagerduty.incident_summary.v1 records")
	cmd.Flags().StringVar(&f.listen, "listen", "127.0.0.1:8474", "address to bind the webhook receiver (loopback default; terminate TLS at a reverse proxy)")
	cmd.Flags().StringVar(&f.path, "path", "/webhooks/pagerduty", "URL path the receiver listens on")
	return cmd
}

func doWebhook(ctx context.Context, f webhookFlags) error {
	secret, err := pagerdutyauth.ResolveWebhookSecret("")
	if err != nil {
		return fmt.Errorf("auth: %w", err)
	}

	sdkClient, err := newWebhookSDKClient(common.endpoint, common.token, sdkOpts()...)
	if err != nil {
		return fmt.Errorf("sdk client: %w", err)
	}
	defer func() { _ = sdkClient.Close() }()

	rec, err := webhook.NewReceiver(webhook.Config{
		Verifier:    webhook.NewHMACVerifier(secret),
		Pusher:      pushAdapter{sdkClient},
		ControlID:   f.incidentControl,
		ActorID:     actorID("incidents"),
		Service:     f.service,
		Environment: f.environment,
		Now:         func() time.Time { return nowFn().UTC() },
	})
	if err != nil {
		return fmt.Errorf("receiver: %w", err)
	}

	srv := webhook.NewServer(f.listen, f.path, rec)
	fmt.Printf("pagerduty webhook receiver listening (profile=subscribe addr=%s path=%s environment=%s) — NOT continuous monitoring\n",
		f.listen, f.path, f.environment)
	if err := webhookServe(ctx, srv); err != nil {
		return fmt.Errorf("serve: %w", err)
	}
	return nil
}

// signalContext returns a context cancelled on SIGINT / SIGTERM so the long-lived
// receiver drains gracefully on the operator's stop signal (mirrors the rippling /
// bamboohr subscribe receivers).
func signalContext() context.Context {
	ctx, _ := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	return ctx
}

// pushAdapter narrows the seam's sdkPushClient (which also has Close) to the
// receiver's Pusher surface (Push only).
type pushAdapter struct{ c sdkPushClient }

func (p pushAdapter) Push(ctx context.Context, rec *evidencev1.EvidenceRecord) (*evidencev1.EvidenceReceipt, error) {
	return p.c.Push(ctx, rec)
}

// ensure pushAdapter satisfies the receiver's Pusher at compile time.
var _ webhook.Pusher = pushAdapter{}
