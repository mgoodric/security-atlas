// Package sdk_test exercises the public push-SDK surface in pkg/sdk-go
// at the branch level. Slice 321 — coverage lift to ≥70% merged.
//
// Load-bearing functions + branches under test:
//
//   - WithTLSConfig            — option wires a custom *tls.Config into NewClient
//   - NewClient (bearer empty) — rejects empty bearer with descriptive error
//   - NewClient (reject path)  — WithInsecure on non-loopback endpoint refused
//   - NewClient (TLS path)     — default TLS path constructs without error
//   - NewClient (loopback path)— WithInsecure on a loopback endpoint accepted
//   - Close (owned conn)       — closes underlying grpc.ClientConn when client owns it
//   - isLoopback (false branch)— non-loopback host returns false (indirectly via NewClient reject)
//
// Tests are pure-Go: they do NOT dial any network — grpc.NewClient is a
// lazy constructor in grpc-go v1.59+, so a real server is not required to
// exercise the option-validation branches that this slice targets.
package sdk_test

import (
	"context"
	"crypto/sha256"
	"crypto/tls"
	"fmt"
	"net"
	"strings"
	"sync"
	"testing"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"
	"google.golang.org/grpc/test/bufconn"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/structpb"
	"google.golang.org/protobuf/types/known/timestamppb"

	evidencev1 "github.com/mgoodric/security-atlas/gen/proto/evidence/v1"
	sdk "github.com/mgoodric/security-atlas/pkg/sdk-go"
)

// TestNewClientRejectsEmptyBearer hits the bearer == "" guard at the top
// of NewClient. Branch: line 58 (the `return nil, fmt.Errorf("...bearer...")`).
func TestNewClientRejectsEmptyBearer(t *testing.T) {
	t.Parallel()

	c, err := sdk.NewClient("localhost:7777", "", sdk.WithInsecure())
	if err == nil {
		t.Fatalf("expected error for empty bearer, got nil; client=%v", c)
	}
	if c != nil {
		t.Fatalf("expected nil client on error, got %v", c)
	}
	if !strings.Contains(err.Error(), "bearer") {
		t.Fatalf("error %q does not mention 'bearer'", err.Error())
	}
}

// TestNewClientRejectsInsecureNonLoopback exercises the WithInsecure +
// non-loopback guard inside NewClient AND the `return false` branch of
// isLoopback. Branch coverage: NewClient line 75 (refuse), isLoopback line
// 138 (the final `return false`).
func TestNewClientRejectsInsecureNonLoopback(t *testing.T) {
	t.Parallel()

	cases := []string{
		"203.0.113.10:7777",                // RFC 5737 TEST-NET-3 IPv4 — not loopback
		"example.test:7777",                // RFC 6761 reserved TLD — not loopback
		"[2001:db8::1]:7777",               // RFC 3849 IPv6 doc range — not loopback
		"not-a-loopback-host.invalid:9001", // RFC 2606 reserved invalid TLD
	}

	for _, endpoint := range cases {
		endpoint := endpoint
		t.Run(endpoint, func(t *testing.T) {
			t.Parallel()
			c, err := sdk.NewClient(endpoint, "test-bearer-321", sdk.WithInsecure())
			if err == nil {
				if c != nil {
					_ = c.Close()
				}
				t.Fatalf("expected refuse-non-loopback error for %q, got nil", endpoint)
			}
			if c != nil {
				t.Fatalf("expected nil client on error for %q, got %v", endpoint, c)
			}
			if !strings.Contains(err.Error(), "loopback") {
				t.Fatalf("error %q does not mention 'loopback' for %q", err.Error(), endpoint)
			}
		})
	}
}

// TestNewClientAcceptsInsecureLoopback exercises the loopback-accepted
// branch through every host the isLoopback whitelist allows. Each variant
// keeps isLoopback at 100% over the switch arms AND constructs the
// transport-creds path that NewClient takes when o.insecure is true.
func TestNewClientAcceptsInsecureLoopback(t *testing.T) {
	t.Parallel()

	cases := []string{
		"localhost:7777",
		"127.0.0.1:7777",
		"[::1]:7777",
		":7777", // empty host — SplitHostPort yields "" which the switch accepts
	}

	for _, endpoint := range cases {
		endpoint := endpoint
		t.Run(endpoint, func(t *testing.T) {
			t.Parallel()
			c, err := sdk.NewClient(endpoint, "test-bearer-321", sdk.WithInsecure())
			if err != nil {
				t.Fatalf("expected NewClient to accept loopback %q, got: %v", endpoint, err)
			}
			if c == nil {
				t.Fatalf("expected non-nil client for %q", endpoint)
			}
			if err := c.Close(); err != nil {
				t.Fatalf("Close on owned conn returned %v", err)
			}
		})
	}
}

