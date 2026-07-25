package api

import (
	"github.com/go-chi/chi/v5"

	metricsapi "github.com/mgoodric/security-atlas/internal/api/metrics"
)

// registerMetrics registers its domain's routes onto the shared root
// router. It is one of the slice-436 per-domain registrars extracted from
// the former monolithic httpHandler(); the root it receives already carries
// the full shared middleware chain (security headers, request ID, CORS,
// JWT, credential gate, tenancy, authz, feature-flag cache), so every route
// registered here is gated identically to how it was inline (slice 436
// AC-6). Behavior is unchanged: routes, handlers, and declaration order are
// preserved verbatim.
func (s *Server) registerMetrics(root *chi.Mux) {
	// Slice 076: metrics catalog + cascade + observation store. Routes
	// appended per the parallel-batch convention (chi.Mux rejects two
	// Mounts at "/"). The literal-segment route /v1/metrics/cascade is
	// declared BEFORE /v1/metrics/{id} so chi's declaration-order match
	// keeps the cascade route ahead of the generic /{id} route. The
	// /{id}/observations + /{id}/inputs + /{id}/target sub-routes are
	// distinct path shapes, no shadowing.
	metricsH := metricsapi.New(s.dbPool)
	root.Get("/v1/metrics", metricsH.ListCatalog)
	root.Get("/v1/metrics/cascade", metricsH.GetCascade)
	root.Get("/v1/metrics/{id}", metricsH.GetCatalog)
	root.Get("/v1/metrics/{id}/observations", metricsH.ListObservations)
	root.Post("/v1/metrics/{id}/inputs", metricsH.CreateInput)
	root.Get("/v1/metrics/{id}/target", metricsH.GetTarget)
	root.Put("/v1/metrics/{id}/target", metricsH.UpsertTarget)
}
