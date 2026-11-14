package api

import (
	"context"
	"os"
	"testing"
	"time"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/durationpb"

	adminv1 "github.com/mgoodric/security-atlas/gen/proto/admin/v1"
	"github.com/mgoodric/security-atlas/internal/platform"
)

// Tests for #2121: AdminCredentialsService.ResetBootstrap runs the
// reset-bootstrap endpoint's reset for an API-key caller, which /v1 (JWT
// only) cannot authenticate.

func (f resetFixture) rpc(as func(context.Context) context.Context, req *adminv1.ResetBootstrapRequest) (*adminv1.ResetBootstrapResponse, error) {
	ctx := context.Background()
	if as != nil {
		ctx = as(ctx)
	}
	return grpcBootstrapResetter{s: f.srv}.ResetBootstrap(ctx, req)
}

// An admin key on a single-tenant install resets, gets a recovery ADMIN key
// for its tenant, and the bearer lands in the bootstrap-token file.
func TestResetBootstrapRPC_AdminKeyOnSingleTenant(t *testing.T) {
	f := newResetFixture(t, 1)
	resp, err := f.rpc(f.tenantAdminKey, &adminv1.ResetBootstrapRequest{Force: true, Ttl: durationpb.New(2 * time.Hour)})
	if err != nil {
		t.Fatalf("ResetBootstrap: %v", err)
	}
	if f.resetter.resetCalls != 1 {
		t.Fatalf("ResetBootstrap called %d times; want 1", f.resetter.resetCalls)
	}
	if resp.GetTenantId() != f.tenantID || !resp.GetFileWritten() || resp.GetBearerToken() == "" {
		t.Fatalf("response = %v; want the caller's tenant, file written, a bearer", resp)
	}
	file, err := os.ReadFile(f.tokenPath)
	if err != nil || string(file) == "" {
		t.Fatalf("bootstrap-token file: %q, %v", file, err)
	}
	cred, err := f.srv.credStore.Authenticate(resp.GetBearerToken())
	if err != nil {
		t.Fatalf("Authenticate recovery key: %v", err)
	}
	if !cred.IsAdmin || cred.ID != resp.GetCredentialId() {
		t.Fatalf("recovery key = %+v; want the admin key %s", cred, resp.GetCredentialId())
	}
	if got := time.Until(resp.GetExpiresAt().AsTime()); got < time.Hour || got > 2*time.Hour {
		t.Fatalf("expires in %s; want about 2h", got)
	}
}

// The single-tenant gate holds on gRPC for every caller: there are no JWTs
// here, so a super_admin claim in context is not consulted. Refusals leave
// nothing reset, written or minted.
func TestResetBootstrapRPC_Refusals(t *testing.T) {
	cases := map[string]struct {
		tenants int
		as      func(resetFixture) func(context.Context) context.Context
		req     *adminv1.ResetBootstrapRequest
		want    codes.Code
	}{
		"no credential": {1, func(resetFixture) func(context.Context) context.Context { return nil },
			&adminv1.ResetBootstrapRequest{Force: true}, codes.Unauthenticated},
		"non-admin key": {1, func(f resetFixture) func(context.Context) context.Context { return f.viewer },
			&adminv1.ResetBootstrapRequest{Force: true}, codes.PermissionDenied},
		"admin key on multi-tenant": {2, func(f resetFixture) func(context.Context) context.Context { return f.tenantAdminKey },
			&adminv1.ResetBootstrapRequest{Force: true}, codes.PermissionDenied},
		"super_admin claim on multi-tenant": {2, func(f resetFixture) func(context.Context) context.Context { return f.superAdminJWT },
			&adminv1.ResetBootstrapRequest{Force: true}, codes.PermissionDenied},
		"other tenant": {1, func(f resetFixture) func(context.Context) context.Context { return f.tenantAdminKey },
			&adminv1.ResetBootstrapRequest{Force: true, TenantId: "00000000-0000-4000-8000-000000000099"}, codes.InvalidArgument},
		"sub-second ttl": {1, func(f resetFixture) func(context.Context) context.Context { return f.tenantAdminKey },
			&adminv1.ResetBootstrapRequest{Force: true, Ttl: durationpb.New(time.Millisecond)}, codes.InvalidArgument},
		"ttl over 7d": {1, func(f resetFixture) func(context.Context) context.Context { return f.tenantAdminKey },
			&adminv1.ResetBootstrapRequest{Force: true, Ttl: durationpb.New(8 * 24 * time.Hour)}, codes.InvalidArgument},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			f := newResetFixture(t, tc.tenants)
			_, err := f.rpc(tc.as(f), tc.req)
			if status.Code(err) != tc.want {
				t.Fatalf("err = %v; want %s", err, tc.want)
			}
			f.assertNothingWritten(t, 0)
		})
	}
}

// The foot-gun gate (first sign-in recorded, no --force) is FailedPrecondition,
// and mints nothing.
func TestResetBootstrapRPC_ForceGate(t *testing.T) {
	f := newResetFixture(t, 1)
	f.resetter.resetErr = platform.ErrResetForbidden
	_, err := f.rpc(f.tenantAdminKey, &adminv1.ResetBootstrapRequest{})
	if status.Code(err) != codes.FailedPrecondition {
		t.Fatalf("err = %v; want FailedPrecondition", err)
	}
	f.assertNothingWritten(t, 1)
}

// Without a platform resetter the RPC is Unavailable, as the HTTP route is
// absent.
func TestResetBootstrapRPC_Unconfigured(t *testing.T) {
	srv := New(Config{})
	_, err := grpcBootstrapResetter{s: srv}.ResetBootstrap(context.Background(), &adminv1.ResetBootstrapRequest{})
	if status.Code(err) != codes.Unavailable {
		t.Fatalf("err = %v; want Unavailable", err)
	}
}
