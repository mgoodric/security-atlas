// Package api wires the gRPC server: services, auth interceptor, panic
// recovery. cmd/atlas calls Run to start it.
package api

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"strings"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"

	"github.com/jackc/pgx/v5/pgxpool"

	adminv1 "github.com/mgoodric/security-atlas/gen/proto/admin/v1"
	connectorsv1 "github.com/mgoodric/security-atlas/gen/proto/connectors/v1"
	evidencev1 "github.com/mgoodric/security-atlas/gen/proto/evidence/v1"
	"github.com/mgoodric/security-atlas/internal/api/admin"
	authapi "github.com/mgoodric/security-atlas/internal/api/auth"
	"github.com/mgoodric/security-atlas/internal/api/authctx"
	"github.com/mgoodric/security-atlas/internal/api/connectorregistry"
	"github.com/mgoodric/security-atlas/internal/api/connectors"
	"github.com/mgoodric/security-atlas/internal/api/credstore"
	"github.com/mgoodric/security-atlas/internal/api/evidence"
	"github.com/mgoodric/security-atlas/internal/api/idemstore"
	"github.com/mgoodric/security-atlas/internal/api/schemaregistry"
	"github.com/mgoodric/security-atlas/internal/artifact"
	"github.com/mgoodric/security-atlas/internal/auth/apikeystore"
	"github.com/mgoodric/security-atlas/internal/authz"
	"github.com/mgoodric/security-atlas/internal/evidence/ingest"
	"github.com/mgoodric/security-atlas/internal/oscal"
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
	dbPool            *pgxpool.Pool
	ingestService     *ingest.Service
	evidencePushRate  float64
	artifactStore     *artifact.Store
	// evidencePublisher is the slice-015 substrate. When non-nil the
	// push HTTP handler routes through JetStream; nil falls back to
	// direct Service.Process.
	evidencePublisher evidence.Publisher
	// Slice 034: DB-backed bearer-credential store. When wired, the HTTP
	// auth middleware falls through to it for tokens not known by the
	// in-memory credstore. Admin-credential HTTP routes (POST/GET/rotate/
	// revoke under /v1/admin/credentials) only mount when this is set.
	apikeyStore *apikeystore.Store
	// Slice 034: user-facing auth routes (OIDC login/callback, local
	// login, logout). Only mounted when set.
	authHandler *authapi.Handler
	// Slice 035: OPA-backed authz engine + decision audit writer. The
	// HTTP authz middleware is attached in httpHandler when both are
	// non-nil. Unit servers can leave them unset to bypass authz; the
	// production binary wires them once at startup via AttachAuthz.
	authzEngine *authz.Engine
	authzAudit  *authz.AuditWriter
	// Slice 030: OSCAL export pipeline. When wired, the
	// POST /v1/audit-periods/{id}/oscal-export route is mounted. Unit
	// servers leave it nil (the export needs a running Python
	// oscal-bridge); the production binary wires it via AttachOscalExporter.
	oscalExporter *oscal.Exporter
	// Slice 072: build-time-injected version metadata callback. When
	// non-nil, GET /v1/version is mounted (public, no auth). cmd/atlas
	// wires it once at startup via Config.VersionFieldsFn (which is
	// `versionFields` from cmd/atlas/version.go).
	versionFieldsFn func() VersionFields
	// Slice 073: platform_status reader/writer + bootstrap-token file
	// path. Wired by cmd/atlas; unit servers leave them nil and the
	// install-state routes 503.
	platformStatus     PlatformStatus
	platformResetter   PlatformResetter
	bootstrapTokenPath string
	// Slice 073: structured logger for non-request-bound events
	// (bootstrap-token deletion is the first). Falls back to
	// slog.Default() when nil.
	logger *slog.Logger
	// Slice 121: opt-in Prometheus `/metrics` fallback handler. Mounted
	// only when ATLAS_METRICS_FALLBACK_ENABLE=true (AC-15). When nil,
	// GET /metrics returns 404 (default off — P0-A3). Auth-exempted via
	// the same pattern slice 092 used for /v1/version (AC-16).
	metricsHandler http.Handler
}

// AttachAuthz wires the slice-035 OPA engine + decision audit writer.
// cmd/atlas constructs them once at startup with the DB pool + a
// DBRolesResolver. Unit servers can leave them unset to bypass authz
// (matching the slice-013/014 attach pattern). Once attached, the HTTP
// authz middleware runs on every non-exempt request.
func (s *Server) AttachAuthz(engine *authz.Engine, audit *authz.AuditWriter) {
	s.authzEngine = engine
	s.authzAudit = audit
}

