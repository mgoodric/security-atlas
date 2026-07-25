package api

import (
	"github.com/go-chi/chi/v5"

	oscalcomponentsapi "github.com/mgoodric/security-atlas/internal/api/oscalcomponents"
	"github.com/mgoodric/security-atlas/internal/featureflag"
)

// registerOSCAL registers its domain's routes onto the shared root
// router. It is one of the slice-436 per-domain registrars extracted from
// the former monolithic httpHandler(); the root it receives already carries
// the full shared middleware chain (security headers, request ID, CORS,
// JWT, credential gate, tenancy, authz, feature-flag cache), so every route
// registered here is gated identically to how it was inline (slice 436
// AC-6). Behavior is unchanged: routes, handlers, and declaration order are
// preserved verbatim.
func (s *Server) registerOSCAL(root *chi.Mux, featureFlagStore *featureflag.Store) {
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
	// Slice 660: the OSCAL vendor-claims module gates on the `oscal.export`
	// feature flag. The whole module (list + get + accept/reject/needs-info
	// disposition + scf-anchor map) is wrapped in a featureflag.Gate group so
	// a flag-off tenant gets a clean 404 + {"error":"feature disabled"} (no
	// internal detail leak — slice 367) on EVERY route, not just nav-hidden.
	// `oscal.export` is OFF by default pending GA, which also removes the
	// user-facing exposure of the edge-broken OSCAL page (ATLAS-001/659/683)
	// regardless of 659's outcome. The Gate reads the caller's tenant flag
	// (RLS — invariant #6) and falls open to the Seed default on a DB error.
	oscalComponentsH := oscalcomponentsapi.New(oscalcomponentsapi.NewStore(s.dbPool))
	root.Group(func(r chi.Router) {
		r.Use(featureflag.Gate(featureFlagStore, "oscal.export"))
		r.Get("/v1/oscal/component-definitions", oscalComponentsH.ListDefinitions)
		r.Get("/v1/oscal/component-definitions/{id}", oscalComponentsH.GetDefinition)
		r.Post("/v1/oscal/component-claims/{id}:accept", oscalComponentsH.Accept)
		r.Post("/v1/oscal/component-claims/{id}:reject", oscalComponentsH.Reject)
		r.Post("/v1/oscal/component-claims/{id}:needs-info", oscalComponentsH.NeedsInfo)
		// Slice 620: operator maps an UNMAPPED vendor claim (slice-512
		// scf_anchor_id IS NULL) to a canonical SCF anchor. grc_engineer-gated;
		// validates the anchor exists in the bundled catalog; sets the crosswalk +
		// appends an append-only mapping-audit row. Requirement -> SCF anchor only
		// (invariant #7); the claim stays a claim -- mapping NEVER writes
		// control_evaluations (invariant #2 / P0-512-1).
		r.Patch("/v1/oscal/component-claims/{id}/scf-anchor", oscalComponentsH.MapScfAnchor)
	})
}
