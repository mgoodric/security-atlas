package api_test

import (
	"context"
	"errors"
	"net"
	"testing"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"
	"google.golang.org/grpc/test/bufconn"
	"google.golang.org/protobuf/types/known/durationpb"
	"google.golang.org/protobuf/types/known/structpb"
	"google.golang.org/protobuf/types/known/timestamppb"

	adminv1 "github.com/mgoodric/security-atlas/gen/proto/admin/v1"
	evidencev1 "github.com/mgoodric/security-atlas/gen/proto/evidence/v1"
	"github.com/mgoodric/security-atlas/internal/api"
	"github.com/mgoodric/security-atlas/internal/canonjson"
	sdk "github.com/mgoodric/security-atlas/pkg/sdk-go"
)

const tenantA = "11111111-1111-1111-1111-111111111111"

// testHarness boots an in-process gRPC server on bufconn and returns it
// with a pre-issued bearer token for tenantA.
type testHarness struct {
	srv    *api.Server
	dialer func(context.Context, string) (net.Conn, error)
	bearer string
	credID string
	tenant string
}

func newHarness(t *testing.T) *testHarness {
	t.Helper()

	srv := api.New(api.Config{RotationGrace: time.Hour})

	listener := bufconn.Listen(1 << 20)
	go func() { _ = srv.GRPC.Serve(listener) }()
	t.Cleanup(func() {
		srv.GRPC.GracefulStop()
		_ = listener.Close()
	})

	cred, bearer, err := srv.IssueBootstrapCredential(tenantA)
	if err != nil {
		t.Fatalf("IssueBootstrapCredential: %v", err)
	}
	return &testHarness{
		srv:    srv,
		dialer: func(ctx context.Context, _ string) (net.Conn, error) { return listener.DialContext(ctx) },
		bearer: bearer,
		credID: cred.ID,
		tenant: tenantA,
	}
}

func (h *testHarness) evidenceClient(t *testing.T) (evidencev1.EvidenceIngestServiceClient, func()) {
	t.Helper()
	conn := h.dial(t)
	return evidencev1.NewEvidenceIngestServiceClient(conn), func() { _ = conn.Close() }
}

func (h *testHarness) adminClient(t *testing.T) (adminv1.AdminCredentialsServiceClient, func()) {
	t.Helper()
	conn := h.dial(t)
	return adminv1.NewAdminCredentialsServiceClient(conn), func() { _ = conn.Close() }
}

func (h *testHarness) dial(t *testing.T) *grpc.ClientConn {
	t.Helper()
	conn, err := grpc.NewClient(
		"passthrough://bufconn",
		grpc.WithTransportCredentials(insecure.NewCredentials()),
		grpc.WithContextDialer(h.dialer),
	)
	if err != nil {
		t.Fatalf("grpc.NewClient: %v", err)
	}
	return conn
}

// bearerCtx returns a context with the bearer attached. No deadline —
// tests are fast and gRPC's own timeouts apply.
func bearerCtx(token string) context.Context {
	return metadata.AppendToOutgoingContext(context.Background(), "authorization", "Bearer "+token)
}

func goodRecord() *evidencev1.EvidenceRecord {
	payload, _ := structpb.NewStruct(map[string]any{"tool": "semgrep"})
	return &evidencev1.EvidenceRecord{
		IdempotencyKey: "ci-1",
		EvidenceKind:   "sast.scan_result.v1",
		SchemaVersion:  "1.0.0",
		ControlId:      "scf:VPM-04",
		Scope: []*evidencev1.ScopeDimension{
			{Key: "environment", Values: []string{"prod"}},
		},
		ObservedAt:        timestamppb.New(time.Date(2026, 5, 10, 12, 0, 0, 0, time.UTC)),
		Result:            evidencev1.Result_RESULT_PASS,
		Payload:           payload,
		SourceAttribution: &evidencev1.SourceAttribution{ActorType: "service_account", ActorId: "ci.test"},
	}
}

// AC-2 + AC-6: a successful push returns a receipt with record_id and
// hash equal to canonjson.HashRecord(record).
func TestEvidencePush_SuccessReceiptHashMatchesCanonical(t *testing.T) {
	h := newHarness(t)
	stub, done := h.evidenceClient(t)
	defer done()

	rec := goodRecord()
	resp, err := stub.Push(bearerCtx(h.bearer), &evidencev1.PushRequest{Record: rec})
	if err != nil {
		t.Fatalf("Push: %v", err)
	}
	if resp.GetReceipt().GetRecordId() == "" {
		t.Fatal("receipt.record_id is empty")
	}
	expected, err := canonjson.HashRecord(rec)
	if err != nil {
		t.Fatalf("HashRecord: %v", err)
	}
	if resp.GetReceipt().GetHash() != expected {
		t.Fatalf("hash mismatch: got %q want %q", resp.GetReceipt().GetHash(), expected)
	}
	if resp.GetReceipt().GetCredentialId() != h.credID {
		t.Fatalf("credential_id mismatch: got %q want %q", resp.GetReceipt().GetCredentialId(), h.credID)
	}
}

