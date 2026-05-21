// Package mcp's client.go is the HTTP client wrapping the platform API.
// Every tool dispatches through this client; every outbound request
// carries the User-Agent header mandated by P0-A4.
//
// Design notes:
//
//   - Per-call fresh transport (mirrors cmd/atlas-cli/cmdhttp/client.go's
//     posture). MCP processes are typically short-lived (operator session
//     length); connection pooling across long sessions matters but the
//     simplicity win of "no shared state to leak" wins for this slice.
//
//   - Response body is capped at 1 MiB via io.LimitReader (P0 — bounds
//     LLM context flood risk). A platform endpoint legitimately returning
//     > 1 MiB indicates either a list limit violation or a payload that
//     should have been excluded; both are failures we surface as a tool
//     error rather than swallow.
//
//   - 429 / Retry-After surfaces to the caller verbatim (P0-A8). No
//     silent retries. The tool handler maps this to a tool error.

package mcp

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
)

// UserAgentTemplate is the User-Agent value the MCP server sets on every
// outbound HTTP request. The `<version>` placeholder is replaced by
// NewClient.
//
// Format chosen so platform-side log filters can:
//
//  1. grep for `atlas-mcp/` to find MCP-originated traffic
//  2. grep for `(mcp; ai_assisted=read-only)` to scope to read tools
//  3. grep for `(mcp; ai_assisted=write)` to scope to write tools (slice 173)
//
// Slice 173 added the write template; the read template stays unchanged.
const UserAgentTemplate = "atlas-mcp/%s (mcp; ai_assisted=read-only)"

// UserAgentWriteTemplate is the slice-173 sibling for write tools. Write
// tools file proposals (HITL approval) rather than committing directly,
// so the User-Agent makes that distinction visible in platform-side
// audit aggregations.
const UserAgentWriteTemplate = "atlas-mcp/%s (mcp; ai_assisted=write)"

// MaxResponseBytes caps a single HTTP response read. A platform endpoint
// returning more is a contract violation that surfaces as an error.
const MaxResponseBytes = 1 << 20 // 1 MiB

// DefaultTimeout is the per-request wall-clock budget. Each tool can
// override via context; this is the safety net.
const DefaultTimeout = 30 * time.Second

// Client is the HTTP client every tool uses to call the platform API.
// Construct once at process start; share across tool invocations.
type Client struct {
	baseURL        *url.URL
	bearer         string
	userAgent      string
	writeUserAgent string
	httpClient     *http.Client
}

// NewClient constructs a Client. baseURL must parse to a valid http or
// https URL; bearer must be non-empty; version is the cmd/atlas-mcp
// build version (interpolated into the User-Agent).
func NewClient(baseURL, bearer, version string) (*Client, error) {
	if bearer == "" {
		return nil, errors.New("bearer token is required")
	}
	u, err := url.Parse(baseURL)
	if err != nil {
		return nil, fmt.Errorf("parse base url: %w", err)
	}
	if u.Scheme != "http" && u.Scheme != "https" {
		return nil, fmt.Errorf("base url scheme must be http or https, got %q", u.Scheme)
	}
	if u.Host == "" {
		return nil, errors.New("base url must include host")
	}
	if version == "" {
		version = "dev"
	}
	return &Client{
		baseURL:        u,
		bearer:         bearer,
		userAgent:      fmt.Sprintf(UserAgentTemplate, version),
		writeUserAgent: fmt.Sprintf(UserAgentWriteTemplate, version),
		httpClient: &http.Client{
			Timeout: DefaultTimeout,
			Transport: &http.Transport{
				DialContext: (&net.Dialer{
					Timeout:   10 * time.Second,
					KeepAlive: 30 * time.Second,
				}).DialContext,
				TLSHandshakeTimeout:   10 * time.Second,
				ResponseHeaderTimeout: 20 * time.Second,
				ExpectContinueTimeout: 1 * time.Second,
				ForceAttemptHTTP2:     true,
				IdleConnTimeout:       30 * time.Second,
			},
		},
	}, nil
}

// UserAgent returns the canonical User-Agent string. Exposed so tests
// can assert P0-A4 compliance without reflecting into the client.
func (c *Client) UserAgent() string { return c.userAgent }

// WriteUserAgent returns the slice-173 write-tool User-Agent string.
func (c *Client) WriteUserAgent() string { return c.writeUserAgent }

// Get performs an authenticated GET against the platform. `path` is a
// route relative to the base URL (e.g., "/v1/controls"); `params` is an
// optional set of query parameters. The caller deserializes the response
// body into `out`; if the response is non-2xx the body is returned via
// HTTPError.
//
// Slice 172 (P0-A8): a 429 surfaces as an HTTPError with StatusCode=429
// and the Retry-After header propagated to the caller. The tool handler
// maps that to a tool error so the LLM agent observes "rate limited" and
// can back off rather than silent retry.
func (c *Client) Get(ctx context.Context, path string, params url.Values, out any) error {
	req, err := c.newRequest(ctx, http.MethodGet, path, params)
	if err != nil {
		return err
	}
	resp, err := c.httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("http get %s: %w", path, err)
	}
	defer func() { _ = resp.Body.Close() }()

	// Bound body size to MaxResponseBytes. A platform endpoint that
	// legitimately exceeds this cap indicates a contract violation
	// (e.g., a list endpoint without a server-side limit).
	body, err := io.ReadAll(io.LimitReader(resp.Body, MaxResponseBytes+1))
	if err != nil {
		return fmt.Errorf("read body: %w", err)
	}
	if int64(len(body)) > MaxResponseBytes {
		return fmt.Errorf("response body exceeds %d bytes (P0 cap)", MaxResponseBytes)
	}

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return &HTTPError{
			StatusCode: resp.StatusCode,
			Body:       strings.TrimSpace(string(body)),
			RetryAfter: parseRetryAfter(resp.Header.Get("Retry-After")),
		}
	}
	if out == nil {
		return nil
	}
	if err := json.Unmarshal(body, out); err != nil {
		return fmt.Errorf("unmarshal response: %w", err)
	}
	return nil
}

