package api

import (
	"github.com/go-chi/chi/v5"

	"github.com/mgoodric/security-atlas/internal/api/vendors"
	"github.com/mgoodric/security-atlas/internal/vendor"
)

// registerVendor registers its domain's routes onto the shared root
// router. It is one of the slice-436 per-domain registrars extracted from
// the former monolithic httpHandler(); the root it receives already carries
// the full shared middleware chain (security headers, request ID, CORS,
// JWT, credential gate, tenancy, authz, feature-flag cache), so every route
// registered here is gated identically to how it was inline (slice 436
// AC-6). Behavior is unchanged: routes, handlers, and declaration order are
// preserved verbatim.
func (s *Server) registerVendor(root *chi.Mux, vendorStore *vendor.Store) {
	// Slice 024: vendor lite module — CRUD + burndown. The burndown route
	// is registered before /v1/vendors/{id} so chi's router matches the
	// literal segment first (chi resolves routes in declaration order
	// inside the same method).
	//
	// Slice 273: the same vendor.Store also backs the board-pack
	// vendor_burndown section adapter wired below. Single store = single
	// resource; the adapter reuses vendor.Store.Burndown.
	vendorsH := vendors.New(vendorStore)
	root.Post("/v1/vendors", vendorsH.CreateVendor)
	root.Get("/v1/vendors", vendorsH.ListVendors)
	root.Get("/v1/vendors/burndown", vendorsH.Burndown)
	root.Get("/v1/vendors/{id}", vendorsH.GetVendor)
	root.Patch("/v1/vendors/{id}", vendorsH.UpdateVendor)
	root.Delete("/v1/vendors/{id}", vendorsH.DeleteVendor)
	// Slice 688: per-review history ledger — list + record a completed review.
	root.Get("/v1/vendors/{id}/reviews", vendorsH.ListReviews)
	root.Post("/v1/vendors/{id}/reviews", vendorsH.RecordReview)
}
