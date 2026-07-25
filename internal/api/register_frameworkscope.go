package api

import (
	"github.com/go-chi/chi/v5"

	csfassessmentapi "github.com/mgoodric/security-atlas/internal/api/csfassessment"
	fwscopesapi "github.com/mgoodric/security-atlas/internal/api/frameworkscopes"
	"github.com/mgoodric/security-atlas/internal/csfassessment"
	"github.com/mgoodric/security-atlas/internal/frameworkscope"
	"github.com/mgoodric/security-atlas/internal/scope"
)

// registerFrameworkScope registers its domain's routes onto the shared root
// router. It is one of the slice-436 per-domain registrars extracted from
// the former monolithic httpHandler(); the root it receives already carries
// the full shared middleware chain (security headers, request ID, CORS,
// JWT, credential gate, tenancy, authz, feature-flag cache), so every route
// registered here is gated identically to how it was inline (slice 436
// AC-6). Behavior is unchanged: routes, handlers, and declaration order are
// preserved verbatim.
func (s *Server) registerFrameworkScope(root *chi.Mux) {
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
	// Slice 515: NIST CSF 2.0 Tier / Profile assessment workflow. Tenant-
	// confidential assessment state over the shared CSF crosswalk; edit routes
	// are grc_engineer/admin-gated inside the handler, read routes are any
	// tenant credential. The gap view reads the existing crosswalk traversal
	// (invariant #1) rather than re-storing it. Literal-segment routes
	// ({kind}/selections) are registered before the {requirement_id} variant
	// per chi's same-method resolution order.
	csfH := csfassessmentapi.New(csfassessment.NewStore(s.dbPool))
	root.Put("/v1/csf/tier", csfH.PutTier)
	root.Get("/v1/csf/tier", csfH.GetTier)
	root.Get("/v1/csf/gap", csfH.Gap)
	root.Put("/v1/csf/profiles/{kind}/selections", csfH.PutSelection)
	root.Delete("/v1/csf/profiles/{kind}/selections/{requirement_id}", csfH.DeleteSelection)
	root.Put("/v1/csf/profiles/{kind}", csfH.PutProfile)
	root.Get("/v1/csf/profiles/{kind}", csfH.GetProfile)
}
