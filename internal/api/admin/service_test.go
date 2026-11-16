package admin

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/durationpb"

	adminv1 "github.com/mgoodric/security-atlas/gen/proto/admin/v1"
	"github.com/mgoodric/security-atlas/internal/api/authctx"
	"github.com/mgoodric/security-atlas/internal/api/credstore"
)

// #1906: Issue refuses a negative ttl before minting, as the HTTP path
// does, and still accepts an absent or zero ttl as "no expiry".
func TestIssue_TTL(t *testing.T) {
	t.Parallel()
	for name, tc := range map[string]struct {
		ttl  *durationpb.Duration
		want codes.Code
	}{
		"negative": {durationpb.New(-time.Second), codes.InvalidArgument},
		"absent":   {nil, codes.OK},
		"zero":     {durationpb.New(0), codes.OK},
		"positive": {durationpb.New(time.Hour), codes.OK},
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			store := credstore.New(time.Minute)
			svc := New(store)
			const tenant = "00000000-0000-0000-0000-000000000001"
			ctx := authctx.WithCredential(context.Background(), credstore.Credential{TenantID: tenant, IsAdmin: true})
			resp, err := svc.Issue(ctx, &adminv1.IssueRequest{
				TenantId: tenant,
				Ttl:      tc.ttl,
			})
			if got := status.Code(err); got != tc.want {
				t.Fatalf("code = %v, want %v (err=%v)", got, tc.want, err)
			}
			if tc.want != codes.OK {
				if resp != nil {
					t.Fatalf("refused Issue returned a response: %+v", resp)
				}
				return
			}
			if resp.GetBearerToken() == "" {
				t.Fatalf("Issue returned no bearer")
			}
		})
	}
}

// #1762, #2167: Issue and List act in the admin caller's own credential
// tenant. Omitting tenant_id or naming the caller's own tenant works; naming
// another tenant is refused; a caller in no tenant can act in none.
func TestIssueAndList_TenantIsCallers(t *testing.T) {
	t.Parallel()
	callerTenant := uuid.NewString()
	otherTenant := uuid.NewString()
	admin := func(tenant string) *credstore.Credential {
		return &credstore.Credential{TenantID: tenant, IsAdmin: true}
	}

	cases := []struct {
		name      string
		caller    *credstore.Credential
		requested string
		want      codes.Code
	}{
		{name: "omitted, caller's tenant", caller: admin(callerTenant), want: codes.OK},
		{name: "explicit, caller's tenant", caller: admin(callerTenant), requested: callerTenant, want: codes.OK},
		{name: "explicit, other tenant", caller: admin(callerTenant), requested: otherTenant, want: codes.PermissionDenied},
		{name: "omitted, caller in no tenant", caller: admin(uuid.Nil.String()), want: codes.InvalidArgument},
		{name: "explicit, caller in no tenant", caller: admin(uuid.Nil.String()), requested: otherTenant, want: codes.PermissionDenied},
		{name: "omitted, caller tenant empty", caller: admin(""), want: codes.InvalidArgument},
		{name: "non-admin, caller's tenant", caller: &credstore.Credential{TenantID: callerTenant}, want: codes.PermissionDenied},
		{name: "no caller", want: codes.Unauthenticated},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			svc := New(credstore.New(time.Hour))
			ctx := context.Background()
			if tc.caller != nil {
				ctx = authctx.WithCredential(ctx, *tc.caller)
			}

			issued, issueErr := svc.Issue(ctx, &adminv1.IssueRequest{TenantId: tc.requested})
			listed, listErr := svc.List(ctx, &adminv1.ListRequest{TenantId: tc.requested})
			for rpc, err := range map[string]error{"Issue": issueErr, "List": listErr} {
				if got := status.Code(err); got != tc.want {
					t.Fatalf("%s code = %v, want %v (err=%v)", rpc, got, tc.want, err)
				}
			}
			if tc.want != codes.OK {
				return
			}
			if got := issued.GetHandle().GetTenantId(); got != callerTenant {
				t.Errorf("issued tenant = %s, want %s", got, callerTenant)
			}
			if len(listed.GetHandles()) != 1 || listed.GetHandles()[0].GetTenantId() != callerTenant {
				t.Errorf("listed = %v, want the one key in tenant %s", listed.GetHandles(), callerTenant)
			}
		})
	}
}

// #2167: Rotate and Revoke need an admin caller, and act only on keys in
// the caller's tenant. Another tenant's key reads as unknown, as on HTTP.
func TestRotateAndRevoke_AdminInOwnTenant(t *testing.T) {
	t.Parallel()
	tenantA := uuid.NewString()
	tenantB := uuid.NewString()

	cases := []struct {
		name   string
		caller *credstore.Credential
		want   codes.Code
	}{
		{name: "admin, same tenant", caller: &credstore.Credential{TenantID: tenantA, IsAdmin: true}, want: codes.OK},
		{name: "admin, other tenant", caller: &credstore.Credential{TenantID: tenantB, IsAdmin: true}, want: codes.NotFound},
		{name: "non-admin, same tenant", caller: &credstore.Credential{TenantID: tenantA}, want: codes.PermissionDenied},
		{name: "approver, same tenant", caller: &credstore.Credential{TenantID: tenantA, IsApprover: true}, want: codes.PermissionDenied},
		{name: "no caller", want: codes.Unauthenticated},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			store := credstore.New(time.Hour)
			svc := New(store)
			ctx := context.Background()
			if tc.caller != nil {
				ctx = authctx.WithCredential(ctx, *tc.caller)
			}
			toRotate, _, err := store.Issue(tenantA, "", nil, 0)
			if err != nil {
				t.Fatalf("Issue: %v", err)
			}
			toRevoke, toRevokeBearer, err := store.Issue(tenantA, "", nil, 0)
			if err != nil {
				t.Fatalf("Issue: %v", err)
			}

			_, rotErr := svc.Rotate(ctx, &adminv1.RotateRequest{Id: toRotate.ID})
			_, revErr := svc.Revoke(ctx, &adminv1.RevokeRequest{Id: toRevoke.ID})
			for rpc, err := range map[string]error{"Rotate": rotErr, "Revoke": revErr} {
				if got := status.Code(err); got != tc.want {
					t.Fatalf("%s code = %v, want %v (err=%v)", rpc, got, tc.want, err)
				}
			}
			_, authErr := store.Authenticate(toRevokeBearer)
			if revoked := authErr != nil; revoked != (tc.want == codes.OK) {
				t.Errorf("key revoked = %v after Revoke returned %v", revoked, tc.want)
			}
		})
	}
}

// #2167: an admin's Rotate or Revoke of an id that does not exist is
// NotFound, and an empty id is still InvalidArgument.
func TestRotateAndRevoke_UnknownAndEmptyID(t *testing.T) {
	t.Parallel()
	svc := New(credstore.New(time.Hour))
	ctx := authctx.WithCredential(context.Background(), credstore.Credential{TenantID: uuid.NewString(), IsAdmin: true})
	for id, want := range map[string]codes.Code{"key_nope": codes.NotFound, "": codes.InvalidArgument} {
		_, rotErr := svc.Rotate(ctx, &adminv1.RotateRequest{Id: id})
		_, revErr := svc.Revoke(ctx, &adminv1.RevokeRequest{Id: id})
		for rpc, err := range map[string]error{"Rotate": rotErr, "Revoke": revErr} {
			if got := status.Code(err); got != want {
				t.Errorf("%s(%q) code = %v, want %v", rpc, id, got, want)
			}
		}
	}
}
