// Package webhook is the slice 543 generic-webhook notification delivery
// channel (PagerDuty / a SIEM / an internal bot). It is a delivery SINK for
// the slice-029 notifications store (NOT a producer — P0-543-4),
// generalizing the slice-445 email substrate to a third channel.
//
// Shape (mirrors email + slack, slice 543 D1):
//
//	Transport  — a one-method abstraction (Post) with an HTTP POST
//	             implementation. The interface is the test seam.
//	Config     — env-driven (ATLAS_WEBHOOK_*). The target URL is
//	             OPERATOR-configured and VALIDATED against the SSRF guard at
//	             construction (P0-543-2). An optional bearer + HMAC signing
//	             secret are Secrets: env-only, NEVER logged (P0-543-5).
//	Channel    — the delivery orchestrator: reads the opted-in user's
//	             notifications under their OWN tenant context (RLS), builds a
//	             flat minimum-disclosure JSON payload, claims the digest
//	             idempotently, and POSTs it.
//
// Security guards (threat model S / I / T):
//
//   - SSRF (DOMINANT, P0-543-2): the URL is operator-configured (env), not
//     user free-text, not notification-derived; AND it is validated by
//     notify.SSRFPolicy at channel construction — an internal target
//     (loopback / RFC1918 / 169.254.169.254 / ULA / CGNAT) is rejected
//     before any send.
//   - minimum disclosure (P0-543-1): payload carries counts + a deep-link
//     only; no notification details/evidence/secrets.
//   - injection (threat-model T): the payload is structured JSON built by
//     the stdlib encoder (no string interpolation into the wire); type
//     labels come from the CLOSED notify.TypeLabel map.
//   - secrets (P0-543-5): bearer + HMAC signing secret are Secrets,
//     env-only, redacted in String(), scrubbed from any surfaced error.
package webhook

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
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

const channelName = "webhook"

const (
	envWebhookURL     = "ATLAS_WEBHOOK_URL"
	envWebhookBearer  = "ATLAS_WEBHOOK_BEARER"
	envWebhookHMACKey = "ATLAS_WEBHOOK_HMAC_SECRET"
	envWebhookTimeout = "ATLAS_WEBHOOK_TIMEOUT"
	envBaseURL        = "ATLAS_PUBLIC_BASE_URL"
)

// DefaultTimeout bounds a single webhook POST.
const DefaultTimeout = 10 * time.Second

// signatureHeader carries the hex HMAC-SHA256 of the body when a signing
// secret is configured (lets the receiver verify authenticity).
const signatureHeader = "X-Atlas-Signature"

// Config is the env-driven webhook configuration for self-host.
type Config struct {
	// URL is the operator-configured target. It is validated by the SSRF
	// guard at channel construction (P0-543-2). It is NOT a Secret (no
	// credential), but it is never user-controlled free-text.
	URL string
	// Bearer is an optional Authorization: Bearer token (Secret).
	Bearer notify.Secret
	// HMACSecret is an optional HMAC-SHA256 signing key (Secret).
	HMACSecret notify.Secret
	Timeout    time.Duration
	BaseURL    string
}

// ConfigFromEnv reads the webhook config from the process environment.
func ConfigFromEnv() Config { return configFromLookup(os.LookupEnv) }

func configFromLookup(lookup func(string) (string, bool)) Config {
	cfg := Config{Timeout: DefaultTimeout}
	if v, ok := lookup(envWebhookURL); ok {
		cfg.URL = strings.TrimSpace(v)
	}
	if v, ok := lookup(envWebhookBearer); ok {
		cfg.Bearer = notify.Secret(strings.TrimSpace(v))
	}
	if v, ok := lookup(envWebhookHMACKey); ok {
		cfg.HMACSecret = notify.Secret(strings.TrimSpace(v))
	}
	if v, ok := lookup(envWebhookTimeout); ok {
		if d, err := time.ParseDuration(v); err == nil && d > 0 {
			cfg.Timeout = d
		}
	}
	if v, ok := lookup(envBaseURL); ok {
		cfg.BaseURL = v
	}
	return cfg
}

// Enabled reports whether a target URL is configured.
func (c Config) Enabled() bool { return c.URL != "" }

// Redacted returns a log-safe rendering — the URL is shown (operator
// config, no credential) but bearer + HMAC secret are redacted (P0-543-5).
func (c Config) Redacted() string {
	return fmt.Sprintf("webhook url=%s bearer=%s hmac_secret=%s timeout=%s",
		c.URL, c.Bearer, c.HMACSecret, c.Timeout)
}

// Transport posts a built webhook body.
type Transport interface {
	Post(ctx context.Context, body []byte) error
}

// ErrNotConfigured is returned when delivery is attempted with no URL.
var ErrNotConfigured = errors.New("webhook: url not configured")

// SSRFPolicy returns the STRICT production SSRF policy: https-only, all
// internal/non-routable targets denied (P0-543-2). The production binary
// constructs the webhook transport with this policy so an operator cannot
// point the channel at an internal service. Tests use a relaxed policy
// (AllowHTTP/AllowLoopback) to target a local httptest server.
func SSRFPolicy() notify.SSRFPolicy { return notify.SSRFPolicy{} }

// HTTPTransport is the production HTTP-POST implementation. Its URL has
// ALREADY passed the SSRF guard (validated at NewHTTPTransport).
type HTTPTransport struct {
	cfg    Config
	url    string // SSRF-validated target
	client *http.Client
}

