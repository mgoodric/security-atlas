package api

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/mgoodric/security-atlas/internal/api/admincreds"
	"github.com/mgoodric/security-atlas/internal/api/anchors"
	"github.com/mgoodric/security-atlas/internal/api/anchorseed"
	artifactsapi "github.com/mgoodric/security-atlas/internal/api/artifacts"
	auditapi "github.com/mgoodric/security-atlas/internal/api/audit"
	"github.com/mgoodric/security-atlas/internal/api/authctx"
	controlsapi "github.com/mgoodric/security-atlas/internal/api/controls"
	"github.com/mgoodric/security-atlas/internal/api/credstore"
	apievidence "github.com/mgoodric/security-atlas/internal/api/evidence"
	exceptionsapi "github.com/mgoodric/security-atlas/internal/api/exceptions"
	fwscopesapi "github.com/mgoodric/security-atlas/internal/api/frameworkscopes"
	risksapi "github.com/mgoodric/security-atlas/internal/api/risks"
	"github.com/mgoodric/security-atlas/internal/api/schemaregistry"
	"github.com/mgoodric/security-atlas/internal/api/scopes"
	"github.com/mgoodric/security-atlas/internal/api/tenancymw"
	"github.com/mgoodric/security-atlas/internal/api/vendors"
	"github.com/mgoodric/security-atlas/internal/artifact"
	"github.com/mgoodric/security-atlas/internal/audit"
	"github.com/mgoodric/security-atlas/internal/auth/apikeystore"
	"github.com/mgoodric/security-atlas/internal/control"
	"github.com/mgoodric/security-atlas/internal/db/dbx"
	"github.com/mgoodric/security-atlas/internal/exception"
	"github.com/mgoodric/security-atlas/internal/frameworkscope"
	"github.com/mgoodric/security-atlas/internal/risk"
	"github.com/mgoodric/security-atlas/internal/scope"
	"github.com/mgoodric/security-atlas/internal/vendor"
	sdk "github.com/mgoodric/security-atlas/pkg/sdk-go"
)

// AttachDB wires a pgx pool into the server. The HTTP handlers under /v1/
// require it; absent a pool, RunHTTP returns an error. Set once at startup.
func (s *Server) AttachDB(pool *pgxpool.Pool) { s.dbPool = pool }

// HTTPHandlerForTests exposes the assembled HTTP handler so tests can
// drive it via httptest.NewServer. Production callers should use RunHTTP.
// Returns nil when no DB pool has been attached (handlers need one).
func (s *Server) HTTPHandlerForTests() http.Handler {
	if s.dbPool == nil {
		return nil
	}
	return s.httpHandler()
}

