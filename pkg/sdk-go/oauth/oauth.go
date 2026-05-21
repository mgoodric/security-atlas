// Package oauth is the security-atlas Go SDK's OAuth 2.0
// client_credentials helper. Slice 191 ships this as the migration
// target for slice 003's api-key-based authentication — SDK
// consumers move from constructing a bearer-string directly to
// constructing an OAuth Client that handles token acquisition,
// caching, and refresh.
//
// USAGE:
//
//	oc, err := oauth.NewClient(oauth.Config{
//	    ClientID:     "...",
//	    ClientSecret: "...",
//	    IssuerURL:    "https://atlas.example.com",
//	})
//	if err != nil { ... }
//	tok, err := oc.Token(ctx)
//	if err != nil { ... }
//	// Use tok as the bearer for any /v1/* call:
//	req.Header.Set("Authorization", "Bearer "+tok)
//
// THREAD SAFETY:
//
// Client.Token is safe for concurrent callers. The internal cache
// + refresh state is guarded by a sync.Mutex. The first caller in
// a refresh window blocks all subsequent callers until the refresh
// completes — there is no thundering-herd to the issuer.
//
// REFRESH POLICY:
//
// Token() returns the cached JWT until 60 seconds before expiry,
// then refreshes synchronously. Tokens have a 1-hour lifetime per
// slice 188; the 60-second early refresh handles clock skew + slow
// requests without ever returning an about-to-expire token.
//
// SCOPE DISCIPLINE:
//
// This package does NOT implement:
//   - Refresh-token grant (v3 deferred per slice 188).
//   - DPoP (v3 deferred per slice 191 P0-191-7).
//   - Token introspection — the SDK is the resource client, not the
//     resource server. Introspection is for resource servers.
package oauth

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"
)

// DefaultRefreshLeeway is the window before expiry inside which
// Token() refreshes proactively. 60 seconds chosen to absorb clock
// skew + slow upstream calls + a busy garbage collector.
const DefaultRefreshLeeway = 60 * time.Second

// DefaultHTTPTimeout is the per-request timeout on the issuer
// HTTP call. Token acquisition is a single short round-trip; 30
// seconds is generous and bounds blocking.
const DefaultHTTPTimeout = 30 * time.Second

// Config configures a Client. ClientID + ClientSecret + IssuerURL
// are required; HTTPClient + RefreshLeeway + Now have defaults.
type Config struct {
	// ClientID is the public OAuth client identifier registered
	// with the atlas issuer (see slice 188's
	// `atlas-cli oauth issue-client`).
	ClientID string

	// ClientSecret is the plaintext OAuth client secret presented
	// to /oauth/token. NEVER persist this in source — load from
	// environment or secret manager at runtime.
	ClientSecret string

	// IssuerURL is the atlas issuer URL — the same value
	// advertised in the `iss` claim of every JWT this client will
	// acquire. The /oauth/token endpoint lives at IssuerURL +
	// "/oauth/token".
	IssuerURL string

	// Audience is the optional RFC 8693 `audience` form param
	// passed to /oauth/token. Default empty (the issuer mints
	// audience=IssuerURL).
	Audience string

	// HTTPClient is the http.Client used for the issuer call. Nil
	// falls back to a new http.Client with DefaultHTTPTimeout.
	HTTPClient *http.Client

	// RefreshLeeway is the time-before-expiry window inside which
	// Token refreshes proactively. Zero falls back to
	// DefaultRefreshLeeway.
	RefreshLeeway time.Duration

	// Now is the clock; nil falls back to time.Now. Tests inject
	// a pinned clock.
	Now func() time.Time
}

// Client is a thread-safe OAuth client_credentials bearer-token
// acquirer with synchronous refresh-before-expiry.
type Client struct {
	cfg Config

	mu        sync.Mutex
	cached    string
	expiresAt time.Time

	tokenURL string
	http     *http.Client
	now      func() time.Time
	leeway   time.Duration
}

// ErrInvalidConfig is returned by NewClient when a required field
// is missing or malformed.
var ErrInvalidConfig = errors.New("oauth: invalid config")