// PostJSON performs an authenticated POST against the platform with a
// JSON body. Mirrors Get's contract: `path` is route-relative; `body`
// is marshalled with encoding/json; the response is unmarshalled into
// `out` (nil to skip). 429 + Retry-After are surfaced as HTTPError.
//
// Slice 173 — write tools dispatch through this method with the write
// User-Agent.
func (c *Client) PostJSON(ctx context.Context, path string, body any, out any) error {
	return c.doJSONBody(ctx, http.MethodPost, path, body, out, c.writeUserAgent)
}

// doJSONBody is the shared write-shaped HTTP path. Body marshalled to
// JSON; if body is nil, the request body is empty (Content-Length 0).
func (c *Client) doJSONBody(ctx context.Context, method, path string, body any, out any, userAgent string) error {
	if !strings.HasPrefix(path, "/") {
		return fmt.Errorf("path must start with /: %q", path)
	}
	var bodyBytes []byte
	if body != nil {
		marshalled, err := json.Marshal(body)
		if err != nil {
			return fmt.Errorf("marshal body: %w", err)
		}
		bodyBytes = marshalled
	}
	full := *c.baseURL
	full.Path = strings.TrimRight(full.Path, "/") + path

	req, err := http.NewRequestWithContext(ctx, method, full.String(), bytesReader(bodyBytes))
	if err != nil {
		return fmt.Errorf("new request: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+c.bearer)
	req.Header.Set("User-Agent", userAgent)
	req.Header.Set("Accept", "application/json")
	if bodyBytes != nil {
		req.Header.Set("Content-Type", "application/json")
	}

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("http %s %s: %w", method, path, err)
	}
	defer func() { _ = resp.Body.Close() }()

	body2, err := io.ReadAll(io.LimitReader(resp.Body, MaxResponseBytes+1))
	if err != nil {
		return fmt.Errorf("read body: %w", err)
	}
	if int64(len(body2)) > MaxResponseBytes {
		return fmt.Errorf("response body exceeds %d bytes (P0 cap)", MaxResponseBytes)
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return &HTTPError{
			StatusCode: resp.StatusCode,
			Body:       strings.TrimSpace(string(body2)),
			RetryAfter: parseRetryAfter(resp.Header.Get("Retry-After")),
		}
	}
	if out == nil {
		return nil
	}
	if err := json.Unmarshal(body2, out); err != nil {
		return fmt.Errorf("unmarshal response: %w", err)
	}
	return nil
}

// newRequest builds an *http.Request with the User-Agent + bearer header
// set. Centralizing here ensures no tool can skip P0-A4 / P0-A1's
// header rules.
func (c *Client) newRequest(ctx context.Context, method, path string, params url.Values) (*http.Request, error) {
	// Build the absolute URL by resolving path against baseURL.
	// path may be like "/v1/controls"; we deliberately do not allow
	// callers to pass absolute URLs (defense against an LLM-supplied
	// arg flowing into a redirect target).
	if !strings.HasPrefix(path, "/") {
		return nil, fmt.Errorf("path must start with /: %q", path)
	}
	full := *c.baseURL
	full.Path = strings.TrimRight(full.Path, "/") + path
	if len(params) > 0 {
		full.RawQuery = params.Encode()
	}

	req, err := http.NewRequestWithContext(ctx, method, full.String(), nil)
	if err != nil {
		return nil, fmt.Errorf("new request: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+c.bearer)
	req.Header.Set("User-Agent", c.userAgent)
	req.Header.Set("Accept", "application/json")
	return req, nil
}

// bytesReader returns nil for nil input so the http.NewRequest body is
// truly absent rather than a non-nil zero-length reader.
func bytesReader(b []byte) io.Reader {
	if b == nil {
		return nil
	}
	return bytes.NewReader(b)
}

// HTTPError carries the salient fields of a non-2xx platform response.
// Surface this to the LLM so the agent can reason about backoff vs
// fatal failure.
type HTTPError struct {
	StatusCode int
	Body       string
	// RetryAfter is the parsed Retry-After header in seconds; zero
	// when the header was absent or unparseable.
	RetryAfter int
}

func (e *HTTPError) Error() string {
	if e.RetryAfter > 0 {
		return fmt.Sprintf("platform http %d (retry after %ds): %s", e.StatusCode, e.RetryAfter, e.Body)
	}
	return fmt.Sprintf("platform http %d: %s", e.StatusCode, e.Body)
}

// parseRetryAfter handles the integer-seconds form of Retry-After.
// HTTP-date form is rare in practice and not implemented in v1; a
// non-numeric value yields 0 (caller still surfaces the error).
func parseRetryAfter(v string) int {
	if v == "" {
		return 0
	}
	n, err := strconv.Atoi(strings.TrimSpace(v))
	if err != nil || n < 0 {
		return 0
	}
	return n
}