// httpHandler builds the platform's HTTP router: anchors + frameworks API
// under /v1/, auth middleware shared with the gRPC server, CORS for the
// local dev frontend.
func (s *Server) httpHandler() http.Handler {
	root := chi.NewRouter()
	root.Use(corsMiddleware)
	// Slice 034: /auth/* (login/callback/logout) is bearer-exempt — users
	// don't have a bearer yet at the moment they sign in. The middleware
	// must skip the prefix. Note: /v1/admin/credentials* DOES go through
	// bearer auth (admin endpoints require an admin credential).
	root.Use(httpAuthMiddlewareWithExemptions(s.credStore, s.apikeyStore, "/auth/"))
	// Slice 033: lift the authenticated credential's tenant id onto the
	// request context so every downstream handler — and every database
	// transaction it opens — runs under the right `app.current_tenant`
	// GUC. Constitutional invariant 6 enforcement. The middleware is a
	// no-op when no credential is in context (bearer-exempt paths like
	// /auth/* keep their own request-supplied tenant resolution).
	root.Use(tenancymw.Middleware)

	mappings := anchorseed.New()
	queries := dbx.New(s.dbPool)
	root.Mount("/", anchors.New(queries, mappings).Routes())
	if dbSvc, ok := s.registry.(*schemaregistry.Service); ok && dbSvc != nil {
		// chi forbids two Mounts on the same path. Attach each schema
		// route directly to the root router so they live alongside the
		// anchors handlers.
		h := schemaregistry.NewHTTPHandler(dbSvc)
		root.Get("/v1/schemas", h.ListHTTP)
		root.Get("/v1/schemas/{kind}/{semver}", h.GetHTTP)
		root.Post("/v1/schemas", h.RegisterHTTP)
	}
	// Slice 017: scope dimensions, scope cells, and per-control applicability.
	// chi.Mux rejects mounting two routers on the same prefix, so the slice's
	// individual routes are wired with Method() one-by-one onto the root.
	scopesH := scopes.New(scope.NewStore(s.dbPool))
	root.Post("/v1/scopes/cells", scopesH.CreateCell)
	root.Get("/v1/scopes/cells", scopesH.ListCells)
	root.Get("/v1/scopes/dimensions", scopesH.ListDimensions)
	root.Get("/v1/controls/{id}/applicability", scopesH.ControlApplicability)
	// Slice 013: evidence ledger write API. Only mounted when an ingest
	// service has been wired in (DB-backed). Unit-only servers leave it
	// nil and exclusively use the slice-003 gRPC fallback path.
	//
	// Slice 015: when the JetStream Publisher is wired
	// (s.evidencePublisher), the handler routes pushes through the
	// stream and acks at stream-commit time (AC-2). When nil, the
	// handler falls back to direct Service.Process — the slice-013
	// path — for unit tests and dev mode without NATS.
	if s.ingestService != nil {
		evidenceH := apievidence.NewHTTPHandler(s.ingestService, s.evidencePushRate)
		if s.evidencePublisher != nil {
			evidenceH = evidenceH.WithPublisher(s.evidencePublisher)
		}
		root.Post("/v1/evidence:push", evidenceH.PushHTTP)
	}
	// Slice 019: risk register CRUD + 5x5 heatmap. Routes appended per the
	// parallel-batch convention — chi.Mux rejects two Mounts at "/", and other
	// batches are also appending here, so order-of-append must not matter.
	risksH := risksapi.New(risk.NewStore(s.dbPool))
	root.Post("/v1/risks", risksH.CreateRisk)
	root.Get("/v1/risks", risksH.ListRisks)
	root.Get("/v1/risks/heatmap", risksH.Heatmap)
	root.Get("/v1/risks/{id}", risksH.GetRisk)
	root.Delete("/v1/risks/{id}", risksH.DeleteRisk)
	// Slice 024: vendor lite module — CRUD + burndown. The burndown route
	// is registered before /v1/vendors/{id} so chi's router matches the
	// literal segment first (chi resolves routes in declaration order
	// inside the same method).
	vendorsH := vendors.New(vendor.NewStore(s.dbPool))
	root.Post("/v1/vendors", vendorsH.CreateVendor)
	root.Get("/v1/vendors", vendorsH.ListVendors)
	root.Get("/v1/vendors/burndown", vendorsH.Burndown)
	root.Get("/v1/vendors/{id}", vendorsH.GetVendor)
	root.Patch("/v1/vendors/{id}", vendorsH.UpdateVendor)
	root.Delete("/v1/vendors/{id}", vendorsH.DeleteVendor)
	// Slice 018: FrameworkScope predicate + four-state workflow + intersection
	// compute. Routes appended per the parallel-batch convention (chi rejects
	// two Mounts at "/"). The /effective-scope route lives under
	// /v1/controls/{id}/ alongside the slice-017 /applicability route.
	fwH := fwscopesapi.New(
		frameworkscope.NewStore(s.dbPool),
		scope.NewStore(s.dbPool),
	)
	root.Post("/v1/framework-scopes", fwH.Create)
	root.Get("/v1/framework-scopes", fwH.List)
	// Sub-resource transitions are PATCH, distinct from the generic PATCH
	// on /v1/framework-scopes/{id} (which edits predicate). chi resolves
	// the literal-segment routes first within the same method.
	root.Patch("/v1/framework-scopes/{id}/submit", fwH.Submit)
	root.Patch("/v1/framework-scopes/{id}/approve", fwH.Approve)
	root.Patch("/v1/framework-scopes/{id}/activate", fwH.Activate)
	root.Get("/v1/framework-scopes/{id}", fwH.Get)
	root.Patch("/v1/framework-scopes/{id}", fwH.Patch)
	root.Get("/v1/controls/{id}/effective-scope", fwH.EffectiveScope)
	// Slice 036: S3 artifact store — large-payload upload + short-TTL
	// signed download. Routes only mount when an artifact store has been
	// wired in via Server.AttachArtifactStore (or Config.ArtifactStore).
	// Unit-only servers leave it nil.
	if s.artifactStore != nil {
		artifactsH := artifactsapi.New(s.artifactStore)
		root.Post("/v1/artifacts:upload", artifactsH.Upload)
		root.Get("/v1/artifacts/{id}", artifactsH.Get)
	}
	// Slice 009: control-as-code bundle upload. Admin-only — same auth gate
	// as the schema registry's POST /v1/schemas. The handler reads either
	// multipart (a .tar.gz bundle) or JSON (inline manifest YAML) per
	// docs/spec/control-bundle.md §4.
	var controlsRegistry control.SchemaRegistry
	if dbSvc, ok := s.registry.(*schemaregistry.Service); ok && dbSvc != nil {
		controlsRegistry = dbSvc
	}
	controlsH := controlsapi.New(control.NewStore(s.dbPool), controlsRegistry)
	root.Post("/v1/controls:upload-bundle", controlsH.UploadBundle)
	// Slice 011: manual control attestation endpoint. Wired only when
	// the slice-013 ingest service is wired in (this slice writes
	// evidence records via that service). When the artifact store is
	// also wired, attestations may cite an uploaded artifact_id.
	if s.ingestService != nil {
		attestH := controlsapi.NewAttestHandler(s.dbPool, s.ingestService, attestUploader(s.artifactStore))
		root.Get("/v1/controls/{id}/attest-form", attestH.AttestForm)
		root.Post("/v1/controls/{id}/attestations", attestH.Submit)
	}
	// Slice 026: sample-pull primitives. Routes appended per the parallel-batch
	// convention (chi rejects two Mounts at "/"). The annotation sub-resource
	// route is declared after the literal /v1/samples/{id} so chi's
	// declaration-order match keeps the literal-segment first.
	auditH := auditapi.New(audit.NewStore(s.dbPool))
	root.Post("/v1/populations", auditH.CreatePopulation)
	root.Get("/v1/populations/{id}", auditH.GetPopulation)
	root.Post("/v1/samples", auditH.DrawSample)
	root.Get("/v1/samples/{id}", auditH.GetSample)
	root.Post("/v1/samples/{id}/annotations", auditH.Annotate)
	root.Get("/v1/samples/{id}/annotations", auditH.ListAnnotations)
	// Slice 021: exception / waiver workflow. Routes appended per the
	// parallel-batch convention -- chi.Mux rejects two Mounts at "/", so
	// individual routes are registered onto the root. Literal-segment
	// routes (/expiring) are declared before /{id} so chi's
	// declaration-order match keeps the calendar route ahead of the
	// generic UUID-id route.
	exceptionsH := exceptionsapi.New(exception.NewStore(s.dbPool))
	root.Post("/v1/exceptions", exceptionsH.CreateException)
	root.Get("/v1/exceptions", exceptionsH.ListExceptions)
	root.Get("/v1/exceptions/expiring", exceptionsH.Expiring)
	root.Get("/v1/exceptions/{id}", exceptionsH.GetException)
	root.Get("/v1/exceptions/{id}/audit-log", exceptionsH.AuditLog)
	root.Patch("/v1/exceptions/{id}/approve", exceptionsH.Approve)
	root.Patch("/v1/exceptions/{id}/deny", exceptionsH.Deny)
	root.Patch("/v1/exceptions/{id}/activate", exceptionsH.Activate)
	// Slice 034: admin credentials HTTP API + auth routes. Routes append
	// per the parallel-batch convention. Admin-credential routes require
	// the bearer auth middleware (admin gate inside the handler). The
	// /auth/* routes are exempted from bearer auth in the middleware
	// (httpAuthMiddlewareWithExemptions above).
	if s.apikeyStore != nil {
		admincredsH := admincreds.New(s.apikeyStore)
		root.Post("/v1/admin/credentials", admincredsH.Issue)
		root.Get("/v1/admin/credentials", admincredsH.List)
		root.Post("/v1/admin/credentials/{id}/rotate", admincredsH.Rotate)
		root.Post("/v1/admin/credentials/{id}/revoke", admincredsH.Revoke)
	}
	if s.authHandler != nil {
		root.Get("/auth/oidc/login", s.authHandler.OIDCLogin)
		root.Get("/auth/oidc/callback", s.authHandler.OIDCCallback)
		root.Post("/auth/local/login", s.authHandler.LocalLogin)
		root.Post("/auth/logout", s.authHandler.Logout)
	}
	return root
}

