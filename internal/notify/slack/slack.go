// Package slack is the slice 543 Slack notification delivery channel. It
// is a delivery SINK for the slice-029 notifications store (it does NOT
// produce notifications — P0-543-4), generalizing the slice-445 email
// substrate (internal/notify/email) to a second channel.
//
// Shape (mirrors email, slice 543 D1 — sibling packages over a heavy
// plugin registry):
//
//	Transport  — a one-method abstraction (Post) with a Slack
//	             incoming-webhook implementation. The interface is the seam
//	             that lets tests substitute an in-memory / httptest sink.
//	Config     — env-driven (ATLAS_SLACK_*) for the single-VM self-host
//	             target. The webhook URL is a Secret (it carries a token in
//	             its path); env-only, NEVER logged (P0-543-5).
//	Channel    — the delivery orchestrator: reads the opted-in user's
//	             notifications under their OWN tenant context (RLS), builds a
//	             minimum-disclosure summary, claims the digest idempotently,
//	             and posts a counts+deep-link-only Block Kit message.
//
// Security guards (threat model I / S / T):
//
//   - payload carries summary counts + a single deep-link only; NO
//     notification details/evidence/secrets (P0-543-1, minimum disclosure)
//   - the Slack target is OPERATOR-configured (env), never user-controlled
//     free-text and never derived from notification content (P0-543-2)
//   - interpolated text is escaped for the Slack mrkdwn/text context
//     (threat-model T — the 445 CRLF/HTML guard analog)
//   - the webhook URL secret is env-only, redacted in any String(), never
//     logged (P0-543-5)
package slack

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/mgoodric/security-atlas/internal/db/dbx"
	"github.com/mgoodric/security-atlas/internal/notify"
	"github.com/mgoodric/security-atlas/internal/tenancy"
)

// channelName is the discriminator stored in channel_delivery_log.channel
// and folded into the per-channel idempotency digest key.
const channelName = "slack"

// Env var names (ATLAS_* platform convention, mirrors email's ATLAS_SMTP_*).
const (
	envSlackWebhookURL = "ATLAS_SLACK_WEBHOOK_URL"
	envSlackTimeout    = "ATLAS_SLACK_TIMEOUT"
	envBaseURL         = "ATLAS_PUBLIC_BASE_URL"
)

// DefaultTimeout bounds a single Slack POST (a slow endpoint fails fast).
const DefaultTimeout = 10 * time.Second

// Config is the env-driven Slack configuration for self-host.
type Config struct {
	// WebhookURL is the Slack incoming-webhook URL. It carries a secret
	// token in its path, so it is a Secret: redacted in logs, env-only.
	WebhookURL notify.Secret
	Timeout    time.Duration
	BaseURL    string
}

// ConfigFromEnv reads the Slack config from the process environment.
func ConfigFromEnv() Config { return configFromLookup(os.LookupEnv) }

func configFromLookup(lookup func(string) (string, bool)) Config {
	cfg := Config{Timeout: DefaultTimeout}
	if v, ok := lookup(envSlackWebhookURL); ok {
		cfg.WebhookURL = notify.Secret(strings.TrimSpace(v))
	}
	if v, ok := lookup(envSlackTimeout); ok {
		if d, err := time.ParseDuration(v); err == nil && d > 0 {
			cfg.Timeout = d
		}
	}
	if v, ok := lookup(envBaseURL); ok {
		cfg.BaseURL = v
	}
	return cfg
}

// Enabled reports whether enough config is present to attempt delivery.
func (c Config) Enabled() bool { return !c.WebhookURL.IsZero() }

// Redacted returns a log-safe rendering that NEVER includes the webhook URL
// secret (P0-543-5).
func (c Config) Redacted() string {
	return fmt.Sprintf("slack webhook_url=%s timeout=%s", c.WebhookURL, c.Timeout)
}

// Transport posts a built Slack message. The interface is the test seam.
type Transport interface {
	// Post transmits one built message body (already minimum-disclosure +
	// escaped). Implementations MUST honor the context deadline.
	Post(ctx context.Context, body []byte) error
}

// ErrNotConfigured is returned when delivery is attempted with no webhook
// URL configured. The channel treats this as "channel inert".
var ErrNotConfigured = errors.New("slack: webhook url not configured")

