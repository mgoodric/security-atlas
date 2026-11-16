package admin

import (
	"context"
	"testing"
	"time"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	adminv1 "github.com/mgoodric/security-atlas/gen/proto/admin/v1"
	"github.com/mgoodric/security-atlas/internal/api/credstore"
)

type recordingResetter struct {
	got *adminv1.ResetBootstrapRequest
}

func (r *recordingResetter) ResetBootstrap(_ context.Context, req *adminv1.ResetBootstrapRequest) (*adminv1.ResetBootstrapResponse, error) {
	r.got = req
	return &adminv1.ResetBootstrapResponse{CredentialId: "key_1"}, nil
}

// #2121: ResetBootstrap is Unavailable until a resetter is wired, then
// hands the request to it unchanged.
func TestResetBootstrap_DelegatesToResetter(t *testing.T) {
	svc := New(credstore.New(time.Hour))
	if _, err := svc.ResetBootstrap(context.Background(), &adminv1.ResetBootstrapRequest{}); status.Code(err) != codes.Unavailable {
		t.Fatalf("unwired: err = %v; want Unavailable", err)
	}
	r := &recordingResetter{}
	svc.SetBootstrapResetter(r)
	req := &adminv1.ResetBootstrapRequest{Force: true, TenantId: "t-1"}
	resp, err := svc.ResetBootstrap(context.Background(), req)
	if err != nil || resp.GetCredentialId() != "key_1" {
		t.Fatalf("ResetBootstrap = %v, %v", resp, err)
	}
	if r.got != req {
		t.Fatalf("resetter got %v; want the request unchanged", r.got)
	}
}
