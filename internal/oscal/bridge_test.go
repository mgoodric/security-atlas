// Slice 285 unit tests for the grpcBridge gRPC client wrapper:
//
//   - DialBridge                  (constructor)
//   - grpcBridge.SerializeSSP     (unary RPC + happy + error path)
//   - grpcBridge.SerializeAssessment (unary RPC + happy + error path)
//   - grpcBridge.SerializePOAM    (unary RPC + happy + error path)
//   - grpcBridge.RoundTripValidate (unary RPC + happy + error path)
//   - grpcBridge.Close            (idempotent close)
//
// These wrappers were all 0% covered in slice 279's audit. They are
// pure plumbing (timeout context + unary call + response unwrap), but
// the unwrap is load-bearing on the export pipeline — every wire-call
// failure path lands here. A bufconn-backed in-memory gRPC server runs
// the test bridge stub; no Python, no TCP, no fixture process.
//
// Branches covered:
//
//   - DialBridge with a valid address returns a *grpcBridge (lazy-dial:
//     grpc.NewClient does not actually contact the server until first
//     RPC, so this is a constructor check only).
//   - Each Serialize* method: happy path returns the response bytes;
//     server-side error propagates as a non-nil error.
//   - RoundTripValidate: happy path returns (valid, errs); server-side
//     error propagates.
//   - Close on a nil-conn grpcBridge returns nil (idempotent).
package oscal

import (
	"context"
	"errors"
	"net"
	"testing"

	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/test/bufconn"

	oscalv1 "github.com/mgoodric/security-atlas/gen/proto/oscal/v1"
)

// stubBridgeServer is an in-memory OscalBridgeService implementation
// used by the bufconn tests. Each test pins responses + errors per RPC.
type stubBridgeServer struct {
	oscalv1.UnimplementedOscalBridgeServiceServer
	sspResp    *oscalv1.SerializeSSPResponse
	sspErr     error
	assessResp *oscalv1.SerializeAssessmentResponse
	assessErr  error
	poamResp   *oscalv1.SerializePOAMResponse
	poamErr    error
	rtResp     *oscalv1.RoundTripValidateResponse
	rtErr      error
}

func (s *stubBridgeServer) SerializeSSP(_ context.Context, _ *oscalv1.SerializeSSPRequest) (*oscalv1.SerializeSSPResponse, error) {
	if s.sspErr != nil {
		return nil, s.sspErr
	}
	return s.sspResp, nil
}

func (s *stubBridgeServer) SerializeAssessment(_ context.Context, _ *oscalv1.SerializeAssessmentRequest) (*oscalv1.SerializeAssessmentResponse, error) {
	if s.assessErr != nil {
		return nil, s.assessErr
	}
	return s.assessResp, nil
}

func (s *stubBridgeServer) SerializePOAM(_ context.Context, _ *oscalv1.SerializePOAMRequest) (*oscalv1.SerializePOAMResponse, error) {
	if s.poamErr != nil {
		return nil, s.poamErr
	}
	return s.poamResp, nil
}

func (s *stubBridgeServer) RoundTripValidate(_ context.Context, _ *oscalv1.RoundTripValidateRequest) (*oscalv1.RoundTripValidateResponse, error) {
	if s.rtErr != nil {
		return nil, s.rtErr
	}
	return s.rtResp, nil
}

// startBufconnBridge wires a stubBridgeServer onto an in-memory bufconn
// listener and returns a connected grpcBridge plus a cleanup func. The
// pattern mirrors slice 003's connectors/*/cmd/atlas-*/integration_test.go
// helpers, scoped down for a single test invocation.
func startBufconnBridge(t *testing.T, stub *stubBridgeServer) *grpcBridge {
	t.Helper()
	lis := bufconn.Listen(1 << 16)
	srv := grpc.NewServer()
	oscalv1.RegisterOscalBridgeServiceServer(srv, stub)
	go func() { _ = srv.Serve(lis) }()
	t.Cleanup(func() {
		srv.GracefulStop()
		_ = lis.Close()
	})

	conn, err := grpc.NewClient(
		"passthrough://bufnet",
		grpc.WithContextDialer(func(_ context.Context, _ string) (net.Conn, error) {
			return lis.DialContext(context.Background())
		}),
		grpc.WithTransportCredentials(insecure.NewCredentials()),
	)
	if err != nil {
		t.Fatalf("grpc.NewClient: %v", err)
	}
	t.Cleanup(func() { _ = conn.Close() })

	return &grpcBridge{conn: conn, client: oscalv1.NewOscalBridgeServiceClient(conn)}
}

func TestDialBridge_ReturnsClientForValidAddress(t *testing.T) {
	// grpc.NewClient is lazy: it does not actually connect until the
	// first RPC. So DialBridge with any syntactically valid address
	// returns a non-nil client. We immediately close it to exercise the
	// Close() path on a real conn.
	c, err := DialBridge("127.0.0.1:65535")
	if err != nil {
		t.Fatalf("DialBridge: %v", err)
	}
	if c == nil {
		t.Fatal("DialBridge returned nil client")
	}
	if err := c.Close(); err != nil {
		t.Errorf("Close: %v", err)
	}
}

