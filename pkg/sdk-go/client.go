// Package sdk is the security-atlas Go push SDK. Wraps the generated
// EvidenceIngestService gRPC client with a small, stable surface area.
package sdk

import (
	"context"
	"crypto/tls"
	"fmt"
	"math/rand/v2"
	"net"
	"strconv"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"

	evidencev1 "github.com/mgoodric/security-atlas/gen/proto/evidence/v1"
)

// MetadataAuthorization is the gRPC metadata key carrying the bearer token.
// BearerPrefix is the required prefix on its value.
const (
	MetadataAuthorization = "authorization"
	BearerPrefix          = "Bearer "
)

// Client is a thread-safe Evidence push client. Use NewClient to construct.
type Client struct {
	conn     *grpc.ClientConn
	stub     evidencev1.EvidenceIngestServiceClient
	bearer   string
	ownsConn bool
	retry    RetryConfig
}

// Option configures the Client.
type Option func(*options)

type options struct {
	insecure bool
	tls      *tls.Config
	retry    RetryConfig
}

// RetryConfig controls Push retry/backoff behavior.
//
// Retries are only attempted for transport-class gRPC statuses:
// Unavailable and DeadlineExceeded. The same EvidenceRecord pointer is
// re-sent unchanged on every attempt, so the idempotency_key and canonical
// record hash remain stable and server-side dedup absorbs a successful
// re-send.
type RetryConfig struct {
	// MaxAttempts is the total attempts including the first RPC. Values less
	// than 1 disable retries by making Push attempt exactly once.
	MaxAttempts int
	// BaseDelay is the first retry delay before jitter is applied.
	BaseDelay time.Duration
	// MaxDelay caps exponential backoff and Retry-After sleeps.
	MaxDelay time.Duration
	// Jitter is the fractional +/- spread applied to exponential delays.
	// Values below 0 are treated as 0; values above 1 are treated as 1.
	Jitter float64
}

// DefaultRetryConfig is the documented SDK retry schedule: one initial Push
// plus retries after 1s, 2s, 4s, and 8s, with 20% jitter.
var DefaultRetryConfig = RetryConfig{
	MaxAttempts: 5,
	BaseDelay:   time.Second,
	MaxDelay:    8 * time.Second,
	Jitter:      0.20,
}

// WithInsecure disables TLS. Only valid when endpoint is a loopback
// address; refuses non-loopback endpoints to prevent accidental plaintext
// over the wire.
func WithInsecure() Option {
	return func(o *options) { o.insecure = true }
}

// WithTLSConfig overrides the default TLS configuration (system roots).
func WithTLSConfig(c *tls.Config) Option {
	return func(o *options) { o.tls = c }
}

// WithRetryConfig overrides the default retry schedule. Set MaxAttempts to
// 1 to disable retry.
func WithRetryConfig(c RetryConfig) Option {
	return func(o *options) { o.retry = normalizeRetryConfig(c) }
}

// NewClient dials endpoint and prepares a Client. bearer is the bearer
// token issued by AdminCredentials.Issue; it is sent on every RPC.
func NewClient(endpoint, bearer string, opts ...Option) (*Client, error) {
	if bearer == "" {
		return nil, fmt.Errorf("sdk: bearer token is required")
	}

	o := options{retry: DefaultRetryConfig}
	for _, opt := range opts {
		opt(&o)
	}

	var transport grpc.DialOption
	switch {
	case o.insecure:
		if !isLoopback(endpoint) {
			return nil, fmt.Errorf("sdk: WithInsecure refuses non-loopback endpoint %q", endpoint)
		}
		transport = grpc.WithTransportCredentials(insecure.NewCredentials())
	default:
		cfg := o.tls
		if cfg == nil {
			cfg = &tls.Config{MinVersion: tls.VersionTLS12}
		}
		transport = grpc.WithTransportCredentials(credentials.NewTLS(cfg))
	}

	conn, err := grpc.NewClient(endpoint, transport)
	if err != nil {
		return nil, fmt.Errorf("sdk: dial %s: %w", endpoint, err)
	}
	return &Client{
		conn:     conn,
		stub:     evidencev1.NewEvidenceIngestServiceClient(conn),
		bearer:   bearer,
		ownsConn: true,
		retry:    o.retry,
	}, nil
}

// Close releases the underlying gRPC connection if the Client owns it.
// Clients constructed via NewClientFromConn return nil — the caller closes
// the conn they passed in.
func (c *Client) Close() error {
	if !c.ownsConn {
		return nil
	}
	return c.conn.Close()
}

