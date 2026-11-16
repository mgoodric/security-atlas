// Package sdk is the security-atlas Go push SDK. Wraps the generated
// EvidenceIngestService gRPC client with a small, stable surface area.
package sdk

import (
	"context"
	"crypto/tls"
	"fmt"

	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/metadata"

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
}

// Option configures the Client.
type Option func(*options)

type options struct {
	insecure bool
	tls      *tls.Config
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

// NewClient dials endpoint and prepares a Client. bearer is the bearer
// token issued by AdminCredentials.Issue; it is sent on every RPC.
func NewClient(endpoint, bearer string, opts ...Option) (*Client, error) {
	if bearer == "" {
		return nil, fmt.Errorf("sdk: bearer token is required")
	}

	conn, err := Dial(endpoint, opts...)
	if err != nil {
		return nil, err
	}
	return &Client{
		conn:     conn,
		stub:     evidencev1.NewEvidenceIngestServiceClient(conn),
		bearer:   bearer,
		ownsConn: true,
	}, nil
}

// Dial opens a gRPC connection to endpoint with the transport the options
// select: TLS (system roots, TLS 1.2+) by default, or plaintext under
// WithInsecure, which refuses any endpoint IsLoopbackEndpoint rejects.
// NewClient dials through it, and so does every connector's
// ConnectorRegistry dial, so --insecure is loopback-only on every gRPC path
// and not only on the push path (#2257). The caller owns the returned conn.
func Dial(endpoint string, opts ...Option) (*grpc.ClientConn, error) {
	o := options{}
	for _, opt := range opts {
		opt(&o)
	}

	var transport grpc.DialOption
	switch {
	case o.insecure:
		if !IsLoopbackEndpoint(endpoint) {
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
	return conn, nil
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
	resp, err := c.stub.Push(ctx, &evidencev1.PushRequest{Record: record})
	if err != nil {
		return nil, fmt.Errorf("sdk: push: %w", err)
	}
	return resp.GetReceipt(), nil
}

// NewClientFromConn builds a Client around an existing grpc.ClientConn
// (typical in tests using bufconn). The Client does not own the conn; its
// Close() is a no-op so the caller can close their own.
func NewClientFromConn(conn *grpc.ClientConn, bearer string) *Client {
	return &Client{
		conn:     conn,
		stub:     evidencev1.NewEvidenceIngestServiceClient(conn),
		bearer:   bearer,
		ownsConn: false,
	}
}