func TestGRPCBridge_SerializeSSP_HappyAndErrorPaths(t *testing.T) {
	want := []byte(`{"system-security-plan":{"uuid":"x"}}`)
	stub := &stubBridgeServer{
		sspResp: &oscalv1.SerializeSSPResponse{OscalJson: want},
	}
	b := startBufconnBridge(t, stub)

	got, err := b.SerializeSSP(context.Background(), &oscalv1.SspInput{})
	if err != nil {
		t.Fatalf("SerializeSSP: %v", err)
	}
	if string(got) != string(want) {
		t.Errorf("SerializeSSP bytes = %q, want %q", got, want)
	}

	// Error path: stub returns an error -> wrapper propagates verbatim
	// (no error transformation in the wrapper itself; the Exporter is
	// where ErrBridgeUnavailable wrapping happens).
	stub.sspErr = errors.New("bridge boom")
	if _, err := b.SerializeSSP(context.Background(), &oscalv1.SspInput{}); err == nil {
		t.Fatal("SerializeSSP must propagate the server-side error")
	}
}

func TestGRPCBridge_SerializeAssessment_HappyAndErrorPaths(t *testing.T) {
	wantAP := []byte(`{"assessment-plan":{"uuid":"x"}}`)
	wantAR := []byte(`{"assessment-results":{"uuid":"x"}}`)
	stub := &stubBridgeServer{
		assessResp: &oscalv1.SerializeAssessmentResponse{
			AssessmentPlanJson:    wantAP,
			AssessmentResultsJson: wantAR,
		},
	}
	b := startBufconnBridge(t, stub)

	ap, ar, err := b.SerializeAssessment(context.Background(), &oscalv1.AssessmentInput{})
	if err != nil {
		t.Fatalf("SerializeAssessment: %v", err)
	}
	if string(ap) != string(wantAP) {
		t.Errorf("AssessmentPlan bytes = %q, want %q", ap, wantAP)
	}
	if string(ar) != string(wantAR) {
		t.Errorf("AssessmentResults bytes = %q, want %q", ar, wantAR)
	}

	stub.assessErr = errors.New("bridge boom")
	if _, _, err := b.SerializeAssessment(context.Background(), &oscalv1.AssessmentInput{}); err == nil {
		t.Fatal("SerializeAssessment must propagate the server-side error")
	}
}

func TestGRPCBridge_SerializePOAM_HappyAndErrorPaths(t *testing.T) {
	want := []byte(`{"plan-of-action-and-milestones":{"uuid":"x"}}`)
	stub := &stubBridgeServer{
		poamResp: &oscalv1.SerializePOAMResponse{OscalJson: want},
	}
	b := startBufconnBridge(t, stub)

	got, err := b.SerializePOAM(context.Background(), &oscalv1.PoamInput{})
	if err != nil {
		t.Fatalf("SerializePOAM: %v", err)
	}
	if string(got) != string(want) {
		t.Errorf("SerializePOAM bytes = %q, want %q", got, want)
	}

	stub.poamErr = errors.New("bridge boom")
	if _, err := b.SerializePOAM(context.Background(), &oscalv1.PoamInput{}); err == nil {
		t.Fatal("SerializePOAM must propagate the server-side error")
	}
}

func TestGRPCBridge_RoundTripValidate_HappyAndErrorPaths(t *testing.T) {
	stub := &stubBridgeServer{
		rtResp: &oscalv1.RoundTripValidateResponse{
			Valid:  false,
			Errors: []string{"missing required field 'metadata.title'"},
		},
	}
	b := startBufconnBridge(t, stub)

	valid, errs, err := b.RoundTripValidate(context.Background(), "system-security-plan", []byte(`{}`))
	if err != nil {
		t.Fatalf("RoundTripValidate: %v", err)
	}
	if valid {
		t.Error("RoundTripValidate must propagate valid=false")
	}
	if len(errs) != 1 || errs[0] == "" {
		t.Errorf("RoundTripValidate errors = %v, want one populated entry", errs)
	}

	// Happy path: valid=true, no errors.
	stub.rtResp = &oscalv1.RoundTripValidateResponse{Valid: true}
	valid, errs, err = b.RoundTripValidate(context.Background(), "system-security-plan", []byte(`{}`))
	if err != nil {
		t.Fatalf("RoundTripValidate happy: %v", err)
	}
	if !valid {
		t.Error("RoundTripValidate must propagate valid=true")
	}
	if len(errs) != 0 {
		t.Errorf("RoundTripValidate happy errors = %v, want none", errs)
	}

	stub.rtErr = errors.New("bridge died mid-validate")
	if _, _, err := b.RoundTripValidate(context.Background(), "system-security-plan", []byte(`{}`)); err == nil {
		t.Fatal("RoundTripValidate must propagate the server-side error")
	}
}

func TestGRPCBridge_Close_NilConnIsNoop(t *testing.T) {
	// A grpcBridge constructed without a conn (the test-construction path
	// or a partial init under error) must Close cleanly — Close gates on
	// `if b.conn == nil`.
	b := &grpcBridge{}
	if err := b.Close(); err != nil {
		t.Errorf("Close on nil-conn grpcBridge = %v, want nil", err)
	}
}