// AC-5: dedup returns the original receipt for same idempotency_key + same content.
func TestEvidencePush_IdempotentDedup(t *testing.T) {
	h := newHarness(t)
	stub, done := h.evidenceClient(t)
	defer done()

	rec := goodRecord()
	first, err := stub.Push(bearerCtx(h.bearer), &evidencev1.PushRequest{Record: rec})
	if err != nil {
		t.Fatalf("Push first: %v", err)
	}
	second, err := stub.Push(bearerCtx(h.bearer), &evidencev1.PushRequest{Record: rec})
	if err != nil {
		t.Fatalf("Push second: %v", err)
	}
	if first.GetReceipt().GetRecordId() != second.GetReceipt().GetRecordId() {
		t.Fatalf("dedup failed: %q vs %q", first.GetReceipt().GetRecordId(), second.GetReceipt().GetRecordId())
	}
}

// AC-5 mismatch: same idempotency_key + different content → AlreadyExists.
func TestEvidencePush_IdempotencyMismatchRejected(t *testing.T) {
	h := newHarness(t)
	stub, done := h.evidenceClient(t)
	defer done()

	rec := goodRecord()
	if _, err := stub.Push(bearerCtx(h.bearer), &evidencev1.PushRequest{Record: rec}); err != nil {
		t.Fatalf("first push: %v", err)
	}

	tampered := goodRecord()
	tampered.Result = evidencev1.Result_RESULT_FAIL
	_, err := stub.Push(bearerCtx(h.bearer), &evidencev1.PushRequest{Record: tampered})
	if status.Code(err) != codes.AlreadyExists {
		t.Fatalf("expected AlreadyExists, got %v", err)
	}
}

// AC-4 + Anti-A4: unregistered evidence_kind → FailedPrecondition.
func TestEvidencePush_UnregisteredKindRejected(t *testing.T) {
	h := newHarness(t)
	stub, done := h.evidenceClient(t)
	defer done()

	rec := goodRecord()
	rec.EvidenceKind = "not.registered.v1"
	_, err := stub.Push(bearerCtx(h.bearer), &evidencev1.PushRequest{Record: rec})
	if status.Code(err) != codes.FailedPrecondition {
		t.Fatalf("expected FailedPrecondition, got %v", err)
	}
}

// Anti-A1: anonymous push → Unauthenticated.
func TestEvidencePush_AnonymousRejected(t *testing.T) {
	h := newHarness(t)
	stub, done := h.evidenceClient(t)
	defer done()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	_, err := stub.Push(ctx, &evidencev1.PushRequest{Record: goodRecord()})
	if status.Code(err) != codes.Unauthenticated {
		t.Fatalf("expected Unauthenticated, got %v", err)
	}
}

// Anti-A2: missing evidence_kind → InvalidArgument.
func TestEvidencePush_MissingKindRejected(t *testing.T) {
	h := newHarness(t)
	stub, done := h.evidenceClient(t)
	defer done()

	rec := goodRecord()
	rec.EvidenceKind = ""
	_, err := stub.Push(bearerCtx(h.bearer), &evidencev1.PushRequest{Record: rec})
	if status.Code(err) != codes.InvalidArgument {
		t.Fatalf("expected InvalidArgument, got %v", err)
	}
}

// Anti-A3: empty scope → InvalidArgument.
func TestEvidencePush_EmptyScopeRejected(t *testing.T) {
	h := newHarness(t)
	stub, done := h.evidenceClient(t)
	defer done()

	rec := goodRecord()
	rec.Scope = nil
	_, err := stub.Push(bearerCtx(h.bearer), &evidencev1.PushRequest{Record: rec})
	if status.Code(err) != codes.InvalidArgument {
		t.Fatalf("expected InvalidArgument, got %v", err)
	}
}

// AC-7 + AC-10: Issue returns bearer; List does not.
func TestCredentials_IssueReturnsTokenListDoesNot(t *testing.T) {
	h := newHarness(t)
	stub, done := h.adminClient(t)
	defer done()

	issued, err := stub.Issue(bearerCtx(h.bearer), &adminv1.IssueRequest{
		TenantId: h.tenant,
		Kinds:    []string{"sast.scan_result.v1"},
		Ttl:      durationpb.New(time.Hour),
	})
	if err != nil {
		t.Fatalf("Issue: %v", err)
	}
	if issued.GetBearerToken() == "" {
		t.Fatal("Issue returned empty bearer_token")
	}

	listed, err := stub.List(bearerCtx(h.bearer), &adminv1.ListRequest{TenantId: h.tenant})
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	for _, h := range listed.GetHandles() {
		// ListResponse has no bearer field at all — but a future field
		// drift would surface here. Spot-check id and last_4.
		if h.GetId() == "" {
			t.Errorf("handle missing id")
		}
		if h.GetLast_4() == "" {
			t.Errorf("handle missing last_4")
		}
	}
}

