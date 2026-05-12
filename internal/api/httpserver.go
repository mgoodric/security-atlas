package api

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/mgoodric/security-atlas/internal/api/anchors"
	"github.com/mgoodric/security-atlas/internal/api/anchorseed"
	artifactsapi "github.com/mgoodric/security-atlas/internal/api/artifacts"
	"github.com/mgoodric/security-atlas/internal/api/authctx"
	controlsapi "github.com/mgoodric/security-atlas/internal/api/controls"
	"github.com/mgoodric/security-atlas/internal/api/credstore"
	apievidence "github.com/mgoodric/security-atlas/internal/api/evidence"
	fwscopesapi "github.com/mgoodric/security-atlas/internal/api/frameworkscopes"
	risksapi "github.com/mgoodric/security-atlas/internal/api/risks"
	"github.com/mgoodric/security-atlas/internal/api/schemaregistry"
	"github.com/mgoodric/security-atlas/internal/api/scopes"
	"github.com/mgoodric/security-atlas/internal/api/vendors"
	"github.com/mgoodric/security-atlas/internal/control"
	"github.com/mgoodric/security-atlas/internal/db/dbx"
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
	root.Use(httpAuthMiddleware(s.credStore))

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
	if s.ingestService != nil {
		evidenceH := apievidence.NewHTTPHandler(s.ingestService, s.evidencePushRate)
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
	return root
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

// httpAuthMiddleware mirrors the gRPC auth interceptor: extract bearer
// from the Authorization header, resolve via credstore, attach the
// credential to context.
func httpAuthMiddleware(store *credstore.Store) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			token, ok := extractBearerFromHTTP(r)
			if !ok {
				writeAuthError(w, "authorization must be `Bearer <token>`")
				return
			}
			cred, err := store.Authenticate(token)
			if err != nil {
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