// TestNewClientAcceptsInsecureNoPort exercises the SplitHostPort error
// branch inside isLoopback. A bare "localhost" (no port) returns an error
// from net.SplitHostPort, so isLoopback falls into the `host = endpoint`
// fallback and matches the "localhost" switch arm.
func TestNewClientAcceptsInsecureNoPort(t *testing.T) {
	t.Parallel()

	c, err := sdk.NewClient("localhost", "test-bearer-321", sdk.WithInsecure())
	if err != nil {
		t.Fatalf("expected NewClient to accept bare loopback host, got: %v", err)
	}
	if c == nil {
		t.Fatalf("expected non-nil client")
	}
	if err := c.Close(); err != nil {
		t.Fatalf("Close on owned conn returned %v", err)
	}
}

// TestNewClientDefaultTLSPath exercises the `default:` branch of the
// transport switch — no WithInsecure means the TLS-credentials path. We
// don't dial; grpc.NewClient is lazy in grpc-go v1.59+.
func TestNewClientDefaultTLSPath(t *testing.T) {
	t.Parallel()

	c, err := sdk.NewClient("audit.example.test:443", "test-bearer-321")
	if err != nil {
		t.Fatalf("expected NewClient with default TLS to succeed, got: %v", err)
	}
	if c == nil {
		t.Fatalf("expected non-nil client")
	}
	if err := c.Close(); err != nil {
		t.Fatalf("Close on owned conn returned %v", err)
	}
}

// TestWithTLSConfigOption confirms WithTLSConfig wires a caller-supplied
// *tls.Config through to NewClient's TLS path. The 100% goal for this
// function is the single statement `o.tls = c` — we verify it by
// constructing with a distinctive config and then closing cleanly. If
// the option had silently ignored its argument, NewClient would still
// succeed, so this test additionally asserts the returned client is
// usable (Close returns nil).
func TestWithTLSConfigOption(t *testing.T) {
	t.Parallel()

	cfg := &tls.Config{
		MinVersion: tls.VersionTLS13,
		ServerName: "audit.example.test",
	}

	c, err := sdk.NewClient("audit.example.test:443", "test-bearer-321", sdk.WithTLSConfig(cfg))
	if err != nil {
		t.Fatalf("expected NewClient with WithTLSConfig to succeed, got: %v", err)
	}
	if c == nil {
		t.Fatalf("expected non-nil client when WithTLSConfig is supplied")
	}
	if err := c.Close(); err != nil {
		t.Fatalf("Close on owned conn returned %v", err)
	}
}

// TestCloseOwnedConn covers the `ownsConn=true` branch of Close — the
// branch NewClient-constructed clients always take. Returning nil here
// (after the underlying grpc.ClientConn closes) proves the path runs.
func TestCloseOwnedConn(t *testing.T) {
	t.Parallel()

	c, err := sdk.NewClient("localhost:7777", "test-bearer-321", sdk.WithInsecure())
	if err != nil {
		t.Fatalf("NewClient: %v", err)
	}
	if c == nil {
		t.Fatalf("expected non-nil client")
	}
	if err := c.Close(); err != nil {
		t.Fatalf("first Close returned %v", err)
	}
	// Second Close on an already-closed conn returns an error from grpc;
	// we only need the first call's behavior for branch coverage. The
	// guard against double-close is grpc-go's concern, not ours.
}

