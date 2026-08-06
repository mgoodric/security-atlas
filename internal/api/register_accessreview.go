package api

import (
	"github.com/go-chi/chi/v5"

	"github.com/mgoodric/security-atlas/internal/accessreview"
	accessreviewsapi "github.com/mgoodric/security-atlas/internal/api/accessreviews"
)

// registerAccessReview registers the OE-670 access-review campaign
// routes onto the shared root router, following the slice-436
// per-domain registrar convention: the root it receives already carries
// the full shared middleware chain (security headers, request ID, CORS,
// JWT, credential gate, tenancy, authz, feature-flag cache), so every
// route registered here is gated identically to its siblings.
//
// The reviewer-facing surface over the OE-628 store: create a campaign
// (SCIM-sourced JSON or manual-CSV multipart), list campaigns, read one
// campaign with its rollup + items, attest keep/revoke per item,
// download the revoke list as CSV (the operator enforcement handoff —
// no route revokes access), and complete a campaign (which emits the
// CC6.3 access_review.completion.v1 evidence inside the store's
// transaction). Routes appended per the parallel-batch convention
// (chi.Mux rejects two Mounts at "/"). The /{id}/... sub-resources use
// literal suffixes (items/{itemID}/attest, revoke-list.csv, complete)
// at distinct depths from the bare /{id}, so there is no shadowing and
// declaration order within the group is free.
func (s *Server) registerAccessReview(root *chi.Mux) {
	h := accessreviewsapi.New(accessreview.NewStore(s.dbPool), accessreviewsapi.NewReadStore(s.dbPool))
	// OE-670: create a campaign (JSON = SCIM-sourced, multipart = manual CSV).
	root.Post("/v1/access-reviews", h.CreateCampaign)
	// OE-670: campaign index (?status= filter).
	root.Get("/v1/access-reviews", h.ListCampaigns)
	// OE-670: one campaign + completion rollup + review items.
	root.Get("/v1/access-reviews/{id}", h.GetCampaign)
	// OE-670: keep/revoke attestation by the assigned reviewer.
	root.Post("/v1/access-reviews/{id}/items/{itemID}/attest", h.AttestItem)
	// OE-670: revoke-decision CSV export (operator enforcement handoff).
	root.Get("/v1/access-reviews/{id}/revoke-list.csv", h.RevokeListCSV)
	// OE-670: complete a campaign — emits the CC6.3 completion evidence.
	root.Post("/v1/access-reviews/{id}/complete", h.CompleteCampaign)
}
