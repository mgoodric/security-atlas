package admin

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	adminv1 "github.com/mgoodric/security-atlas/gen/proto/admin/v1"
	"github.com/mgoodric/security-atlas/internal/api/authctx"
	"github.com/mgoodric/security-atlas/internal/api/credstore"
)

// #1762: Issue and List without tenant_id act in the caller's own
// credential tenant; an explicit tenant_id still wins, and a caller with
// no tenant must name one.
func TestIssueAndList_TenantDefaultsToCaller(t *testing.T) {
	t.Parallel()
	callerTenant := uuid.NewString()
	otherTenant := uuid.NewString()

	cases := []struct {
		name      string
		caller    *credstore.Credential
		requested string
		want      string
	}{
		{name: "omitted, caller's tenant", caller: &credstore.Credential{TenantID: callerTenant}, want: callerTenant},
		{name: "explicit wins", caller: &credstore.Credential{TenantID: callerTenant}, requested: otherTenant, want: otherTenant},
		{name: "omitted, caller in no tenant", caller: &credstore.Credential{TenantID: uuid.Nil.String()}},
		{name: "omitted, caller tenant empty", caller: &credstore.Credential{}},
		{name: "omitted, no caller"},
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
			if tc.want == "" {
				for _, err := range []error{issueErr, listErr} {
					if status.Code(err) != codes.InvalidArgument {
						t.Fatalf("err = %v, want InvalidArgument (tenant_id is required)", err)
					}
				}
				return
			}
			if issueErr != nil || listErr != nil {
				t.Fatalf("issue err = %v, list err = %v", issueErr, listErr)
			}
			if got := issued.GetHandle().GetTenantId(); got != tc.want {
				t.Errorf("issued tenant = %s, want %s", got, tc.want)
			}
			if len(listed.GetHandles()) != 1 || listed.GetHandles()[0].GetTenantId() != tc.want {
				t.Errorf("listed = %v, want the one key in tenant %s", listed.GetHandles(), tc.want)
			}
		})
	}
}
