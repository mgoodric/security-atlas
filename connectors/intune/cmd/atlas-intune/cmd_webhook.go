package main

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"os"
	"os/signal"
	"syscall"

	"github.com/spf13/cobra"

	evidencev1 "github.com/mgoodric/security-atlas/gen/proto/evidence/v1"
	sdk "github.com/mgoodric/security-atlas/pkg/sdk-go"

	"github.com/mgoodric/security-atlas/connectors/intune/internal/devices"
	"github.com/mgoodric/security-atlas/connectors/intune/internal/intuneauth"
	"github.com/mgoodric/security-atlas/connectors/mdm/devposture"
	"github.com/mgoodric/security-atlas/connectors/mdm/mdmwebhook"
)

// Webhook seams: the receiver wiring reaches through these function variables so
// tests can swap in fakes for the sdk client constructor and the blocking Serve
// loop without binding a real socket or hitting a real platform.
var (
	newWebhookSDKClient = func(endpoint, bearer string, opts ...sdk.Option) (sdkPushClient, error) {
		return sdk.NewClient(endpoint, bearer, opts...)
	}
	webhookServe = mdmwebhook.Serve
)

// maxValidationTokenLen bounds the echoed validationToken (Graph tokens are short
// opaque strings; this caps a hostile query param so the echo path cannot be
// abused to reflect a large body).
const maxValidationTokenLen = 2048

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
		Short: "run the Intune event-driven (subscribe) Graph change-notification receiver",
		Long: `Run the source-side Microsoft Graph change-notification receiver: a
long-lived HTTP server (inside this connector process) that receives Graph
managed-device change notifications, VERIFIES the per-subscription clientState
before doing any work, and pushes the SAME endpoint.device_posture.v1 record the
pull profile emits — so a device's posture evidence refreshes near-real-time on a
compliance-state change.

Profile: subscribe (event-driven via Graph change notifications). This is NOT
continuous monitoring and not a relabeled poll. The platform-side wire is still
push (invariant #3): this receiver is part of the CONNECTOR, not a new inbound
platform API.

Graph validation handshake: when Graph creates or renews the subscription it
sends a request carrying a 'validationToken' query parameter; the receiver MUST
respond 200 with the token echoed as text/plain and MUST NOT process it as a
delivery. This receiver handles that handshake FIRST and builds no record for it.

Security (STRIDE Spoofing, DOMINANT): anyone can POST to a public notification
endpoint, so each delivery's clientState is verified (constant-time) against the
configured per-subscription secret BEFORE any record is built or pushed. A
delivery with a missing/forged clientState is rejected 401 and never produces a
record. The body is size-bounded so a hostile POST cannot exhaust memory (413).

Over-collection: the receiver emits the SAME posture-summary field set as the
pull profile. The change notification's resourceData is mapped directly; the
connector never reads beyond the posture-summary.

Dedup: a webhook-emitted record and a subsequent pull-emitted record for the SAME
device within the SAME UTC hour collapse to one ledger row (the slice 490
idempotency key, reused unchanged).

Auth: Graph does NOT HMAC-sign notification bodies. Set
INTUNE_WEBHOOK_CLIENT_STATE to the per-subscription clientState you set when
creating the subscription. It is read from the environment (never a flag), never
logged, never placed into an evidence record.

Bind: defaults to loopback (127.0.0.1). Graph requires an HTTPS endpoint with a
valid certificate — terminate TLS at a reverse proxy in front of this process.`,
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
	cmd.Flags().StringVar(&f.listen, "listen", "127.0.0.1:8477", "address to bind the receiver (loopback default; terminate TLS at a reverse proxy)")
	cmd.Flags().StringVar(&f.path, "path", "/webhooks/intune", "URL path the receiver listens on")
	return cmd
}

func doWebhook(ctx context.Context, f webhookFlags) error {
	clientState, err := intuneauth.ResolveClientState("")
	if err != nil {
		return fmt.Errorf("auth: %w", err)
	}

	sdkClient, err := newWebhookSDKClient(common.endpoint, common.token, sdkOpts()...)
	if err != nil {
		return fmt.Errorf("sdk client: %w", err)
	}
	defer func() { _ = sdkClient.Close() }()

	rec, err := mdmwebhook.NewReceiver(mdmwebhook.Config{
		SourceMDM:   devposture.MDMIntune,
		Verifier:    mdmwebhook.NewClientStateVerifier(clientState, devices.ExtractClientState),
		Parser:      devices.ParseChangeNotification,
		Pusher:      pushAdapter{sdkClient},
		ControlID:   f.deviceControl,
		ActorID:     actorID("devices"),
		Service:     "intune",
		Environment: f.environment,
	})
	if err != nil {
		return fmt.Errorf("receiver: %w", err)
	}

	// The validation-handshake handler wraps the receiver and OWNS the non-record
	// validationToken path BEFORE delegating a real delivery to the shared
	// verify-first skeleton (D-INTUNE-1).
	srv := mdmwebhook.NewServer(f.listen, f.path, validationHandler{rec})
	fmt.Printf("intune webhook receiver listening (profile=subscribe addr=%s path=%s environment=%s) — NOT continuous monitoring\n",
		f.listen, f.path, f.environment)
	if err := webhookServe(ctx, srv); err != nil {
		return fmt.Errorf("serve: %w", err)
	}
	return nil
}

// validationHandler is the Intune ADAPTER that owns the Microsoft Graph
// subscription-validation handshake. Graph sends a request carrying a
// `validationToken` query parameter when it creates or renews a subscription; the
// receiver MUST respond 200 with the token echoed verbatim as text/plain within
// ~10s and MUST NOT process it as a delivery (no clientState verification, no
// record). This handler intercepts that case FIRST, then delegates every real
// delivery to the shared verify-first skeleton (the wrapped mdmwebhook.Receiver),
// which verifies clientState BEFORE building any record. Keeping the handshake in
// the vendor adapter (not the shared package) honors the slice-557 directive to
// avoid bending the shared seam for one vendor's special path.
type validationHandler struct {
	inner http.Handler
}

func (h validationHandler) ServeHTTP(w http.ResponseWriter, req *http.Request) {
	if token := req.URL.Query().Get("validationToken"); token != "" {
		// Bound the echoed token (defensive: a hostile caller cannot make us
		// reflect an unbounded body) and respond plain-text per the Graph contract.
		if len(token) > maxValidationTokenLen {
			token = token[:maxValidationTokenLen]
		}
		w.Header().Set("Content-Type", "text/plain")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(token))
		return
	}
	h.inner.ServeHTTP(w, req)
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
