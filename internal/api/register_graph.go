package api

import (
	"github.com/go-chi/chi/v5"

	apievidence "github.com/mgoodric/security-atlas/internal/api/evidence"
	"github.com/mgoodric/security-atlas/internal/api/schemaregistry"
	"github.com/mgoodric/security-atlas/internal/api/scopes"
	"github.com/mgoodric/security-atlas/internal/api/ucfcoverage"
	"github.com/mgoodric/security-atlas/internal/eval"
	"github.com/mgoodric/security-atlas/internal/frameworkscope"
	"github.com/mgoodric/security-atlas/internal/scope"
)

// registerGraph registers its domain's routes onto the shared root
// router. It is one of the slice-436 per-domain registrars extracted from
// the former monolithic httpHandler(); the root it receives already carries
// the full shared middleware chain (security headers, request ID, CORS,
// JWT, credential gate, tenancy, authz, feature-flag cache), so every route
// registered here is gated identically to how it was inline (slice 436
// AC-6). Behavior is unchanged: routes, handlers, and declaration order are
// preserved verbatim.
func (s *Server) registerGraph(root *chi.Mux) {
	// Slice 008: UCF graph traversal HTTP API. Three read endpoints
	// query the requirement-anchor-control graph through the SCF spine
	// (canvas §3 / Plans/UCF_GRAPH_MODEL.md). Routes are appended
	// directly to the root chi router — adding a second
	// chi.NewRouter().Mount("/", ...) would panic. The
	// /v1/anchors/{id}/requirements route under this handler supersedes
	// the slice-006 in-memory placeholder (which was removed from
	// anchors.Routes()); the response shape is a superset of the
	// in-memory one so slice-007's TestRequirementsForAnchor still
	// passes.
	// Slice 256 — wire the eval engine, scope store, and frameworkscope
	// store so /v1/controls/{id}/coverage can carry the per-row weighted
	// `coverage` field (strength × 30-day effectiveness, intersected
	// with the framework's scope predicate). Unit tests that don't need
	// these dependencies leave the field omitted by skipping
	// AttachCoverage — the slice-008 shape stays backwards-compatible.
	ucfH := ucfcoverage.New(s.dbPool).AttachCoverage(
		eval.NewEngine(eval.NewStore(s.dbPool), scope.NewStore(s.dbPool)),
		scope.NewStore(s.dbPool),
		frameworkscope.NewStore(s.dbPool),
	)
	ucfH.RegisterRoutes(root)
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
}
