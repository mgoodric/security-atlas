package api

import (
	"github.com/go-chi/chi/v5"

	oscalcomponentsapi "github.com/mgoodric/security-atlas/internal/api/oscalcomponents"
)

// registerOSCAL registers its domain's routes onto the shared root
// router. It is one of the slice-436 per-domain registrars extracted from
// the former monolithic httpHandler(); the root it receives already carries
// the full shared middleware chain (security headers, request ID, CORS,
// JWT, credential gate, tenancy, authz, feature-flag cache), so every route
// registered here is gated identically to how it was inline (slice 436
// AC-6). Behavior is unchanged: routes, handlers, and declaration order are
// preserved verbatim.
func (s *Server) registerOSCAL(root *chi.Mux) {
	// Slice 589: OSCAL vendor-claim read API + operator disposition. Pure
	// reads over the slice-512 imported component-definitions + the
	// accept/reject/needs-info disposition write. A vendor claim is an
	// ASSERTION, not platform-verified evidence: the disposition records the
	// human decision and NEVER auto-satisfies a control (canvas invariant #2,
	// P0-512-1). The Store writes ONLY the claim's disposition metadata + an
	// append-only audit row -- it never touches control_evaluations or the
	// evidence ledger, and never the oscal-bridge (the read path is over
	// persisted Postgres rows). Routes appended per the parallel-batch
	// convention (chi rejects two Mounts at "/"). The {id}:verb shape mirrors
	// the slice-025 walkthroughs /{id}:finalize precedent.
	// Slice 660 / #2170 / #2171: vendor claims are a feature of Third-party
	// risk, switched by `vendor.claims` (formerly `oscal.export`). Its routes
	// are in the module registry (featureflag.Modules), so the module gate in
	// the authorization chain answers 404 + {"error":"feature disabled"} when
	// Third-party risk or the feature is off. `vendor.claims` is OFF by
	// default pending GA.
	oscalComponentsH := oscalcomponentsapi.New(oscalcomponentsapi.NewStore(s.dbPool))
	root.Get("/v1/oscal/component-definitions", oscalComponentsH.ListDefinitions)
	root.Get("/v1/oscal/component-definitions/{id}", oscalComponentsH.GetDefinition)
	root.Post("/v1/oscal/component-claims/{id}:accept", oscalComponentsH.Accept)
	root.Post("/v1/oscal/component-claims/{id}:reject", oscalComponentsH.Reject)
	root.Post("/v1/oscal/component-claims/{id}:needs-info", oscalComponentsH.NeedsInfo)
	// Slice 620: operator maps an UNMAPPED vendor claim (slice-512
	// scf_anchor_id IS NULL) to a canonical SCF anchor. grc_engineer-gated;
	// validates the anchor exists in the bundled catalog; sets the crosswalk +
	// appends an append-only mapping-audit row. Requirement -> SCF anchor only
	// (invariant #7); the claim stays a claim -- mapping NEVER writes
	// control_evaluations (invariant #2 / P0-512-1).
	root.Patch("/v1/oscal/component-claims/{id}/scf-anchor", oscalComponentsH.MapScfAnchor)
}