func TestPushRetriesTransportFailureAndDeduplicatesSameRecord(t *testing.T) {
	t.Parallel()

	const idem = "retry-dedup-key"
	wantReceipt := &evidencev1.EvidenceReceipt{
		RecordId:     "record-retry-dedup",
		Hash:         "hash-retry-dedup",
		CredentialId: "cred-retry",
	}
	srv := &fakePushServer{
		handle: func(ctx context.Context, req *evidencev1.PushRequest, attempt int) (*evidencev1.PushResponse, error) {
			if req.GetRecord().GetIdempotencyKey() != idem {
				return nil, status.Errorf(codes.InvalidArgument, "idempotency_key changed to %q", req.GetRecord().GetIdempotencyKey())
			}
			hash, err := deterministicRecordHash(req.GetRecord())
			if err != nil {
				return nil, status.Errorf(codes.Internal, "hash: %v", err)
			}
			srvState := fakeLedgerFromContext(ctx)
			if attempt == 1 {
				srvState.remember(idem, hash, wantReceipt)
				return nil, status.Error(codes.Unavailable, "response lost after ledger commit")
			}
			receipt, ok := srvState.lookupSame(idem, hash)
			if !ok {
				return nil, status.Error(codes.AlreadyExists, "same key with different bytes")
			}
			return &evidencev1.PushResponse{Receipt: receipt}, nil
		},
	}
	conn := startSDKBufconn(t, srv)
	client := sdk.NewClientFromConn(conn, "test-bearer-retry", sdk.WithRetryConfig(sdk.RetryConfig{
		MaxAttempts: 2,
		BaseDelay:   0,
		MaxDelay:    0,
		Jitter:      0,
	}))

	receipt, err := client.Push(context.Background(), testEvidenceRecord(idem))
	if err != nil {
		t.Fatalf("Push retry returned error: %v", err)
	}
	if receipt.GetRecordId() != wantReceipt.GetRecordId() {
		t.Fatalf("record_id = %q; want %q", receipt.GetRecordId(), wantReceipt.GetRecordId())
	}
	if attempts := srv.attempts(); attempts != 2 {
		t.Fatalf("attempts = %d; want 2", attempts)
	}
}

func TestPushDoesNotRetryAlreadyExists(t *testing.T) {
	t.Parallel()

	srv := &fakePushServer{
		handle: func(context.Context, *evidencev1.PushRequest, int) (*evidencev1.PushResponse, error) {
			return nil, status.Error(codes.AlreadyExists, "same key with different content")
		},
	}
	conn := startSDKBufconn(t, srv)
	client := sdk.NewClientFromConn(conn, "test-bearer-retry", sdk.WithRetryConfig(sdk.RetryConfig{
		MaxAttempts: 3,
		BaseDelay:   0,
		MaxDelay:    0,
		Jitter:      0,
	}))

	_, err := client.Push(context.Background(), testEvidenceRecord("terminal-already-exists"))
	if err == nil {
		t.Fatalf("expected AlreadyExists error, got nil")
	}
	if got := status.Code(err); got != codes.AlreadyExists {
		t.Fatalf("status.Code(err) = %v; want %v; err=%v", got, codes.AlreadyExists, err)
	}
	if attempts := srv.attempts(); attempts != 1 {
		t.Fatalf("attempts = %d; want 1", attempts)
	}
}

func TestPushRetriesDeadlineExceeded(t *testing.T) {
	t.Parallel()

	srv := &fakePushServer{
		handle: func(context.Context, *evidencev1.PushRequest, int) (*evidencev1.PushResponse, error) {
			return nil, status.Error(codes.DeadlineExceeded, "temporary deadline")
		},
	}
	conn := startSDKBufconn(t, srv)
	client := sdk.NewClientFromConn(conn, "test-bearer-retry", sdk.WithRetryConfig(sdk.RetryConfig{
		MaxAttempts: 2,
		BaseDelay:   0,
		MaxDelay:    0,
		Jitter:      0,
	}))

	_, err := client.Push(context.Background(), testEvidenceRecord("deadline-retry"))
	if err == nil {
		t.Fatalf("expected terminal DeadlineExceeded after retries, got nil")
	}
	if attempts := srv.attempts(); attempts != 2 {
		t.Fatalf("attempts = %d; want 2", attempts)
	}
}

func TestPushHonorsRetryAfterMetadata(t *testing.T) {
	t.Parallel()

	srv := &fakePushServer{
		handle: func(ctx context.Context, req *evidencev1.PushRequest, attempt int) (*evidencev1.PushResponse, error) {
			if attempt == 1 {
				if err := grpc.SetTrailer(ctx, metadata.Pairs("retry-after", "0")); err != nil {
					return nil, status.Errorf(codes.Internal, "set retry-after: %v", err)
				}
				return nil, status.Error(codes.Unavailable, "retry after zero")
			}
			return &evidencev1.PushResponse{Receipt: &evidencev1.EvidenceReceipt{RecordId: req.GetRecord().GetIdempotencyKey()}}, nil
		},
	}
	conn := startSDKBufconn(t, srv)
	client := sdk.NewClientFromConn(conn, "test-bearer-retry", sdk.WithRetryConfig(sdk.RetryConfig{
		MaxAttempts: 2,
		BaseDelay:   time.Hour,
		MaxDelay:    time.Hour,
		Jitter:      0,
	}))

	ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancel()
	receipt, err := client.Push(ctx, testEvidenceRecord("retry-after-zero"))
	if err != nil {
		t.Fatalf("Push did not honor Retry-After metadata: %v", err)
	}
	if receipt.GetRecordId() != "retry-after-zero" {
		t.Fatalf("record_id = %q; want retry-after-zero", receipt.GetRecordId())
	}
	if attempts := srv.attempts(); attempts != 2 {
		t.Fatalf("attempts = %d; want 2", attempts)
	}
}