// NewHTTPTransport validates the configured URL against the SSRF policy and
// constructs the transport. A failing validation (internal target, bad
// scheme) returns an error so the channel fails fast and visibly rather
// than silently at send time (P0-543-2).
func NewHTTPTransport(cfg Config, policy notify.SSRFPolicy) (*HTTPTransport, error) {
	if !cfg.Enabled() {
		// Inert transport: Post returns ErrNotConfigured. We still return a
		// non-nil transport so the channel can be wired uniformly.
		return &HTTPTransport{cfg: cfg, client: &http.Client{}}, nil
	}
	validated, err := policy.ValidateWebhookURL(cfg.URL)
	if err != nil {
		return nil, fmt.Errorf("webhook: target rejected: %w", err)
	}
	return &HTTPTransport{cfg: cfg, url: validated, client: &http.Client{}}, nil
}

// Post delivers the JSON body. Bearer + HMAC secret are revealed ONLY here
// (transport boundary); any error is scrubbed of both before it surfaces.
func (t *HTTPTransport) Post(ctx context.Context, body []byte) error {
	if !t.cfg.Enabled() {
		return ErrNotConfigured
	}
	if t.cfg.Timeout > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, t.cfg.Timeout)
		defer cancel()
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, t.url, bytes.NewReader(body))
	if err != nil {
		return errors.New(t.scrub(err.Error()))
	}
	req.Header.Set("Content-Type", "application/json")
	if !t.cfg.Bearer.IsZero() {
		req.Header.Set("Authorization", "Bearer "+t.cfg.Bearer.Reveal())
	}
	if !t.cfg.HMACSecret.IsZero() {
		req.Header.Set(signatureHeader, sign(body, t.cfg.HMACSecret))
	}
	resp, err := t.client.Do(req)
	if err != nil {
		return errors.New("webhook: post failed: " + t.scrub(err.Error()))
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		snippet, _ := io.ReadAll(io.LimitReader(resp.Body, 256))
		return fmt.Errorf("webhook: post status %d: %s", resp.StatusCode, t.scrub(string(snippet)))
	}
	return nil
}

func (t *HTTPTransport) scrub(s string) string {
	return notify.ScrubSecret(s, t.cfg.Bearer, t.cfg.HMACSecret)
}

// sign returns the hex HMAC-SHA256 of body keyed by the signing secret.
func sign(body []byte, key notify.Secret) string {
	mac := hmac.New(sha256.New, []byte(key.Reveal()))
	mac.Write(body)
	return hex.EncodeToString(mac.Sum(nil))
}

// DeliveryResult reports the outcome of a delivery attempt for one user.
type DeliveryResult struct {
	Sent    bool
	Skipped bool
	Reason  string
}

// Channel is the webhook delivery orchestrator. Mirrors email/slack.
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

// SetOptIn sets the caller's webhook-channel master opt-in (default OUT).
func (c *Channel) SetOptIn(ctx context.Context, tenantID, userID uuid.UUID, enabled bool) error {
	return c.inTx(ctx, func(ctx context.Context, q *dbx.Queries) error {
		_, err := q.UpsertWebhookOptIn(ctx, dbx.UpsertWebhookOptInParams{
			TenantID: pgUUID(tenantID), UserID: pgUUID(userID), Enabled: enabled,
		})
		if err != nil {
			return fmt.Errorf("webhook: upsert opt-in: %w", err)
		}
		return nil
	})
}

// GetOptIn reports whether the caller has opted in. Missing row = opted-OUT.
func (c *Channel) GetOptIn(ctx context.Context, tenantID, userID uuid.UUID) (bool, error) {
	var enabled bool
	err := c.inTx(ctx, func(ctx context.Context, q *dbx.Queries) error {
		v, err := q.GetWebhookOptIn(ctx, dbx.GetWebhookOptInParams{
			TenantID: pgUUID(tenantID), UserID: pgUUID(userID),
		})
		if errors.Is(err, pgx.ErrNoRows) {
			enabled = false
			return nil
		}
		if err != nil {
			return fmt.Errorf("webhook: get opt-in: %w", err)
		}
		enabled = v
		return nil
	})
	return enabled, err
}

// DeliverDigest builds + delivers the unread-notification summary for one
// target user via the webhook. ctx MUST carry the user's tenant; all reads
// are RLS-scoped to it. Flow mirrors email/slack.
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
			return fmt.Errorf("webhook: list notifications: %w", err)
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
			return fmt.Errorf("webhook: claim digest: %w", err)
		}
		claimID = id
		claimed = true

		body, err = BuildPayload(notify.Summary{
			TypeCounts: counts, TotalUnread: total, DeepLink: notify.DeepLink(c.baseURL),
		})
		if err != nil {
			return fmt.Errorf("webhook: build payload: %w", err)
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
		return DeliveryResult{}, fmt.Errorf("webhook: send failed: %w", sendErr)
	}
	if recErr != nil {
		return DeliveryResult{}, fmt.Errorf("webhook: record outcome: %w", recErr)
	}
	return DeliveryResult{Sent: true}, nil
}

func (c *Channel) inTx(ctx context.Context, fn func(context.Context, *dbx.Queries) error) error {
	tx, err := c.pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("webhook: begin tx: %w", err)
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
		return fmt.Errorf("webhook: commit: %w", err)
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
		return uuid.Nil, fmt.Errorf("webhook: parse tenant id: %w", err)
	}
	return id, nil
}

func pgUUID(u uuid.UUID) pgtype.UUID { return pgtype.UUID{Bytes: u, Valid: true} }

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
