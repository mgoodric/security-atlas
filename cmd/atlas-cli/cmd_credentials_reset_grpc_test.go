package main

import (
	"context"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"

	adminv1 "github.com/mgoodric/security-atlas/gen/proto/admin/v1"
)

// fakeResetServer records the ResetBootstrap call it receives.
type fakeResetServer struct {
	adminv1.UnimplementedAdminCredentialsServiceServer
	gotAuth string
	gotReq  *adminv1.ResetBootstrapRequest
	err     error
}

func (f *fakeResetServer) ResetBootstrap(ctx context.Context, req *adminv1.ResetBootstrapRequest) (*adminv1.ResetBootstrapResponse, error) {
	md, _ := metadata.FromIncomingContext(ctx)
	if v := md.Get("authorization"); len(v) > 0 {
		f.gotAuth = v[0]
	}
	f.gotReq = req
	if f.err != nil {
		return nil, f.err
	}
	return &adminv1.ResetBootstrapResponse{CredentialId: "key_1", TenantId: req.GetTenantId(), BearerToken: "minted-admin"}, nil
}

// startFakeResetServer serves fake on a loopback gRPC listener and points
// the CLI's endpoint, insecure and token flags at it.
func startFakeResetServer(t *testing.T, fake *fakeResetServer, token string) {
	t.Helper()
	lis, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	gs := grpc.NewServer()
	adminv1.RegisterAdminCredentialsServiceServer(gs, fake)
	go func() { _ = gs.Serve(lis) }()
	t.Cleanup(gs.Stop)

	prev := common
	common.endpoint, common.token, common.insecure = lis.Addr().String(), token, true
	t.Cleanup(func() { common = prev })
	prevStdout := cmdCredentialsStdout
	cmdCredentialsStdout = func() io.Writer { return io.Discard }
	t.Cleanup(func() { cmdCredentialsStdout = prevStdout })
}

// #2121: /v1 accepts only JWTs, so an API-key --token (ATLAS_BOOTSTRAP_TOKEN)
// resets over gRPC, carrying the key as the bearer and --force, --tenant and
// --ttl in the request. The HTTP endpoint is never called.
func TestRunResetBootstrap_APIKeyGoesOverGRPC(t *testing.T) {
	httpCalled := false
	httpSrv := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		httpCalled = true
	}))
	defer httpSrv.Close()
	fake := &fakeResetServer{}
	startFakeResetServer(t, fake, "bootstrap-api-key")

	if err := runResetBootstrap("t-1", true, httpSrv.URL, 48*time.Hour); err != nil {
		t.Fatalf("runResetBootstrap: %v", err)
	}
	if httpCalled {
		t.Fatalf("an API-key reset reached the HTTP endpoint")
	}
	if fake.gotAuth != "Bearer bootstrap-api-key" {
		t.Fatalf("authorization = %q, want the API key as bearer", fake.gotAuth)
	}
	req := fake.gotReq
	if req == nil || !req.GetForce() || req.GetTenantId() != "t-1" || req.GetTtl().AsDuration() != 48*time.Hour {
		t.Fatalf("request = %v, want force, tenant t-1, ttl 48h", req)
	}
}

// With no --ttl the field is absent and the server default applies; a
// sub-second --ttl is refused before anything is sent.
func TestRunResetBootstrapGRPC_TTL(t *testing.T) {
	fake := &fakeResetServer{}
	startFakeResetServer(t, fake, "bootstrap-api-key")

	if err := runResetBootstrap("", true, "", 0); err != nil {
		t.Fatalf("runResetBootstrap: %v", err)
	}
	if fake.gotReq.Ttl != nil {
		t.Fatalf("ttl sent for --ttl 0: %v", fake.gotReq.Ttl)
	}
	fake.gotReq = nil
	if err := runResetBootstrap("", true, "", time.Millisecond); err == nil {
		t.Fatalf("sub-second --ttl: want an error")
	}
	if fake.gotReq != nil {
		t.Fatalf("a refused --ttl reached the server")
	}
}

// The foot-gun gate's FailedPrecondition surfaces as the --force hint.
func TestRunResetBootstrapGRPC_RefusalSuggestsForce(t *testing.T) {
	fake := &fakeResetServer{err: status.Error(codes.FailedPrecondition, "refusing to reset")}
	startFakeResetServer(t, fake, "bootstrap-api-key")

	err := runResetBootstrap("", false, "", 0)
	if err == nil || !strings.Contains(err.Error(), "--force") {
		t.Fatalf("err = %v, want the --force hint", err)
	}
}