// Push sends one evidence record. Wraps gRPC errors so callers can use
// errors.As to extract a status.
func (c *Client) Push(ctx context.Context, record *evidencev1.EvidenceRecord) (*evidencev1.EvidenceReceipt, error) {
	ctx = metadata.AppendToOutgoingContext(ctx, MetadataAuthorization, BearerPrefix+c.bearer)
	var lastErr error
	for attempt := 1; attempt <= c.retry.MaxAttempts; attempt++ {
		var header, trailer metadata.MD
		resp, err := c.stub.Push(ctx, &evidencev1.PushRequest{Record: record}, grpc.Header(&header), grpc.Trailer(&trailer))
		if err == nil {
			return resp.GetReceipt(), nil
		}
		lastErr = err
		if attempt == c.retry.MaxAttempts || !isRetryable(err) {
			return nil, fmt.Errorf("sdk: push: %w", err)
		}
		delay := c.retry.delay(attempt)
		if retryAfter, ok := retryAfterDelay(header, trailer); ok {
			delay = retryAfter
			if c.retry.MaxDelay > 0 && delay > c.retry.MaxDelay {
				delay = c.retry.MaxDelay
			}
		}
		timer := time.NewTimer(delay)
		select {
		case <-ctx.Done():
			if !timer.Stop() {
				<-timer.C
			}
			return nil, fmt.Errorf("sdk: push: %w", ctx.Err())
		case <-timer.C:
		}
	}
	return nil, fmt.Errorf("sdk: push: %w", lastErr)
}

// NewClientFromConn builds a Client around an existing grpc.ClientConn
// (typical in tests using bufconn). The Client does not own the conn; its
// Close() is a no-op so the caller can close their own.
func NewClientFromConn(conn *grpc.ClientConn, bearer string, opts ...Option) *Client {
	o := options{retry: DefaultRetryConfig}
	for _, opt := range opts {
		opt(&o)
	}
	return &Client{
		conn:     conn,
		stub:     evidencev1.NewEvidenceIngestServiceClient(conn),
		bearer:   bearer,
		ownsConn: false,
		retry:    o.retry,
	}
}

// isLoopback returns true for the loopback hosts WithInsecure accepts. Uses
// net.SplitHostPort so IPv6 brackets are handled correctly.
func isLoopback(endpoint string) bool {
	host, _, err := net.SplitHostPort(endpoint)
	if err != nil {
		host = endpoint
	}
	switch host {
	case "", "localhost", "127.0.0.1", "::1":
		return true
	}
	return false
}

func isRetryable(err error) bool {
	switch status.Code(err) {
	case codes.Unavailable, codes.DeadlineExceeded:
		return true
	default:
		return false
	}
}

func normalizeRetryConfig(c RetryConfig) RetryConfig {
	if c.MaxAttempts < 1 {
		c.MaxAttempts = 1
	}
	if c.BaseDelay < 0 {
		c.BaseDelay = 0
	}
	if c.MaxDelay < 0 {
		c.MaxDelay = 0
	}
	if c.MaxDelay > 0 && c.BaseDelay > c.MaxDelay {
		c.BaseDelay = c.MaxDelay
	}
	if c.Jitter < 0 {
		c.Jitter = 0
	}
	if c.Jitter > 1 {
		c.Jitter = 1
	}
	return c
}

func (c RetryConfig) delay(retryNumber int) time.Duration {
	delay := c.BaseDelay
	for i := 1; i < retryNumber; i++ {
		delay *= 2
		if c.MaxDelay > 0 && delay >= c.MaxDelay {
			delay = c.MaxDelay
			break
		}
	}
	if c.Jitter > 0 && delay > 0 {
		spread := int64(float64(delay) * c.Jitter)
		if spread > 0 {
			offset := rand.Int64N(spread*2+1) - spread
			delay += time.Duration(offset)
		}
	}
	if c.MaxDelay > 0 && delay > c.MaxDelay {
		return c.MaxDelay
	}
	return delay
}

func retryAfterDelay(mds ...metadata.MD) (time.Duration, bool) {
	for _, md := range mds {
		values := md.Get("retry-after")
		if len(values) == 0 {
			values = md.Get("Retry-After")
		}
		for _, value := range values {
			if secs, err := strconv.Atoi(value); err == nil && secs >= 0 {
				return time.Duration(secs) * time.Second, true
			}
			if when, err := time.Parse(time.RFC1123, value); err == nil {
				delay := time.Until(when)
				if delay < 0 {
					delay = 0
				}
				return delay, true
			}
		}
	}
	return 0, false
}