// attestUploader returns the slice-011 ArtifactUploader adapter over the
// slice-036 *artifact.Store, or nil when no artifact store has been
// wired. The slice-011 handler tolerates a nil uploader — it just
// rejects requests that cite an artifact_id with 503.
func attestUploader(store *artifact.Store) controlsapi.ArtifactUploader {
	if store == nil {
		return nil
	}
	return &storeArtifactAdapter{store: store}
}

type storeArtifactAdapter struct {
	store *artifact.Store
}

// PayloadURIFor resolves artifactID through artifact.Store.Get (which
// enforces RLS via the tenant context) and returns the canonical s3://
// URI for the artifact. Cross-tenant lookups return ErrNotFound, which
// the handler surfaces as 404.
func (a *storeArtifactAdapter) PayloadURIFor(ctx context.Context, artifactID uuid.UUID) (string, error) {
	art, err := a.store.Get(ctx, artifactID)
	if err != nil {
		return "", err
	}
	return art.PayloadURI(a.store.Bucket()), nil
}

// RunHTTP starts the HTTP server on addr (e.g., ":8080") and blocks until
// ctx is canceled, at which point it shuts down within a 5-second grace.
// Returns an error if no DB pool has been attached.
func (s *Server) RunHTTP(ctx context.Context, addr string) error {
	if s.dbPool == nil {
		return errors.New("api: HTTP server requires a DB pool (call Server.AttachDB before RunHTTP)")
	}
	srv := &http.Server{
		Addr:              addr,
		Handler:           s.httpHandler(),
		ReadHeaderTimeout: 5 * time.Second,
	}
	errCh := make(chan error, 1)
	go func() { errCh <- srv.ListenAndServe() }()
	select {
	case <-ctx.Done():
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		return srv.Shutdown(shutdownCtx)
	case err := <-errCh:
		if errors.Is(err, http.ErrServerClosed) {
			return nil
		}
		return err
	}
}