// AttachMetricsHandler wires the slice-121 Prometheus `/metrics` fallback
// handler. cmd/atlas constructs it from the otel.Init Result when
// ATLAS_METRICS_FALLBACK_ENABLE=true. Unit servers leave it nil — GET
// /metrics returns 404 (default off; opt-in per P0-A3).
func (s *Server) AttachMetricsHandler(h http.Handler) {
	s.metricsHandler = h
}

// AttachOscalExporter wires the slice-030 OSCAL export pipeline.
// cmd/atlas constructs the Exporter at startup with the DB pool, a gRPC
// client to the Python oscal-bridge, and a signer. Unit servers leave it
// unset — the export needs a running bridge. Once attached, the
// POST /v1/audit-periods/{id}/oscal-export route is mounted.
func (s *Server) AttachOscalExporter(exporter *oscal.Exporter) {
	s.oscalExporter = exporter
}

// IssueBootstrapCredential mints a credential for the supplied tenant and
// returns the bearer token. cmd/atlas uses it once at startup; tests use it
// to mint per-test bearers. Production deployments call AdminCredentials.Issue
// from a separate admin client instead.
func (s *Server) IssueBootstrapCredential(tenantID string) (credstore.Credential, string, error) {
	return s.credStore.Issue(tenantID, "", nil, 0)
}

// IssueBootstrapAdminCredential mints an admin-flagged credential for the
// supplied tenant. Tests use it to drive the slice-014 schema registration
// flow (admin-only). Production deployments will graduate to a proper
// admin issuance path in a later slice.
func (s *Server) IssueBootstrapAdminCredential(tenantID string) (credstore.Credential, string, error) {
	return s.credStore.IssueAdmin(tenantID, 0)
}

// IssueBootstrapFixedAdminCredential mints an admin-flagged credential for
// tenantID whose bearer token is the caller-supplied deterministic token.
// Slice 037: the docker-compose self-host bundle's one-shot bootstrap
// container uses a pre-shared ATLAS_BOOTSTRAP_TOKEN to authenticate
// control-bundle uploads against the freshly started server.
func (s *Server) IssueBootstrapFixedAdminCredential(tenantID, token string) (credstore.Credential, error) {
	return s.credStore.IssueFixedAdmin(tenantID, token)
}

// IssueBootstrapApproverCredential mints an approver-flagged credential for
// the supplied tenant. Tests use it to drive the slice-018 FrameworkScope
// approval flow (approver-only). Production deployments will graduate to
// OPA-driven RBAC in slice 035.
func (s *Server) IssueBootstrapApproverCredential(tenantID string) (credstore.Credential, string, error) {
	return s.credStore.IssueApprover(tenantID, 0)
}

// IssueBootstrapOwnerCredential mints a credential carrying the supplied
// owner roles for tenantID. Slice 011 uses it to drive the manual-control
// attestation endpoint: the bearer must hold the control's `owner_role`
// to attest. Tests and (until slice 035) production owner-credential
// issuance go through here.
func (s *Server) IssueBootstrapOwnerCredential(tenantID string, roles []string) (credstore.Credential, string, error) {
	return s.credStore.IssueOwner(tenantID, roles, 0)
}

// RebindBearerUserIDForTests overrides the UserID field on the
// credential keyed by the supplied bearer plaintext. Slice 023
// integration tests use this to bind a bootstrap credential to a
// seeded users row id so the policy_acknowledgments composite FK
// passes. Slice 034's OIDC-callback path sets UserID at issue time
// from the IdP's `sub` claim; this hook bridges bootstrap creds (which
// default UserID to their own credential id) to seeded users rows for
// integration tests that don't run the OIDC dance.
func (s *Server) RebindBearerUserIDForTests(bearer, userID string) error {
	return s.credStore.RebindUserIDForTests(bearer, userID)
}