// NewClient constructs a Client. Returns ErrInvalidConfig if
// ClientID / ClientSecret / IssuerURL are empty or if IssuerURL is
// not parseable.
func NewClient(cfg Config) (*Client, error) {
	if cfg.ClientID == "" {
		return nil, fmt.Errorf("%w: ClientID is required", ErrInvalidConfig)
	}
	if cfg.ClientSecret == "" {
		return nil, fmt.Errorf("%w: ClientSecret is required", ErrInvalidConfig)
	}
	if cfg.IssuerURL == "" {
		return nil, fmt.Errorf("%w: IssuerURL is required", ErrInvalidConfig)
	}
	if _, err := url.Parse(cfg.IssuerURL); err != nil {
		return nil, fmt.Errorf("%w: IssuerURL is not a valid URL: %v", ErrInvalidConfig, err)
	}
	httpClient := cfg.HTTPClient
	if httpClient == nil {
		httpClient = &http.Client{Timeout: DefaultHTTPTimeout}
	}
	nowFn := cfg.Now
	if nowFn == nil {
		nowFn = time.Now
	}
	leeway := cfg.RefreshLeeway
	if leeway <= 0 {
		leeway = DefaultRefreshLeeway
	}
	return &Client{
		cfg:      cfg,
		tokenURL: strings.TrimRight(cfg.IssuerURL, "/") + "/oauth/token",
		http:     httpClient,
		now:      nowFn,
		leeway:   leeway,
	}, nil
}

// Token returns a valid bearer token. The cached token is
// returned when it's still at least RefreshLeeway away from expiry;
// otherwise Token acquires a fresh one. Concurrent callers see a
// single synchronous refresh under the mutex.
func (c *Client) Token(ctx context.Context) (string, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	now := c.now()
	if c.cached != "" && now.Add(c.leeway).Before(c.expiresAt) {
		return c.cached, nil
	}
	// Acquire fresh token.
	tok, exp, err := c.acquire(ctx, now)
	if err != nil {
		return "", err
	}
	c.cached = tok
	c.expiresAt = exp
	return tok, nil
}

// tokenResponse mirrors the RFC 6749 §5.1 access-token response.
// Only the fields the SDK needs are bound; extra fields are
// tolerated silently.
type tokenResponse struct {
	AccessToken string `json:"access_token"`
	TokenType   string `json:"token_type"`
	ExpiresIn   int    `json:"expires_in"`
}

// acquire POSTs a `grant_type=client_credentials` request to the
// issuer's /oauth/token endpoint and parses the response. Caller
// holds the mutex; we do NOT release-and-reacquire across the
// network call because concurrent acquires from racing callers
// would defeat the cache.
func (c *Client) acquire(ctx context.Context, now time.Time) (string, time.Time, error) {
	form := url.Values{}
	form.Set("grant_type", "client_credentials")
	form.Set("client_id", c.cfg.ClientID)
	form.Set("client_secret", c.cfg.ClientSecret)
	if c.cfg.Audience != "" {
		form.Set("audience", c.cfg.Audience)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.tokenURL,
		strings.NewReader(form.Encode()))
	if err != nil {
		return "", time.Time{}, fmt.Errorf("oauth: build request: %w", err)
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Accept", "application/json")

	resp, err := c.http.Do(req)
	if err != nil {
		return "", time.Time{}, fmt.Errorf("oauth: token request: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", time.Time{}, fmt.Errorf("oauth: read token response: %w", err)
	}

	if resp.StatusCode != http.StatusOK {
		return "", time.Time{}, fmt.Errorf("oauth: token endpoint returned %d: %s",
			resp.StatusCode, strings.TrimSpace(string(body)))
	}

	var tr tokenResponse
	if err := json.Unmarshal(body, &tr); err != nil {
		return "", time.Time{}, fmt.Errorf("oauth: parse token response: %w", err)
	}
	if tr.AccessToken == "" {
		return "", time.Time{}, errors.New("oauth: token response missing access_token")
	}
	if tr.ExpiresIn <= 0 {
		// Issuer didn't specify; fall back to 1 hour minus leeway.
		tr.ExpiresIn = 3600
	}
	exp := now.Add(time.Duration(tr.ExpiresIn) * time.Second)
	return tr.AccessToken, exp, nil
}
