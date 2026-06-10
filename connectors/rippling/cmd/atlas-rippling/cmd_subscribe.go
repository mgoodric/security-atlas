package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/mgoodric/security-atlas/connectors/hris/webhook"
	"github.com/mgoodric/security-atlas/connectors/hris/worker"
	"github.com/mgoodric/security-atlas/connectors/rippling/internal/ripplingauth"
	"github.com/mgoodric/security-atlas/connectors/rippling/internal/workers"
)

// Rippling webhook signature scheme (slice 573 — see decisions log D1):
// Rippling signs each webhook delivery with HMAC-SHA256 over the raw request
// body, keyed by the per-subscription signing secret, hex-encoded in the
// X-Rippling-Signature header. The receiver recomputes that HMAC and compares in
// constant time BEFORE any record is built.
const ripplingSigHeader = "X-Rippling-Signature"

// runSubscribe is seamed so the seam test can drive the long-lived receiver
// without binding a real port for the whole command. doSubscribe builds the
// receiver and delegates to it.
var serveReceiver = webhook.Serve

// ripplingParser extracts the triggered worker id from a Rippling webhook
// envelope. Rippling termination/status-change webhooks carry the affected
// worker's id; the rest of the payload is a trigger only — the authoritative
// lifecycle facts come from the bounded re-read.
type ripplingParser struct{}

// ripplingWebhookEnvelope is the minimal Rippling webhook shape the connector
// reads: the event type and the affected worker id. Every other field is
// discarded (the envelope is a trigger, not the record source).
type ripplingWebhookEnvelope struct {
	Event string `json:"event"`
	Data  struct {
		EmployeeID string `json:"employeeId"`
		ID         string `json:"id"`
	} `json:"data"`
}

// ParseWorkerIDs returns the single affected worker as a one-element slice (or an
// empty slice for an unrelated event). The Rippling envelope is single-worker, so
// the shared receiver's fan-out loop is a no-op here — Rippling behavior is
// identical to the pre-fan-out single-worker path (slice 655: Rippling stays
// single).
func (ripplingParser) ParseWorkerIDs(body []byte) ([]string, error) {
	var env ripplingWebhookEnvelope
	if err := json.Unmarshal(body, &env); err != nil {
		return nil, fmt.Errorf("parse rippling webhook: %w", err)
	}
	id := strings.TrimSpace(env.Data.EmployeeID)
	if id == "" {
		id = strings.TrimSpace(env.Data.ID)
	}
	if id == "" {
		return nil, nil
	}
	return []string{id}, nil
}

// ripplingFetcher adapts the read-only single-worker client to the receiver's
// WorkerFetcher: the SAME minimal-field read-only client the pull profile uses.
type ripplingFetcher struct {
	api workers.OneAPI
}

func (f ripplingFetcher) FetchWorker(ctx context.Context, workerID string) (worker.RawWorker, bool, error) {
	return workers.FetchOne(ctx, f.api, workerID)
}

type subscribeFlags struct {
	environment   string
	workerControl string
	baseURL       string
	listenAddr    string
	path          string
}

func newSubscribeCmd() *cobra.Command {
	var f subscribeFlags
	cmd := &cobra.Command{
		Use:   "subscribe",
		Short: "run the source-side Rippling termination-webhook receiver (event-driven)",
		Long: `Run a long-lived SOURCE-SIDE webhook receiver IN THIS CONNECTOR PROCESS that
receives Rippling termination / status-change webhook deliveries, verifies the
per-subscription HMAC-SHA256 signature (X-Rippling-Signature) BEFORE processing,
re-reads the affected worker's MINIMAL lifecycle fields via the read-only
Rippling API, builds the SAME hris.worker_lifecycle.v1 record, and pushes it.

Profile: subscribe (event-driven via the Rippling webhook). This is NOT
continuous monitoring and NOT a platform inbound API — the platform-side wire
stays push (invariant #3). The webhook is a TRIGGER; the connector re-reads the
worker's minimal lifecycle fields to build the record, never reading beyond the
allowed field set.

Dedup: a webhook-emitted record and a pull-emitted record for the same worker in
the same UTC hour collapse to one ledger row (shared idempotency key).

Auth: set RIPPLING_API_TOKEN (read-only worker-lifecycle scope) and
RIPPLING_WEBHOOK_SECRET (the per-subscription signing secret). Neither appears in
a log line or an evidence record.`,
		SilenceErrors: true,
		SilenceUsage:  true,
		PreRunE: func(_ *cobra.Command, _ []string) error {
			if f.environment == "" {
				return errors.New("--environment is required (records must be scoped)")
			}
			return resolveCommon()
		},
		RunE: func(_ *cobra.Command, _ []string) error {
			return doSubscribe(commandContext(), f)
		},
	}
	cmd.Flags().StringVar(&f.environment, "environment", "", "environment scope tag [required]")
	cmd.Flags().StringVar(&f.workerControl, "worker-control", "scf:IAC-22", "control_id to attach to hris.worker_lifecycle.v1 records")
	cmd.Flags().StringVar(&f.baseURL, "base-url", "", "Rippling API base URL override (env: RIPPLING_BASE_URL)")
	cmd.Flags().StringVar(&f.listenAddr, "listen", "127.0.0.1:8533", "address the webhook receiver binds (loopback by default; front with a reverse proxy for TLS)")
	cmd.Flags().StringVar(&f.path, "path", "/hooks/rippling", "URL path the receiver serves")
	return cmd
}

func doSubscribe(ctx context.Context, f subscribeFlags) error {
	cred, err := ripplingauth.Resolve(ripplingauth.ResolveOpts{BaseURL: f.baseURL})
	if err != nil {
		return fmt.Errorf("auth: %w", err)
	}
	secret := strings.TrimSpace(os.Getenv(ripplingauth.EnvWebhookSecret))
	if secret == "" {
		return fmt.Errorf("webhook secret required: set %s (the per-subscription signing secret)", ripplingauth.EnvWebhookSecret)
	}

	sdkClient, err := newSDKClient(common.endpoint, common.token, sdkOpts()...)
	if err != nil {
		return fmt.Errorf("sdk client: %w", err)
	}
	defer func() { _ = sdkClient.Close() }()

	httpClient := &http.Client{Timeout: 30 * time.Second}
	oneAPI := newWorkersOneAPI(httpClient, cred.BaseURL(), cred.APIToken())

	rec, err := webhook.NewReceiver(webhook.Config{
		Vendor:      worker.HRISRippling,
		Verifier:    webhook.NewHMACVerifier(worker.HRISRippling, secret, ripplingSigHeader, "", webhook.EncodingHex),
		Parser:      ripplingParser{},
		Fetcher:     ripplingFetcher{api: oneAPI},
		Pusher:      sdkClient,
		ControlID:   f.workerControl,
		ActorID:     actorID("webhook"),
		Environment: f.environment,
	})
	if err != nil {
		return fmt.Errorf("receiver: %w", err)
	}

	srv := webhook.NewServer(f.listenAddr, f.path, rec)
	fmt.Printf("rippling termination-webhook receiver listening on %s%s (profile=subscribe, environment=%s)\n", f.listenAddr, f.path, f.environment)
	return serveReceiver(ctx, srv)
}
