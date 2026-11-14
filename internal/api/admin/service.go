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
	caller, err := RequireAdmin(ctx)
	if err != nil {
		return nil, err
	}
	tenant, err := requestTenant(caller, req.GetTenantId())
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
	cred, bearer, err := s.store.IssueMinted(tenant, req.GetScopePredicate(), req.GetKinds(), ttl, credstore.MinterUserID(caller))
	if err != nil {
		return nil, status.Errorf(codes.Internal, "issue: %v", err)
	}
	return &adminv1.IssueResponse{
		Handle:      handleFromCred(cred),
		BearerToken: bearer,
	}, nil
}

// Rotate issues a successor token and starts the predecessor's grace window.
func (s *Service) Rotate(ctx context.Context, req *adminv1.RotateRequest) (*adminv1.RotateResponse, error) {
	if err := s.requireOwnKey(ctx, req.GetId()); err != nil {
		return nil, err
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
func (s *Service) Revoke(ctx context.Context, req *adminv1.RevokeRequest) (*adminv1.RevokeResponse, error) {
	if err := s.requireOwnKey(ctx, req.GetId()); err != nil {
		return nil, err
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
	caller, err := RequireAdmin(ctx)
	if err != nil {
		return nil, err
	}
	tenant, err := requestTenant(caller, req.GetTenantId())
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

// RequireAdmin is the gate every AdminCredentialsService RPC passes
// first, the gRPC twin of the HTTP admincreds requireAdmin (#2167): the
// caller must hold an admin credential. A missing credential is
// Unauthenticated, a non-admin one PermissionDenied. It returns the
// caller so the RPC can scope its work to the caller's tenant.
func RequireAdmin(ctx context.Context) (credstore.Credential, error) {
	caller, ok := authctx.CredentialFromContext(ctx)
	if !ok {
		return credstore.Credential{}, status.Error(codes.Unauthenticated, "missing credential")
	}
	if !caller.IsAdmin {
		return credstore.Credential{}, status.Error(codes.PermissionDenied, "admin credential required")
	}
	return caller, nil
}

// requireOwnKey gates Rotate and Revoke: an admin caller, acting on a key
// in the caller's own tenant. A key in another tenant is NotFound, as on
// the HTTP path, so a caller learns nothing about other tenants' ids.
func (s *Service) requireOwnKey(ctx context.Context, id string) error {
	caller, err := RequireAdmin(ctx)
	if err != nil {
		return err
	}
	if id == "" {
		return status.Error(codes.InvalidArgument, "id is required")
	}
	if tenant, ok := s.store.TenantOf(id); !ok || tenant != caller.TenantID {
		return status.Errorf(codes.NotFound, "unknown key id %q", id)
	}
	return nil
}

// requestTenant is the tenant an Issue or List acts in: the caller's own
// credential tenant (#1762). Admin credentials are tenant-scoped, so a
// tenant_id naming any other tenant is refused (#2167); naming the
// caller's own is accepted for older clients. A caller with no tenant
// (empty, or the all-zeros id) cannot act in any.
func requestTenant(caller credstore.Credential, requested string) (string, error) {
	own := caller.TenantID
	if own == "" || own == uuid.Nil.String() {
		if requested == "" {
			return "", status.Error(codes.InvalidArgument, "tenant_id is required")
		}
		return "", status.Error(codes.PermissionDenied, "credential is not scoped to a tenant")
	}
	if requested != "" && requested != own {
		return "", status.Error(codes.PermissionDenied, "tenant_id must be the calling credential's tenant")
	}
	return own, nil
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
