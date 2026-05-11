package connectors_test

import (
	"context"
	"net"
	"testing"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"
	"google.golang.org/grpc/test/bufconn"

	connectorsv1 "github.com/mgoodric/security-atlas/gen/proto/connectors/v1"
	"github.com/mgoodric/security-atlas/internal/api"
)

const tenantA = "11111111-1111-1111-1111-111111111111"

type harness struct {
	srv    *api.Server
	bearer string
	dialer func(context.Context, string) (net.Conn, error)
}

func newHarness(t *testing.T) *harness {
	t.Helper()
	srv := api.New(api.Config{RotationGrace: time.Hour})
	lis := bufconn.Listen(1 << 20)
	go func() { _ = srv.GRPC.Serve(lis) }()
	t.Cleanup(func() {
		srv.GRPC.GracefulStop()
		_ = lis.Close()
	})
	_, bearer, err := srv.IssueBootstrapCredential(tenantA)
	if err != nil {
		t.Fatalf("IssueBootstrapCredential: %v", err)
	}
	return &harness{
		srv:    srv,
		bearer: bearer,
		dialer: func(ctx context.Context, _ string) (net.Conn, error) { return lis.DialContext(ctx) },
	}
}

func (h *harness) client(t *testing.T) (connectorsv1.ConnectorRegistryServiceClient, func()) {
	t.Helper()
	conn, err := grpc.NewClient("passthrough://bufconn",
		grpc.WithTransportCredentials(insecure.NewCredentials()),
		grpc.WithContextDialer(h.dialer),
	)
	if err != nil {
		t.Fatalf("grpc.NewClient: %v", err)
	}
	return connectorsv1.NewConnectorRegistryServiceClient(conn), func() { _ = conn.Close() }
}

func authCtx(bearer string) context.Context {
	return metadata.AppendToOutgoingContext(context.Background(), "authorization", "Bearer "+bearer)
}

func TestRegister_HandleReflectsRequest(t *testing.T) {
	h := newHarness(t)
	c, done := h.client(t)
	defer done()

	resp, err := c.Register(authCtx(h.bearer), &connectorsv1.RegisterRequest{
		Name:              "aws-connector",
		Version:           "v0.4.0",
		InstanceId:        "inst-1",
		SupportedKinds:    []string{"aws.s3.bucket_encryption_state.v1"},
		ProfilesSupported: []string{"push"},
	})
	if err != nil {
		t.Fatalf("Register: %v", err)
	}
	h2 := resp.GetHandle()
	if h2.GetTenantId() != tenantA {
		t.Fatalf("tenant_id = %q; want %q", h2.GetTenantId(), tenantA)
	}
	if h2.GetName() != "aws-connector" {
		t.Fatalf("name = %q", h2.GetName())
	}
	if h2.GetRegisteredAt() == nil {
		t.Fatal("registered_at must be populated by the server")
	}
}

func TestRegister_DuplicateNaturalKeyAlreadyExists(t *testing.T) {
	h := newHarness(t)
	c, done := h.client(t)
	defer done()

	req := &connectorsv1.RegisterRequest{
		Name: "aws-connector", Version: "v0.4.0", InstanceId: "inst-1",
		SupportedKinds: []string{"aws.s3.bucket_encryption_state.v1"}, ProfilesSupported: []string{"push"},
	}
	if _, err := c.Register(authCtx(h.bearer), req); err != nil {
		t.Fatalf("first Register: %v", err)
	}
	_, err := c.Register(authCtx(h.bearer), req)
	if status.Code(err) != codes.AlreadyExists {
		t.Fatalf("second Register code = %v; want AlreadyExists", status.Code(err))
	}
}

func TestList_FilteredByAuthTenant(t *testing.T) {
	h := newHarness(t)
	c, done := h.client(t)
	defer done()

	if _, err := c.Register(authCtx(h.bearer), &connectorsv1.RegisterRequest{
		Name: "aws-connector", Version: "v0.4.0", InstanceId: "inst-1",
		SupportedKinds: []string{"aws.s3.bucket_encryption_state.v1"}, ProfilesSupported: []string{"push"},
	}); err != nil {
		t.Fatalf("Register: %v", err)
	}

	resp, err := c.List(authCtx(h.bearer), &connectorsv1.ListRequest{})
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(resp.GetHandles()) != 1 {
		t.Fatalf("got %d handles; want 1", len(resp.GetHandles()))
	}
	if resp.GetHandles()[0].GetTenantId() != tenantA {
		t.Fatalf("handle tenant = %q", resp.GetHandles()[0].GetTenantId())
	}
}

func TestList_OtherTenantSeesNothing(t *testing.T) {
	h := newHarness(t)
	c, done := h.client(t)
	defer done()

	if _, err := c.Register(authCtx(h.bearer), &connectorsv1.RegisterRequest{
		Name: "aws-connector", Version: "v0.4.0", InstanceId: "inst-1",
		SupportedKinds: []string{"aws.s3.bucket_encryption_state.v1"}, ProfilesSupported: []string{"push"},
	}); err != nil {
		t.Fatalf("Register: %v", err)
	}

	_, otherBearer, err := h.srv.IssueBootstrapCredential("22222222-2222-2222-2222-222222222222")
	if err != nil {
		t.Fatalf("IssueBootstrapCredential(other): %v", err)
	}
	resp, err := c.List(authCtx(otherBearer), &connectorsv1.ListRequest{})
	if err != nil {
		t.Fatalf("List(other): %v", err)
	}
	if len(resp.GetHandles()) != 0 {
		t.Fatalf("cross-tenant List leaked %d handles", len(resp.GetHandles()))
	}
}

func TestRegister_RejectsMissingRequiredFields(t *testing.T) {
	h := newHarness(t)
	c, done := h.client(t)
	defer done()

	cases := []*connectorsv1.RegisterRequest{
		{Version: "v0.4.0", InstanceId: "i", SupportedKinds: []string{"k"}, ProfilesSupported: []string{"push"}}, // name
		{Name: "n", InstanceId: "i", SupportedKinds: []string{"k"}, ProfilesSupported: []string{"push"}},         // version
		{Name: "n", Version: "v", SupportedKinds: []string{"k"}, ProfilesSupported: []string{"push"}},            // instance
		{Name: "n", Version: "v", InstanceId: "i", ProfilesSupported: []string{"push"}},                          // kinds
		{Name: "n", Version: "v", InstanceId: "i", SupportedKinds: []string{"k"}},                                // profiles
	}
	for i, req := range cases {
		if _, err := c.Register(authCtx(h.bearer), req); status.Code(err) != codes.InvalidArgument {
			t.Errorf("case %d: code = %v; want InvalidArgument", i, status.Code(err))
		}
	}
}