// AC-9: revoke → push 401.
func TestCredentials_RevokeInvalidatesImmediately(t *testing.T) {
	h := newHarness(t)
	admin, doneAdmin := h.adminClient(t)
	defer doneAdmin()
	evid, doneEvid := h.evidenceClient(t)
	defer doneEvid()

	issued, err := admin.Issue(bearerCtx(h.bearer), &adminv1.IssueRequest{TenantId: h.tenant})
	if err != nil {
		t.Fatalf("Issue: %v", err)
	}
	victim := issued.GetBearerToken()
	id := issued.GetHandle().GetId()

	// Sanity: victim can push.
	if _, err := evid.Push(bearerCtx(victim), &evidencev1.PushRequest{Record: goodRecord()}); err != nil {
		t.Fatalf("baseline push with victim: %v", err)
	}

	if _, err := admin.Revoke(bearerCtx(h.bearer), &adminv1.RevokeRequest{Id: id}); err != nil {
		t.Fatalf("Revoke: %v", err)
	}

	// Re-attempt with revoked bearer.
	rec := goodRecord()
	rec.IdempotencyKey = "ci-after-revoke"
	_, err = evid.Push(bearerCtx(victim), &evidencev1.PushRequest{Record: rec})
	if status.Code(err) != codes.Unauthenticated {
		t.Fatalf("expected Unauthenticated after revoke, got %v", err)
	}
}

// AC-8: rotate → both old and new bearers work until predecessor expires.
func TestCredentials_RotateBothWorkUntilGraceExpires(t *testing.T) {
	h := newHarness(t)
	admin, doneAdmin := h.adminClient(t)
	defer doneAdmin()
	evid, doneEvid := h.evidenceClient(t)
	defer doneEvid()

	issued, err := admin.Issue(bearerCtx(h.bearer), &adminv1.IssueRequest{TenantId: h.tenant})
	if err != nil {
		t.Fatalf("Issue: %v", err)
	}
	oldBearer := issued.GetBearerToken()
	oldID := issued.GetHandle().GetId()

	rotated, err := admin.Rotate(bearerCtx(h.bearer), &adminv1.RotateRequest{Id: oldID})
	if err != nil {
		t.Fatalf("Rotate: %v", err)
	}
	newBearer := rotated.GetBearerToken()
	if newBearer == oldBearer {
		t.Fatal("Rotate returned same token as predecessor")
	}
	if rotated.GetPredecessorExpiresAt() == nil {
		t.Fatal("RotateResponse missing predecessor_expires_at")
	}

	// Both should work.
	rec1 := goodRecord()
	rec1.IdempotencyKey = "ci-old-key"
	if _, err := evid.Push(bearerCtx(oldBearer), &evidencev1.PushRequest{Record: rec1}); err != nil {
		t.Fatalf("push with old bearer: %v", err)
	}
	rec2 := goodRecord()
	rec2.IdempotencyKey = "ci-new-key"
	if _, err := evid.Push(bearerCtx(newBearer), &evidencev1.PushRequest{Record: rec2}); err != nil {
		t.Fatalf("push with new bearer: %v", err)
	}
}

// Bearer-prefix variations should be rejected as Unauthenticated.
func TestAuth_MalformedAuthorizationRejected(t *testing.T) {
	h := newHarness(t)
	stub, done := h.evidenceClient(t)
	defer done()

	cases := []string{"", "Bearer", "Bearer ", "Token " + h.bearer, "  "}
	for _, raw := range cases {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		if raw != "" {
			ctx = metadata.AppendToOutgoingContext(ctx, "authorization", raw)
		}
		_, err := stub.Push(ctx, &evidencev1.PushRequest{Record: goodRecord()})
		cancel()
		if status.Code(err) != codes.Unauthenticated {
			t.Errorf("authorization=%q: expected Unauthenticated, got %v", raw, err)
		}
	}
}

// SDK client wraps gRPC errors with %w so errors.As recovers the status.
func TestSDK_ErrorWrapping(t *testing.T) {
	h := newHarness(t)
	client := sdk.NewClientFromConn(h.dial(t), h.bearer)

	rec := goodRecord()
	rec.EvidenceKind = ""
	_, err := client.Push(context.Background(), rec)
	if err == nil {
		t.Fatal("expected error")
	}

	var st interface{ GRPCStatus() *status.Status }
	if !errors.As(err, &st) {
		t.Fatalf("error does not unwrap to gRPC status: %v", err)
	}
	if st.GRPCStatus().Code() != codes.InvalidArgument {
		t.Fatalf("wrapped code = %v, want InvalidArgument", st.GRPCStatus().Code())
	}
}