// HTTPTransport is the production incoming-webhook implementation.
type HTTPTransport struct {
	cfg    Config
	client *http.Client
}

// NewHTTPTransport constructs an HTTPTransport from config.
func NewHTTPTransport(cfg Config) *HTTPTransport {
	return &HTTPTransport{cfg: cfg, client: &http.Client{}}
}

// Post delivers the JSON body to the Slack incoming-webhook URL. The
// webhook URL secret is revealed ONLY here (transport boundary) and never
// logged; any error is scrubbed of the secret before it surfaces
// (defense-in-depth, P0-543-5).
func (t *HTTPTransport) Post(ctx context.Context, body []byte) error {
	if !t.cfg.Enabled() {
		return ErrNotConfigured
	}
	if t.cfg.Timeout > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, t.cfg.Timeout)
		defer cancel()
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, t.cfg.WebhookURL.Reveal(), bytes.NewReader(body))
	if err != nil {
		return errors.New(notify.ScrubSecret(err.Error(), t.cfg.WebhookURL))
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := t.client.Do(req)
	if err != nil {
		return errors.New("slack: post failed: " + notify.ScrubSecret(err.Error(), t.cfg.WebhookURL))
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		snippet, _ := io.ReadAll(io.LimitReader(resp.Body, 256))
		return fmt.Errorf("slack: post status %d: %s", resp.StatusCode,
			notify.ScrubSecret(string(snippet), t.cfg.WebhookURL))
	}
	return nil
}

// DeliveryResult reports the outcome of a delivery attempt for one user.
type DeliveryResult struct {
	Sent    bool
	Skipped bool
	Reason  string
}

// Channel is the Slack delivery orchestrator. Mirrors email.Channel.
type Channel struct {
	pool      *pgxpool.Pool
	transport Transport
	baseURL   string
	now       func() time.Time
}

// NewChannel wires the channel.
func NewChannel(pool *pgxpool.Pool, transport Transport, baseURL string) *Channel {
	return &Channel{pool: pool, transport: transport, baseURL: baseURL, now: time.Now}
}

// SetOptIn sets the caller's Slack-channel master opt-in. Default
// opted-OUT; this is the only path that flips it on. Tenant + user come
// from the authenticated context (no user-controlled target — P0-543-2).
func (c *Channel) SetOptIn(ctx context.Context, tenantID, userID uuid.UUID, enabled bool) error {
	return c.inTx(ctx, func(ctx context.Context, q *dbx.Queries) error {
		_, err := q.UpsertSlackOptIn(ctx, dbx.UpsertSlackOptInParams{
			TenantID: pgUUID(tenantID), UserID: pgUUID(userID), Enabled: enabled,
		})
		if err != nil {
			return fmt.Errorf("slack: upsert opt-in: %w", err)
		}
		return nil
	})
}

// GetOptIn reports whether the caller has opted in. Missing row = opted-OUT.
func (c *Channel) GetOptIn(ctx context.Context, tenantID, userID uuid.UUID) (bool, error) {
	var enabled bool
	err := c.inTx(ctx, func(ctx context.Context, q *dbx.Queries) error {
		v, err := q.GetSlackOptIn(ctx, dbx.GetSlackOptInParams{
			TenantID: pgUUID(tenantID), UserID: pgUUID(userID),
		})
		if errors.Is(err, pgx.ErrNoRows) {
			enabled = false
			return nil
		}
		if err != nil {
			return fmt.Errorf("slack: get opt-in: %w", err)
		}
		enabled = v
		return nil
	})
	return enabled, err
}