// corsMiddleware allows the Next.js dev origin to call the API with the
// bearer token. Production frontends served from the same origin don't
// trigger CORS.
func corsMiddleware(next http.Handler) http.Handler {
	const devOrigin = "http://localhost:3000"
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		origin := r.Header.Get("Origin")
		if origin == devOrigin {
			w.Header().Set("Access-Control-Allow-Origin", devOrigin)
			w.Header().Set("Vary", "Origin")
			w.Header().Set("Access-Control-Allow-Methods", "GET, OPTIONS")
			w.Header().Set("Access-Control-Allow-Headers", "Authorization, Content-Type")
			w.Header().Set("Access-Control-Allow-Credentials", "true")
		}
		if r.Method == http.MethodOptions {
			w.WriteHeader(http.StatusNoContent)
			return
		}
		next.ServeHTTP(w, r)
	})
}

// httpAuthMiddlewareWithExemptions is the HTTP auth middleware that:
//  1. Skips bearer auth for request paths whose prefix matches any exempt.
//     The /auth/* routes need this because the user has no bearer yet at
//     the moment of sign-in.
//  2. Stacks a DB-backed apikeystore.Store as a fallback for tokens that
//     the in-memory credstore does not know about. Connector pushes use
//     DB-backed keys; bootstrap admin credentials use in-memory.
func httpAuthMiddlewareWithExemptions(store *credstore.Store, apikeys *apikeystore.Store, exempt ...string) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			for _, p := range exempt {
				if p != "" && strings.HasPrefix(r.URL.Path, p) {
					next.ServeHTTP(w, r)
					return
				}
			}
			token, ok := extractBearerFromHTTP(r)
			if !ok {
				writeAuthError(w, "authorization must be `Bearer <token>`")
				return
			}
			cred, err := store.Authenticate(token)
			if err != nil {
				// Fall through to the DB-backed apikeystore for slice-034
				// keys that were issued via /v1/admin/credentials.
				if errors.Is(err, credstore.ErrUnknownKey) && apikeys != nil {
					dbCred, dbErr := apikeys.Authenticate(r.Context(), token)
					if dbErr == nil {
						ctx := authctx.WithCredential(r.Context(), dbCred)
						next.ServeHTTP(w, r.WithContext(ctx))
						return
					}
					writeAuthError(w, "invalid or revoked bearer token")
					return
				}
				if errors.Is(err, credstore.ErrUnknownKey) {
					writeAuthError(w, "invalid or revoked bearer token")
					return
				}
				http.Error(w, "internal error", http.StatusInternalServerError)
				return
			}
			ctx := authctx.WithCredential(r.Context(), cred)
			next.ServeHTTP(w, r.WithContext(ctx))
		})
	}
}

func extractBearerFromHTTP(r *http.Request) (string, bool) {
	auth := r.Header.Get(sdk.MetadataAuthorization)
	if auth == "" {
		return "", false
	}
	parts := strings.SplitN(strings.TrimSpace(auth), " ", 2)
	if len(parts) != 2 || !strings.EqualFold(parts[0], "bearer") {
		return "", false
	}
	tok := strings.TrimSpace(parts[1])
	return tok, tok != ""
}

func writeAuthError(w http.ResponseWriter, msg string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusUnauthorized)
	_, _ = w.Write([]byte(`{"error":"` + msg + `"}`))
}
