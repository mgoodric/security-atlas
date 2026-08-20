package api

import (
	"github.com/go-chi/chi/v5"

	personnelsecurityapi "github.com/mgoodric/security-atlas/internal/api/personnelsecurity"
	"github.com/mgoodric/security-atlas/internal/personnelsecurity"
)

// registerPersonnelSecurity registers the OE-663 personnel-security
// checklist routes onto the shared root router, following the slice-436
// per-domain registrar convention: the root it receives already carries
// the full shared middleware chain (security headers, request ID, CORS,
// JWT, credential gate, tenancy, authz, feature-flag cache), so every
// route registered here is gated identically to its siblings.
//
// The tenant-operator surface over the OE-630 store: list + get expose
// checklist state; create-manual and complete-item are the two write
// verbs (complete-item is what emits the personnel_security.workflow.v1
// CC1 evidence record). HRIS-triggered creation stays connector-side —
// deliberately no HTTP route. Routes appended per the parallel-batch
// convention (chi.Mux rejects two Mounts at "/"). The literal-segment
// /checklist-items/ subtree cannot shadow /checklists/{id} — distinct
// segments — so declaration order within the group is free.
func (s *Server) registerPersonnelSecurity(root *chi.Mux) {
	store := personnelsecurity.NewStore(s.dbPool)
	reader := personnelsecurityapi.NewReadStore(s.dbPool)
	h := personnelsecurityapi.New(store, reader)
	root.Get("/v1/personnel-security/checklists", h.ListChecklists)
	root.Post("/v1/personnel-security/checklists", h.CreateChecklist)
	root.Get("/v1/personnel-security/checklists/{id}", h.GetChecklist)
	root.Post("/v1/personnel-security/checklist-items/{id}/complete", h.CompleteItem)
}