// DeliverDigest builds + delivers the unread-notification summary for one
// target user via Slack. The ctx MUST carry the user's tenant; everything
// is read under that tenant context (RLS), so cross-tenant delivery is
// impossible. Flow mirrors email.Channel.DeliverDigest.
func (c *Channel) DeliverDigest(ctx context.Context, userID uuid.UUID, recipientUserID string) (DeliveryResult, error) {
	tenantID, err := tenantFromCtx(ctx)
	if err != nil {
		return DeliveryResult{}, err
	}

	optedIn, err := c.GetOptIn(ctx, tenantID, userID)
	if err != nil {
		return DeliveryResult{}, err
	}
	if !optedIn {
		return DeliveryResult{Skipped: true, Reason: "user opted out"}, nil
	}

	digestKey := notify.DigestKeyForDay(channelName, c.now().UTC().Format("2006-01-02"))

	var result DeliveryResult
	var body []byte
	var claimID pgtype.UUID
	var claimed bool
	err = c.inTx(ctx, func(ctx context.Context, q *dbx.Queries) error {
		rows, err := q.ListNotificationsForUser(ctx, dbx.ListNotificationsForUserParams{
			TenantID: pgUUID(tenantID), RecipientUserID: recipientUserID, Limit: 500, Offset: 0,
		})
		if err != nil {
			return fmt.Errorf("slack: list notifications: %w", err)
		}
		counts := map[string]int{}
		total := 0
		for _, r := range rows {
			if r.ReadAt.Valid {
				continue
			}
			counts[r.Type]++
			total++
		}
		if total == 0 {
			result = DeliveryResult{Skipped: true, Reason: "no unread notifications"}
			return nil
		}

		id, err := q.ClaimChannelDigest(ctx, dbx.ClaimChannelDigestParams{
			TenantID: pgUUID(tenantID), Channel: channelName,
			RecipientUserID: recipientUserID, DigestKey: digestKey,
		})
		if errors.Is(err, pgx.ErrNoRows) {
			result = DeliveryResult{Skipped: true, Reason: "already delivered this period"}
			return nil
		}
		if err != nil {
			return fmt.Errorf("slack: claim digest: %w", err)
		}
		claimID = id
		claimed = true

		body, err = BuildMessage(notify.Summary{
			TypeCounts: counts, TotalUnread: total, DeepLink: notify.DeepLink(c.baseURL),
		})
		if err != nil {
			return fmt.Errorf("slack: build message: %w", err)
		}
		return nil
	})
	if err != nil {
		return DeliveryResult{}, err
	}
	if !claimed {
		return result, nil
	}

	sendErr := c.transport.Post(ctx, body)

	recErr := c.inTx(ctx, func(ctx context.Context, q *dbx.Queries) error {
		if sendErr != nil {
			return q.MarkChannelDigestFailed(ctx, dbx.MarkChannelDigestFailedParams{
				TenantID: pgUUID(tenantID), ID: claimID, LastError: truncErr(sendErr),
			})
		}
		return q.MarkChannelDigestSent(ctx, dbx.MarkChannelDigestSentParams{
			TenantID: pgUUID(tenantID), ID: claimID,
		})
	})
	if sendErr != nil {
		return DeliveryResult{}, fmt.Errorf("slack: send failed: %w", sendErr)
	}
	if recErr != nil {
		return DeliveryResult{}, fmt.Errorf("slack: record outcome: %w", recErr)
	}
	return DeliveryResult{Sent: true}, nil
}

func (c *Channel) inTx(ctx context.Context, fn func(context.Context, *dbx.Queries) error) error {
	tx, err := c.pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("slack: begin tx: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if err := tenancy.ApplyTenant(ctx, tx); err != nil {
		return err
	}
	q := dbx.New(tx)
	if err := fn(ctx, q); err != nil {
		return err
	}
	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("slack: commit: %w", err)
	}
	return nil
}

func tenantFromCtx(ctx context.Context) (uuid.UUID, error) {
	tenantStr, err := tenancy.TenantFromContext(ctx)
	if err != nil {
		return uuid.Nil, err
	}
	id, err := uuid.Parse(tenantStr)
	if err != nil {
		return uuid.Nil, fmt.Errorf("slack: parse tenant id: %w", err)
	}
	return id, nil
}

func pgUUID(u uuid.UUID) pgtype.UUID { return pgtype.UUID{Bytes: u, Valid: true} }

// truncErr bounds the persisted last_error and strips control chars. It is
// called on the transport error, which is already secret-scrubbed by the
// transport; this is belt-and-suspenders.
func truncErr(err error) string {
	s := strings.Map(func(r rune) rune {
		if r < 0x20 || r == 0x7f {
			return -1
		}
		return r
	}, err.Error())
	const max = 500
	if len(s) > max {
		return s[:max]
	}
	return s
}
