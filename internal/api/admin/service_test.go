package admin

import (
	"context"
	"testing"
	"time"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/durationpb"

	adminv1 "github.com/mgoodric/security-atlas/gen/proto/admin/v1"
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
			resp, err := svc.Issue(context.Background(), &adminv1.IssueRequest{
				TenantId: "00000000-0000-0000-0000-000000000001",
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
