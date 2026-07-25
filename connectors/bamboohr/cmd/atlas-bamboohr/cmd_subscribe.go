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

	"github.com/mgoodric/security-atlas/connectors/bamboohr/internal/bamboohrauth"
	"github.com/mgoodric/security-atlas/connectors/bamboohr/internal/workers"
	"github.com/mgoodric/security-atlas/connectors/hris/webhook"
	"github.com/mgoodric/security-atlas/connectors/hris/worker"
)

// BambooHR webhook signature scheme (slice 573 — see decisions log D1):
// BambooHR signs each webhook delivery with HMAC-SHA256 over the raw request
// body, keyed by the per-monitor "Private Key" (the webhook secret), hex-encoded
// in the X-BambooHR-Signature header. The receiver recomputes that HMAC and
// compares in constant time BEFORE any record is built.
const bambooSigHeader = "X-BambooHR-Signature"

// serveReceiver is seamed so the seam test can drive the long-lived receiver
// without binding a real port.
var serveReceiver = webhook.Serve

// bambooParser extracts the triggered worker ids from a BambooHR webhook
// envelope. BambooHR webhooks deliver an "employees" array of changed records,
// each carrying the employee id; a single delivery can carry MORE THAN ONE
// changed employee (e.g. a bulk status change). The parser returns EVERY changed
// employee's id (slice 655 fan-out); the receiver re-reads + pushes a record for
// each. The shared receiver de-duplicates and bounds the list (webhook.MaxFanOut)
// before fanning out.
type bambooParser struct{}

type bambooWebhookEnvelope struct {
	Employees []struct {
		ID any `json:"id"`
	} `json:"employees"`
}

func (bambooParser) ParseWorkerIDs(body []byte) ([]string, error) {
	var env bambooWebhookEnvelope
	if err := json.Unmarshal(body, &env); err != nil {
		return nil, fmt.Errorf("parse bamboohr webhook: %w", err)
	}
	ids := make([]string, 0, len(env.Employees))
	for _, e := range env.Employees {
		if id := stringifyID(e.ID); id != "" {
			ids = append(ids, id)
		}
	}
	return ids, nil
}

// stringifyID normalizes a BambooHR employee id that may arrive as a JSON number
// or string into the connector's string worker id.
func stringifyID(v any) string {
	switch x := v.(type) {
	case string:
		return strings.TrimSpace(x)
	case float64:
		return strings.TrimSpace(fmt.Sprintf("%.0f", x))
	default:
		return ""
	}
}

// bambooFetcher adapts the read-only single-worker client to the receiver's
// WorkerFetcher.
type bambooFetcher struct {
	api workers.OneAPI
}

func (f bambooFetcher) FetchWorker(ctx context.Context, workerID string) (worker.RawWorker, bool, error) {
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
		Short: "run the source-side BambooHR termination-webhook receiver (event-driven)",
		Long: `Run a long-lived SOURCE-SIDE webhook receiver IN THIS CONNECTOR PROCESS that
receives BambooHR termination / status-change webhook deliveries, verifies the
per-monitor HMAC-SHA256 signature (X-BambooHR-Signature) BEFORE processing,
re-reads the affected worker's MINIMAL lifecycle fields via the read-only
BambooHR API, builds the SAME hris.worker_lifecycle.v1 record, and pushes it.

Profile: subscribe (event-driven via the BambooHR webhook). This is NOT
continuous monitoring and NOT a platform inbound API — the platform-side wire
stays push (invariant #3). The webhook is a TRIGGER; the connector re-reads the
worker's minimal lifecycle fields to build the record, never reading beyond the
allowed field set.

Dedup: a webhook-emitted record and a pull-emitted record for the same worker in
the same UTC hour collapse to one ledger row (shared idempotency key).

Auth: set BAMBOOHR_API_KEY + BAMBOOHR_COMPANY_DOMAIN (read-only worker-directory
role) and BAMBOOHR_WEBHOOK_SECRET (the per-monitor private key). None appears in
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
	cmd.Flags().StringVar(&f.baseURL, "base-url", "", "BambooHR API base URL override (env: BAMBOOHR_BASE_URL)")
	cmd.Flags().StringVar(&f.listenAddr, "listen", "127.0.0.1:8534", "address the webhook receiver binds (loopback by default; front with a reverse proxy for TLS)")
	cmd.Flags().StringVar(&f.path, "path", "/hooks/bamboohr", "URL path the receiver serves")
	return cmd
}

func doSubscribe(ctx context.Context, f subscribeFlags) error {
	cred, err := bamboohrauth.Resolve(bamboohrauth.ResolveOpts{BaseURL: f.baseURL})
	if err != nil {
		return fmt.Errorf("auth: %w", err)
	}
	secret := strings.TrimSpace(os.Getenv(bamboohrauth.EnvWebhookSecret))
	if secret == "" {
		return fmt.Errorf("webhook secret required: set %s (the per-monitor private key)", bamboohrauth.EnvWebhookSecret)
	}

	sdkClient, err := newSDKClient(common.endpoint, common.token, sdkOpts()...)
	if err != nil {
		return fmt.Errorf("sdk client: %w", err)
	}
	defer func() { _ = sdkClient.Close() }()

	httpClient := &http.Client{Timeout: 30 * time.Second}
	oneAPI := newWorkersOneAPI(httpClient, cred.BaseURL(), cred.CompanyDomain(), cred.APIKey())

	rec, err := webhook.NewReceiver(webhook.Config{
		Vendor:      worker.HRISBambooHR,
		Verifier:    webhook.NewHMACVerifier(worker.HRISBambooHR, secret, bambooSigHeader, "", webhook.EncodingHex),
		Parser:      bambooParser{},
		Fetcher:     bambooFetcher{api: oneAPI},
		Pusher:      sdkClient,
		ControlID:   f.workerControl,
		ActorID:     actorID("webhook"),
		Environment: f.environment,
	})
	if err != nil {
		return fmt.Errorf("receiver: %w", err)
	}

	srv := webhook.NewServer(f.listenAddr, f.path, rec)
	fmt.Printf("bamboohr termination-webhook receiver listening on %s%s (profile=subscribe, environment=%s)\n", f.listenAddr, f.path, f.environment)
	return serveReceiver(ctx, srv)
}