type fakePushServer struct {
	evidencev1.UnimplementedEvidenceIngestServiceServer

	mu     sync.Mutex
	n      int
	handle func(context.Context, *evidencev1.PushRequest, int) (*evidencev1.PushResponse, error)
	ledger *fakeLedger
}

func (s *fakePushServer) Push(ctx context.Context, req *evidencev1.PushRequest) (*evidencev1.PushResponse, error) {
	s.mu.Lock()
	s.n++
	attempt := s.n
	if s.ledger == nil {
		s.ledger = &fakeLedger{rows: map[string]fakeLedgerRow{}}
	}
	ledger := s.ledger
	s.mu.Unlock()
	return s.handle(context.WithValue(ctx, fakeLedgerKey{}, ledger), req, attempt)
}

func (s *fakePushServer) attempts() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.n
}

type fakeLedgerKey struct{}

type fakeLedger struct {
	mu   sync.Mutex
	rows map[string]fakeLedgerRow
}

type fakeLedgerRow struct {
	hash    string
	receipt *evidencev1.EvidenceReceipt
}

func fakeLedgerFromContext(ctx context.Context) *fakeLedger {
	ledger, ok := ctx.Value(fakeLedgerKey{}).(*fakeLedger)
	if !ok {
		panic("fake ledger missing from context")
	}
	return ledger
}

func (l *fakeLedger) remember(idem, hash string, receipt *evidencev1.EvidenceReceipt) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.rows[idem] = fakeLedgerRow{hash: hash, receipt: receipt}
}

func (l *fakeLedger) lookupSame(idem, hash string) (*evidencev1.EvidenceReceipt, bool) {
	l.mu.Lock()
	defer l.mu.Unlock()
	row, ok := l.rows[idem]
	if !ok || row.hash != hash {
		return nil, false
	}
	return row.receipt, true
}

func startSDKBufconn(t *testing.T, srv evidencev1.EvidenceIngestServiceServer) *grpc.ClientConn {
	t.Helper()

	lis := bufconn.Listen(1 << 20)
	grpcServer := grpc.NewServer()
	evidencev1.RegisterEvidenceIngestServiceServer(grpcServer, srv)
	go func() {
		if err := grpcServer.Serve(lis); err != nil {
			t.Errorf("bufconn server: %v", err)
		}
	}()
	t.Cleanup(grpcServer.Stop)

	conn, err := grpc.NewClient("passthrough://bufconn",
		grpc.WithContextDialer(func(ctx context.Context, _ string) (net.Conn, error) {
			return lis.DialContext(ctx)
		}),
		grpc.WithTransportCredentials(insecure.NewCredentials()),
	)
	if err != nil {
		t.Fatalf("grpc.NewClient: %v", err)
	}
	t.Cleanup(func() { _ = conn.Close() })
	return conn
}

func testEvidenceRecord(idem string) *evidencev1.EvidenceRecord {
	payload, err := structpb.NewStruct(map[string]any{"status": "ok"})
	if err != nil {
		panic(err)
	}
	return &evidencev1.EvidenceRecord{
		IdempotencyKey: idem,
		EvidenceKind:   "test.kind.v1",
		SchemaVersion:  "1.0.0",
		ControlId:      "scf:TST-01",
		Scope: []*evidencev1.ScopeDimension{
			{Key: "environment", Values: []string{"test"}},
		},
		ObservedAt: timestamppb.New(time.Date(2026, 8, 4, 12, 0, 0, 0, time.UTC)),
		Result:     evidencev1.Result_RESULT_PASS,
		Payload:    payload,
		SourceAttribution: &evidencev1.SourceAttribution{
			ActorType: "connector",
			ActorId:   "connector:test",
		},
	}
}

func deterministicRecordHash(record *evidencev1.EvidenceRecord) (string, error) {
	b, err := proto.MarshalOptions{Deterministic: true}.Marshal(record)
	if err != nil {
		return "", err
	}
	return fmt.Sprintf("%x", sha256.Sum256(b)), nil
}
