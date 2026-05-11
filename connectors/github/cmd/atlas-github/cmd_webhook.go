package main

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/spf13/cobra"
	"google.golang.org/protobuf/types/known/structpb"
	"google.golang.org/protobuf/types/known/timestamppb"

	evidencev1 "github.com/mgoodric/security-atlas/gen/proto/evidence/v1"
	sdk "github.com/mgoodric/security-atlas/pkg/sdk-go"

	"github.com/mgoodric/security-atlas/connectors/github/internal/githubwebhook"
)

// EnvWebhookSecret is the env var the webhook receiver reads for the
// shared signing key. Anti-criterion P0: never accepted via flag, never
// echoed in logs.
const EnvWebhookSecret = "GITHUB_WEBHOOK_SECRET"

type webhookFlags struct {
	addr        string
	path        string
	environment string
	controlID   string
}

func newWebhookCmd() *cobra.Command {
	var f webhookFlags
	cmd := &cobra.Command{
		Use:   "webhook",
		Short: "start the github.audit_event.v1 webhook receiver",
		Long: `Start the HTTP receiver for GitHub organization webhooks.

Configure a GitHub organization webhook with:
  - Payload URL: https://<this-host>` + "<--path>" + `
  - Content type: application/json
  - Secret: the value of $GITHUB_WEBHOOK_SECRET in this environment
  - SSL verification: enabled

Every delivery:
  1. Has its X-Hub-Signature-256 verified with constant-time compare.
  2. Has its X-GitHub-Delivery used verbatim as the idempotency_key.
  3. Is transformed to github.audit_event.v1 and pushed via the SDK.

Anti-criterion P0: this binary refuses to start if GITHUB_WEBHOOK_SECRET
is empty.`,
		SilenceErrors: true,
		SilenceUsage:  true,
		PreRunE: func(_ *cobra.Command, _ []string) error {
			if f.environment == "" {
				return errors.New("--environment is required")
			}
			if os.Getenv(EnvWebhookSecret) == "" {
				return fmt.Errorf("%s is required (never accepted via flag — anti-criterion P0)", EnvWebhookSecret)
			}
			return resolveCommon()
		},
		RunE: func(_ *cobra.Command, _ []string) error {
			return doWebhook(context.Background(), f)
		},
	}
	cmd.Flags().StringVar(&f.addr, "addr", ":8080", "address to bind the webhook receiver on")
	cmd.Flags().StringVar(&f.path, "path", "/webhook", "HTTP path the GitHub webhook delivers to")
	cmd.Flags().StringVar(&f.environment, "environment", "", "environment scope tag [required]")
	cmd.Flags().StringVar(&f.controlID, "control-id", "scf:MON-01", "control_id to attach to github.audit_event.v1 records")
	return cmd
}

func doWebhook(ctx context.Context, f webhookFlags) error {
	secret := []byte(os.Getenv(EnvWebhookSecret))
	sdkClient, err := sdk.NewClient(common.endpoint, common.token, sdkOpts()...)
	if err != nil {
		return fmt.Errorf("sdk client: %w", err)
	}
	defer func() { _ = sdkClient.Close() }()

	pusher := &sdkPusher{client: sdkClient, env: f.environment, controlID: f.controlID}
	handler, err := githubwebhook.NewHandler(secret, pusher, nil)
	if err != nil {
		return fmt.Errorf("webhook handler: %w", err)
	}

	mux := http.NewServeMux()
	mux.Handle(f.path, handler)
	mux.HandleFunc("/healthz", func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusOK) })

	srv := &http.Server{
		Addr:              f.addr,
		Handler:           mux,
		ReadHeaderTimeout: 10 * time.Second,
		ReadTimeout:       30 * time.Second,
		WriteTimeout:      30 * time.Second,
		IdleTimeout:       120 * time.Second,
	}

	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)
	errCh := make(chan error, 1)
	go func() {
		fmt.Printf("github webhook receiver listening addr=%s path=%s env=%s\n", f.addr, f.path, f.environment)
		errCh <- srv.ListenAndServe()
	}()

	select {
	case <-ctx.Done():
		// caller cancelled — let the signal path handle shutdown
	case <-sigCh:
		shutCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = srv.Shutdown(shutCtx)
	case err := <-errCh:
		if err != nil && !errors.Is(err, http.ErrServerClosed) {
			return fmt.Errorf("listen: %w", err)
		}
	}
	return nil
}

// sdkPusher adapts pkg/sdk-go's Client to the githubwebhook.Pusher
// interface. Lives in this package so githubwebhook stays free of
// protobuf dependencies.
type sdkPusher struct {
	client    *sdk.Client
	env       string
	controlID string
}

func (p *sdkPusher) Push(ctx context.Context, r *githubwebhook.AuditEventRecord) error {
	rec, err := buildAuditEventRecord(r, p.env, p.controlID)
	if err != nil {
		return err
	}
	_, err = p.client.Push(ctx, rec)
	return err
}

func buildAuditEventRecord(r *githubwebhook.AuditEventRecord, env, controlID string) (*evidencev1.EvidenceRecord, error) {
	pm := map[string]any{
		"event_type":  r.EventType,
		"action":      r.Action,
		"actor":       r.Actor,
		"created_at":  r.CreatedAt.UTC().Format(time.RFC3339),
		"org":         r.Org,
		"delivery_id": r.DeliveryID,
	}
	if r.Repo != "" {
		pm["repo"] = r.Repo
	}
	payload, err := structpb.NewStruct(pm)
	if err != nil {
		return nil, err
	}
	return &evidencev1.EvidenceRecord{
		IdempotencyKey: r.IdempotencyKey, // anti-criterion P0: verbatim X-GitHub-Delivery
		EvidenceKind:   "github.audit_event.v1",
		SchemaVersion:  "1.0.0",
		ControlId:      controlID,
		Scope: []*evidencev1.ScopeDimension{
			{Key: "org", Values: []string{r.Org}},
			{Key: "environment", Values: []string{env}},
		},
		ObservedAt: timestamppb.New(r.CreatedAt.UTC()),
		Result:     evidencev1.Result_RESULT_INCONCLUSIVE, // event-level, evaluator decides per (kind, action)
		Payload:    payload,
		SourceAttribution: &evidencev1.SourceAttribution{
			ActorType: "connector",
			ActorId:   actorID("webhook"),
		},
	}, nil
}
