// Package api wires the gRPC server: services, auth interceptor, panic
// recovery. cmd/atlas calls Run to start it.
package api

import (
	"context"
	"errors"
	"fmt"
	"net"
	"strings"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"

	adminv1 "github.com/mgoodric/security-atlas/gen/proto/admin/v1"
	connectorsv1 "github.com/mgoodric/security-atlas/gen/proto/connectors/v1"
	evidencev1 "github.com/mgoodric/security-atlas/gen/proto/evidence/v1"
	"github.com/mgoodric/security-atlas/internal/api/admin"
	"github.com/mgoodric/security-atlas/internal/api/authctx"
	"github.com/mgoodric/security-atlas/internal/api/connectorregistry"
	"github.com/mgoodric/security-atlas/internal/api/connectors"
	"github.com/mgoodric/security-atlas/internal/api/credstore"
	"github.com/mgoodric/security-atlas/internal/api/evidence"
	"github.com/mgoodric/security-atlas/internal/api/idemstore"
	"github.com/mgoodric/security-atlas/internal/api/schemaregistry"
	sdk "github.com/mgoodric/security-atlas/pkg/sdk-go"
)

// Server bundles the gRPC server and its in-memory stores. The stores are
// unexported; callers reach them through purposeful methods (e.g.
// IssueBootstrapCredential) so the surface stays small as more services land.
type Server struct {
	GRPC              *grpc.Server
	credStore         *credstore.Store
	registry          schemaregistry.Registry
	idemStore         idemstore.Store
	connectorRegistry connectorregistry.Store
}

// IssueBootstrapCredential mints a credential for the supplied tenant and
// returns the bearer token. cmd/atlas uses it once at startup; tests use it
// to mint per-test bearers. Production deployments call AdminCredentials.Issue
// from a separate admin client instead.
func (s *Server) IssueBootstrapCredential(tenantID string) (credstore.Credential, string, error) {
	return s.credStore.Issue(tenantID, "", nil, 0)
}

// Config groups the wiring inputs. Zero values yield a sane local setup.
type Config struct {
	RotationGrace time.Duration
	RegistrySeed  []schemaregistry.KindVersion
}

// New constructs the Server with its services and interceptors mounted.
func New(cfg Config) *Server {
	if cfg.RotationGrace == 0 {
		cfg.RotationGrace = time.Hour
	}
	if cfg.RegistrySeed == nil {
		cfg.RegistrySeed = schemaregistry.DefaultSeed()
	}

	cred := credstore.New(cfg.RotationGrace)
	reg := schemaregistry.New(cfg.RegistrySeed)
	idem := idemstore.New()
	connReg := connectorregistry.New(nil)

	grpcServer := grpc.NewServer(
		grpc.ChainUnaryInterceptor(
			recoverInterceptor(),
			authInterceptor(cred),
		),
	)
	evidencev1.RegisterEvidenceIngestServiceServer(grpcServer, evidence.New(reg, idem, nil))
	adminv1.RegisterAdminCredentialsServiceServer(grpcServer, admin.New(cred))
	connectorsv1.RegisterConnectorRegistryServiceServer(grpcServer, connectors.New(connReg))

	return &Server{
		GRPC:              grpcServer,
		credStore:         cred,
		registry:          reg,
		idemStore:         idem,
		connectorRegistry: connReg,
	}
}

// Run starts the gRPC server on addr (e.g., ":50051") and blocks until ctx
// is canceled, at which point it gracefully stops.
func (s *Server) Run(ctx context.Context, addr string) error {
	lis, err := net.Listen("tcp", addr)
	if err != nil {
		return fmt.Errorf("api: listen %s: %w", addr, err)
	}
	errCh := make(chan error, 1)
	go func() { errCh <- s.GRPC.Serve(lis) }()

	select {
	case <-ctx.Done():
		s.GRPC.GracefulStop()
		return nil
	case err := <-errCh:
		return err
	}
}

// authInterceptor extracts the bearer token from `authorization` metadata,
// resolves it against the credential store, and attaches the credential to
// context. Missing/empty/malformed/revoked tokens return Unauthenticated.
// AdminCredentials.Issue is allowed under a bootstrap path (mock).
func authInterceptor(store *credstore.Store) grpc.UnaryServerInterceptor {
	return func(ctx context.Context, req any, info *grpc.UnaryServerInfo, handler grpc.UnaryHandler) (any, error) {
		token, err := extractBearer(ctx)
		if err != nil {
			return nil, err
		}
		cred, err := store.Authenticate(token)
		if err != nil {
			if errors.Is(err, credstore.ErrUnknownKey) {
				return nil, status.Error(codes.Unauthenticated, "invalid or revoked bearer token")
			}
			return nil, status.Errorf(codes.Internal, "authenticate: %v", err)
		}
		ctx = authctx.WithCredential(ctx, cred)
		return handler(ctx, req)
	}
}

func extractBearer(ctx context.Context) (string, error) {
	md, ok := metadata.FromIncomingContext(ctx)
	if !ok {
		return "", status.Error(codes.Unauthenticated, "missing metadata")
	}
	auth := md.Get(sdk.MetadataAuthorization)
	if len(auth) == 0 {
		return "", status.Error(codes.Unauthenticated, "missing authorization metadata")
	}
	parts := strings.SplitN(strings.TrimSpace(auth[0]), " ", 2)
	if len(parts) != 2 || !strings.EqualFold(parts[0], "bearer") {
		return "", status.Error(codes.Unauthenticated, "authorization must be `Bearer <token>`")
	}
	tok := strings.TrimSpace(parts[1])
	if tok == "" {
		return "", status.Error(codes.Unauthenticated, "authorization must be `Bearer <token>`")
	}
	return tok, nil
}

// recoverInterceptor turns handler panics into codes.Internal so the
// server doesn't crash on bad metadata or nil deref.
func recoverInterceptor() grpc.UnaryServerInterceptor {
	return func(ctx context.Context, req any, info *grpc.UnaryServerInfo, handler grpc.UnaryHandler) (resp any, err error) {
		defer func() {
			if r := recover(); r != nil {
				err = status.Errorf(codes.Internal, "panic: %v", r)
			}
		}()
		return handler(ctx, req)
	}
}
