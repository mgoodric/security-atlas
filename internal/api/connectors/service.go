// Package connectors implements the ConnectorRegistryService gRPC service.
// The handler reads the caller's tenant from the auth context (slice 003)
// and never accepts a client-supplied tenant_id.
package connectors

import (
	"context"
	"errors"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/timestamppb"

	connectorsv1 "github.com/mgoodric/security-atlas/gen/proto/connectors/v1"
	"github.com/mgoodric/security-atlas/internal/api/authctx"
	"github.com/mgoodric/security-atlas/internal/api/connectorregistry"
)

type Service struct {
	connectorsv1.UnimplementedConnectorRegistryServiceServer
	store connectorregistry.Store
}

func New(store connectorregistry.Store) *Service {
	return &Service{store: store}
}

func (s *Service) Register(ctx context.Context, req *connectorsv1.RegisterRequest) (*connectorsv1.RegisterResponse, error) {
	cred, ok := authctx.CredentialFromContext(ctx)
	if !ok {
		return nil, status.Error(codes.Unauthenticated, "no credential in context")
	}
	switch {
	case req.GetName() == "":
		return nil, status.Error(codes.InvalidArgument, "name is required")
	case req.GetVersion() == "":
		return nil, status.Error(codes.InvalidArgument, "version is required")
	case req.GetInstanceId() == "":
		return nil, status.Error(codes.InvalidArgument, "instance_id is required")
	case len(req.GetSupportedKinds()) == 0:
		return nil, status.Error(codes.InvalidArgument, "supported_kinds is required")
	case len(req.GetProfilesSupported()) == 0:
		return nil, status.Error(codes.InvalidArgument, "profiles_supported is required")
	}

	handle, err := s.store.Register(cred.TenantID, req.GetName(), req.GetVersion(), req.GetInstanceId(), req.GetSupportedKinds(), req.GetProfilesSupported())
	if err != nil {
		if errors.Is(err, connectorregistry.ErrAlreadyRegistered) {
			return nil, status.Error(codes.AlreadyExists, "connector instance already registered")
		}
		return nil, status.Errorf(codes.Internal, "register: %v", err)
	}
	return &connectorsv1.RegisterResponse{Handle: handleToProto(handle)}, nil
}

func (s *Service) List(ctx context.Context, _ *connectorsv1.ListRequest) (*connectorsv1.ListResponse, error) {
	cred, ok := authctx.CredentialFromContext(ctx)
	if !ok {
		return nil, status.Error(codes.Unauthenticated, "no credential in context")
	}
	handles := s.store.List(cred.TenantID)
	out := make([]*connectorsv1.ConnectorHandle, 0, len(handles))
	for _, h := range handles {
		out = append(out, handleToProto(h))
	}
	return &connectorsv1.ListResponse{Handles: out}, nil
}

func handleToProto(h connectorregistry.Handle) *connectorsv1.ConnectorHandle {
	return &connectorsv1.ConnectorHandle{
		Id:                h.ID,
		TenantId:          h.TenantID,
		Name:              h.Name,
		Version:           h.Version,
		InstanceId:        h.InstanceID,
		SupportedKinds:    h.SupportedKinds,
		ProfilesSupported: h.ProfilesSupported,
		RegisteredAt:      timestamppb.New(h.RegisteredAt),
	}
}
