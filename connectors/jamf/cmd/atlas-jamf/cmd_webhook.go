package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/signal"
	"syscall"

	"github.com/spf13/cobra"

	evidencev1 "github.com/mgoodric/security-atlas/gen/proto/evidence/v1"
	sdk "github.com/mgoodric/security-atlas/pkg/sdk-go"

	"github.com/mgoodric/security-atlas/connectors/jamf/internal/devices"
	"github.com/mgoodric/security-atlas/connectors/jamf/internal/jamfauth"
	"github.com/mgoodric/security-atlas/connectors/mdm/devposture"
	"github.com/mgoodric/security-atlas/connectors/mdm/mdmwebhook"
)

// Webhook seams: the receiver wiring reaches through these function variables so
// tests can swap in fakes for the sdk client constructor and the blocking Serve
// loop without binding a real socket or hitting a real platform. Production code
// paths are unchanged; only the call-site indirection moved.
var (
	newWebhookSDKClient = func(endpoint, bearer string, opts ...sdk.Option) (sdkPushClient, error) {
		return sdk.NewClient(endpoint, bearer, opts...)
	}
	webhookServe = mdmwebhook.Serve
)

type webhookFlags struct {
	environment   string
	deviceControl string
	listen        string
	path          string
}

func newWebhookCmd() *cobra.Command {
	var f webhookFlags
	cmd := &cobra.Command{
		Use:   "webhook",
		Short: "run the Jamf event-driven (subscribe) webhook receiver",
		Long: `Run the source-side Jamf Pro webhook receiver: a long-lived HTTP server
(inside this connector process) that receives Jamf computer-lifecycle webhook
deliveries (ComputerCheckIn / ComputerInventoryCompleted / ...), VERIFIES the
operator-configured shared secret before doing any work, and pushes the SAME
endpoint.device_posture.v1 record the pull profile emits — so a device's posture
evidence refreshes near-real-time on a compliance-state change.

Profile: subscribe (event-driven via the Jamf webhook). This is NOT continuous
monitoring and not a relabeled poll — the connector receives a webhook Jamf POSTs
to this process as devices check in. The platform-side wire is still push
(invariant #3): this receiver is part of the CONNECTOR, not a new inbound
platform API.

Security (STRIDE Spoofing, DOMINANT): anyone can POST to a public webhook
endpoint, so the operator-configured shared secret is verified (constant-time)
BEFORE any record is built or pushed. An unauthenticated, forged, or
wrong-credential delivery is rejected 401 and never produces a record. The body
is size-bounded so a hostile POST cannot exhaust memory (413).

Over-collection: the receiver emits the SAME posture-summary field set as the
pull profile (disk-encryption, screen-lock, managed/enrolled, compliance, OS
version, and the device->owner ASSIGNMENT identity) — never device geolocation,
installed-app inventory, device contents, or owner personal contact detail. The
webhook payload is mapped directly; the connector never reads beyond the
posture-summary.

Dedup: a webhook-emitted record and a subsequent pull-emitted record for the SAME
device within the SAME UTC hour collapse to one ledger row (the slice 490
idempotency key, reused unchanged).

Auth: Jamf Pro does NOT HMAC-sign webhook bodies. Set JAMF_WEBHOOK_SECRET to the
shared secret you configure on the Jamf webhook (a custom header value); set
JAMF_WEBHOOK_HEADER to override the header name (default X-Jamf-Webhook-Secret).
The secret is read from the environment (never a flag), never logged, never placed
into an evidence record. No Jamf REST token is needed for this profile — the
verified webhook payload carries the device posture fields directly.

Bind: defaults to loopback (127.0.0.1). Terminate TLS at a reverse proxy in front
of this process.`,
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
	cmd.Flags().StringVar(&f.deviceControl, "device-control", "scf:END-04", "control_id to attach to endpoint.device_posture.v1 records")
	cmd.Flags().StringVar(&f.listen, "listen", "127.0.0.1:8476", "address to bind the webhook receiver (loopback default; terminate TLS at a reverse proxy)")
	cmd.Flags().StringVar(&f.path, "path", "/webhooks/jamf", "URL path the receiver listens on")
	return cmd
}

func doWebhook(ctx context.Context, f webhookFlags) error {
	secret, err := jamfauth.ResolveWebhookSecret("")
	if err != nil {
		return fmt.Errorf("auth: %w", err)
	}
	header := jamfauth.WebhookHeader("")

	sdkClient, err := newWebhookSDKClient(common.endpoint, common.token, sdkOpts()...)
	if err != nil {
		return fmt.Errorf("sdk client: %w", err)
	}
	defer func() { _ = sdkClient.Close() }()

	rec, err := mdmwebhook.NewReceiver(mdmwebhook.Config{
		SourceMDM:   devposture.MDMJamf,
		Verifier:    mdmwebhook.NewSharedSecretVerifier(header, secret),
		Parser:      devices.ParseWebhookEvent,
		Pusher:      pushAdapter{sdkClient},
		ControlID:   f.deviceControl,
		ActorID:     actorID("devices"),
		Service:     "jamf",
		Environment: f.environment,
	})
	if err != nil {
		return fmt.Errorf("receiver: %w", err)
	}

	srv := mdmwebhook.NewServer(f.listen, f.path, rec)
	fmt.Printf("jamf webhook receiver listening (profile=subscribe addr=%s path=%s environment=%s) — NOT continuous monitoring\n",
		f.listen, f.path, f.environment)
	if err := webhookServe(ctx, srv); err != nil {
		return fmt.Errorf("serve: %w", err)
	}
	return nil
}

// signalContext returns a context cancelled on SIGINT / SIGTERM so the long-lived
// receiver drains gracefully on the operator's stop signal.
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
var _ mdmwebhook.Pusher = pushAdapter{}
