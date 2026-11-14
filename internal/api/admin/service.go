// Package admin implements the AdminCredentialsService gRPC service. It
// translates RPCs into credstore operations and shapes the proto responses.
package admin

import (
	"context"
	"errors"

	"github.com/google/uuid"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/durationpb"
	"google.golang.org/protobuf/types/known/timestamppb"

	adminv1 "github.com/mgoodric/security-atlas/gen/proto/admin/v1"
	"github.com/mgoodric/security-atlas/internal/api/authctx"
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
// A user caller is recorded as the key's minter, for attribution only
// (#1947): the key outlives that user's access.
func (s *Service) Issue(ctx context.Context, req *adminv1.IssueRequest) (*adminv1.IssueResponse, error) {
	tenant, err := requestTenant(ctx, req.GetTenantId())
	if err != nil {
		return nil, err
	}
	// A nil ttl is 0, "no expiry". A negative one is refused, matching the
	// HTTP path's ttl_seconds >= 0 (#1906). AsDuration saturates rather
	// than wrapping, so no upper bound is needed here (#1914 D3).
	ttl := req.GetTtl().AsDuration()
	if ttl < 0 {
		return nil, status.Error(codes.InvalidArgument, "ttl must not be negative")
	}
	var minter string
	if caller, ok := authctx.CredentialFromContext(ctx); ok {
		minter = credstore.MinterUserID(caller)
	}
	cred, bearer, err := s.store.IssueMinted(tenant, req.GetScopePredicate(), req.GetKinds(), ttl, minter)
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
func (s *Service) List(ctx context.Context, req *adminv1.ListRequest) (*adminv1.ListResponse, error) {
	tenant, err := requestTenant(ctx, req.GetTenantId())
	if err != nil {
		return nil, err
	}
	creds := s.store.List(tenant)
	handles := make([]*adminv1.ApiKeyHandle, 0, len(creds))
	for _, c := range creds {
		handles = append(handles, handleFromCred(c))
	}
	return &adminv1.ListResponse{Handles: handles}, nil
}

// requestTenant is the tenant an Issue or List acts in: tenant_id when the
// request names one, otherwise the caller's own credential tenant, so a CLI
// on a single-tenant install need not name it (#1762). A caller with no
// tenant (the all-zeros id a super_admin JWT carries outside any tenant)
// must name one.
func requestTenant(ctx context.Context, requested string) (string, error) {
	if requested != "" {
		return requested, nil
	}
	if caller, ok := authctx.CredentialFromContext(ctx); ok &&
		caller.TenantID != "" && caller.TenantID != uuid.Nil.String() {
		return caller.TenantID, nil
	}
	return "", status.Error(codes.InvalidArgument, "tenant_id is required")
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