// Config groups the wiring inputs. Zero values yield a sane local setup.
type Config struct {
	RotationGrace time.Duration
	RegistrySeed  []schemaregistry.KindVersion
	// SchemaRegistry, when non-nil, replaces the default in-memory
	// registry with a DB-backed Service. The HTTP handler for slice 014
	// only mounts when this field is populated, so unit-only servers
	// keep the slice-003 IsRegistered surface.
	SchemaRegistry *schemaregistry.Service
	// IngestService, when non-nil, routes evidence pushes through the
	// slice-013 DB-backed ingestion stage (writes to evidence_records,
	// validates via schema registry, audits every attempt). When nil,
	// the slice-003 in-memory fallback runs — used by unit tests that
	// don't want a Postgres dependency.
	IngestService *ingest.Service
	// EvidencePushRate is the per-credential token-bucket replenish rate
	// for the slice-013 REST push endpoint. 0 disables rate limiting
	// (used by tests). Production defaults: 100 records/second.
	EvidencePushRate float64
	// ArtifactStore, when non-nil, wires the slice-036 S3 artifact store
	// into the HTTP server. Routes POST /v1/artifacts:upload and
	// GET /v1/artifacts/{id} only mount when this is populated.
	// Unit-only servers leave it nil — the platform binary constructs
	// the Store with an S3 client and presigner pointed at the
	// configured bucket (MinIO locally, S3 in prod).
	ArtifactStore *artifact.Store
	// EvidencePublisher is the slice-015 JetStream substrate. When
	// non-nil the push HTTP handler routes pushes through the stream
	// and the platform binary runs a consumer that drains the stream
	// into the ledger. When nil, push falls back to direct
	// Service.Process — backwards compat for unit servers and for the
	// dev mode without NATS.
	EvidencePublisher evidence.Publisher
	// VersionFieldsFn is the slice-072 build-time-injected version
	// metadata callback. When non-nil, GET /v1/version is mounted —
	// public, no auth, no tenancy. cmd/atlas wires in
	// `versionFields` from cmd/atlas/version.go. Unit-only servers
	// leave it nil and the route is simply absent.
	VersionFieldsFn func() VersionFields
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
	var reg schemaregistry.Registry
	if cfg.SchemaRegistry != nil {
		reg = cfg.SchemaRegistry
	} else {
		reg = schemaregistry.New(cfg.RegistrySeed)
	}
	idem := idemstore.New()
	connReg := connectorregistry.New(nil)

	grpcServer := grpc.NewServer(
		grpc.ChainUnaryInterceptor(
			recoverInterceptor(),
			authInterceptor(cred),
		),
	)
	evidencev1.RegisterEvidenceIngestServiceServer(grpcServer, evidence.New(cfg.IngestService, reg, idem))
	adminv1.RegisterAdminCredentialsServiceServer(grpcServer, admin.New(cred))
	connectorsv1.RegisterConnectorRegistryServiceServer(grpcServer, connectors.New(connReg))

	return &Server{
		GRPC:              grpcServer,
		credStore:         cred,
		registry:          reg,
		idemStore:         idem,
		connectorRegistry: connReg,
		ingestService:     cfg.IngestService,
		evidencePushRate:  cfg.EvidencePushRate,
		artifactStore:     cfg.ArtifactStore,
		evidencePublisher: cfg.EvidencePublisher,
		versionFieldsFn:   cfg.VersionFieldsFn,
	}
}

// AttachEvidencePublisher wires the slice-015 substrate after Server
// construction. The platform binary builds the JetStreamPublisher with
// its Conn and calls this once at startup. Unit servers don't need to
// call it.
func (s *Server) AttachEvidencePublisher(pub evidence.Publisher) {
	s.evidencePublisher = pub
}

// AttachArtifactStore wires the slice-036 artifact store after Server
// construction. The platform binary builds the store with its S3 client
// + presigner + bucket and calls this once at startup. Unit servers
// don't need to call it.
func (s *Server) AttachArtifactStore(store *artifact.Store) {
	s.artifactStore = store
}

// AttachAPIKeyStore wires the slice-034 DB-backed bearer credential store.
// The HTTP auth middleware uses it as a fallback for tokens not known by
// the in-memory credstore; the admin-credential HTTP routes
// (POST/GET/rotate/revoke under /v1/admin/credentials) only mount when
// this is set. cmd/atlas wires it once at startup with a BEARER_HASH_KEY-
// backed bearer.Hasher.
func (s *Server) AttachAPIKeyStore(store *apikeystore.Store) {
	s.apikeyStore = store
}

// AttachAuthHandler wires the slice-034 user-facing auth routes. The
// /auth/* routes (OIDC login/callback, local login, logout) only mount
// when this is set. cmd/atlas wires it once at startup with the OIDC
// authenticator, users store, and sessions store.
func (s *Server) AttachAuthHandler(h *authapi.Handler) {
	s.authHandler = h
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
