// Package admin implements the AdminCredentialsService gRPC service. It
// translates RPCs into credstore operations and shapes the proto responses.
package admin

import (
	"context"
	"errors"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/durationpb"
	"google.golang.org/protobuf/types/known/timestamppb"

	adminv1 "github.com/mgoodric/security-atlas/gen/proto/admin/v1"
	"github.com/mgoodric/security-atlas/internal/api/credstore"
)

// Service implements adminv1.AdminCredentialsServiceServer.
type Service struct {
	adminv1.UnimplementedAdminCredentialsServiceServer
	store *credstore.Store
}

// New constructs the service.
func New(store *credstore.Store) *Service {
	return &Service{store: store}
}

// Issue creates a new API key and returns the bearer token exactly once.
func (s *Service) Issue(_ context.Context, req *adminv1.IssueRequest) (*adminv1.IssueResponse, error) {
	if req.GetTenantId() == "" {
		return nil, status.Error(codes.InvalidArgument, "tenant_id is required")
	}
	cred, bearer, err := s.store.Issue(req.GetTenantId(), req.GetScopePredicate(), req.GetKinds(), req.GetTtl().AsDuration())
	if err != nil {
		return nil, status.Errorf(codes.Internal, "issue: %v", err)
	}
	return &adminv1.IssueResponse{
		Handle:      handleFromCred(cred),
		BearerToken: bearer,
	}, nil
}

// Rotate issues a successor token and starts the predecessor's grace window.
func (s *Service) Rotate(_ context.Context, req *adminv1.RotateRequest) (*adminv1.RotateResponse, error) {
	if req.GetId() == "" {
		return nil, status.Error(codes.InvalidArgument, "id is required")
	}
	successor, bearer, predecessorExpiresAt, err := s.store.Rotate(req.GetId())
	if err != nil {
		if errors.Is(err, credstore.ErrUnknownKey) {
			return nil, status.Errorf(codes.NotFound, "unknown key id %q", req.GetId())
		}
		return nil, status.Errorf(codes.Internal, "rotate: %v", err)
	}
	return &adminv1.RotateResponse{
		Handle:               handleFromCred(successor),
		BearerToken:          bearer,
		PredecessorExpiresAt: timestamppb.New(predecessorExpiresAt),
	}, nil
}

// Revoke invalidates the key immediately.
func (s *Service) Revoke(_ context.Context, req *adminv1.RevokeRequest) (*adminv1.RevokeResponse, error) {
	if req.GetId() == "" {
		return nil, status.Error(codes.InvalidArgument, "id is required")
	}
	if err := s.store.Revoke(req.GetId()); err != nil {
		if errors.Is(err, credstore.ErrUnknownKey) {
			return nil, status.Errorf(codes.NotFound, "unknown key id %q", req.GetId())
		}
		return nil, status.Errorf(codes.Internal, "revoke: %v", err)
	}
	return &adminv1.RevokeResponse{}, nil
}

// List enumerates active credentials for a tenant. No tokens returned.
func (s *Service) List(_ context.Context, req *adminv1.ListRequest) (*adminv1.ListResponse, error) {
	if req.GetTenantId() == "" {
		return nil, status.Error(codes.InvalidArgument, "tenant_id is required")
	}
	creds := s.store.List(req.GetTenantId())
	handles := make([]*adminv1.ApiKeyHandle, 0, len(creds))
	for _, c := range creds {
		handles = append(handles, handleFromCred(c))
	}
	return &adminv1.ListResponse{Handles: handles}, nil
}

func handleFromCred(c credstore.Credential) *adminv1.ApiKeyHandle {
	h := &adminv1.ApiKeyHandle{
		Id:             c.ID,
		TenantId:       c.TenantID,
		ScopePredicate: c.ScopePredicate,
		Kinds:          c.Kinds,
		Ttl:            durationpb.New(c.TTL),
		IssuedAt:       timestamppb.New(c.IssuedAt),
		RotatedFrom:    c.RotatedFrom,
		Last_4:         c.Last4,
	}
	if !c.LastUsedAt.IsZero() {
		h.LastUsedAt = timestamppb.New(c.LastUsedAt)
	}
	return h
}
