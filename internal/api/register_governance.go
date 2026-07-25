package api

import (
	"github.com/go-chi/chi/v5"

	decisionsapi "github.com/mgoodric/security-atlas/internal/api/decisions"
	exceptionsapi "github.com/mgoodric/security-atlas/internal/api/exceptions"
	mcpwriteproposalsapi "github.com/mgoodric/security-atlas/internal/api/mcpwriteproposals"
	policiesapi "github.com/mgoodric/security-atlas/internal/api/policies"
	policyacksapi "github.com/mgoodric/security-atlas/internal/api/policyacks"
	"github.com/mgoodric/security-atlas/internal/decision"
	"github.com/mgoodric/security-atlas/internal/exception"
	"github.com/mgoodric/security-atlas/internal/mcp/writeproposals"
	"github.com/mgoodric/security-atlas/internal/policy"
)

// registerGovernance registers its domain's routes onto the shared root
// router. It is one of the slice-436 per-domain registrars extracted from
// the former monolithic httpHandler(); the root it receives already carries
// the full shared middleware chain (security headers, request ID, CORS,
// JWT, credential gate, tenancy, authz, feature-flag cache), so every route
// registered here is gated identically to how it was inline (slice 436
// AC-6). Behavior is unchanged: routes, handlers, and declaration order are
// preserved verbatim.
func (s *Server) registerGovernance(root *chi.Mux) {
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
	// Slice 173: MCP write tools + HITL approval flow. Routes appended per
	// the parallel-batch convention (chi rejects two Mounts at "/"). The
	// MCP write tools (running in the cmd/atlas-mcp binary) call POST
	// /v1/mcp/write-proposals to file a draft; operators confirm or reject
	// via the same surface. The Store ships with the four canonical
	// Appliers (create_risk, update_control_state, push_evidence,
	// update_risk_treatment) registered; on confirm, the Applier executes
	// inside the same transaction as the state flip so a downstream-write
	// failure rolls the proposal back to state=ai_proposed.
	mcpWriteStore := writeproposals.RegisterDefaultAppliers(writeproposals.NewStore(s.dbPool))
	mcpWriteH := mcpwriteproposalsapi.New(mcpWriteStore)
	root.Post("/v1/mcp/write-proposals", mcpWriteH.CreateProposal)
	root.Get("/v1/mcp/write-proposals", mcpWriteH.ListProposals)
	root.Get("/v1/mcp/write-proposals/{id}", mcpWriteH.GetProposal)
	root.Post("/v1/mcp/write-proposals/{id}/confirm", mcpWriteH.ConfirmProposal)
	root.Post("/v1/mcp/write-proposals/{id}/reject", mcpWriteH.RejectProposal)
	// Slice 055: Decision Log CRUD + linkage (canvas Â§6.7). Routes appended
	// per the parallel-batch convention -- chi.Mux rejects two Mounts at
	// "/", so individual routes register onto the root. The literal-segment
	// route /v1/decisions/overdue is declared BEFORE the bare
	// /v1/decisions/{id} so chi's declaration-order match keeps the calendar
	// route ahead of the generic UUID-id route. The link sub-resource
	// routes are declared after the bare /{id} routes but use distinct path
	// shapes (/{id}/links/{kind}[/{targetID}]) so there is no shadowing.
	decisionsH := decisionsapi.New(decision.NewStore(s.dbPool))
	root.Post("/v1/decisions", decisionsH.CreateDecision)
	root.Get("/v1/decisions", decisionsH.ListDecisions)
	root.Get("/v1/decisions/overdue", decisionsH.Overdue)
	root.Get("/v1/decisions/{id}", decisionsH.GetDecision)
	root.Get("/v1/decisions/{id}/audit-log", decisionsH.AuditLog)
	root.Patch("/v1/decisions/{id}", decisionsH.UpdateDecision)
	root.Post("/v1/decisions/{id}/supersede", decisionsH.Supersede)
	root.Post("/v1/decisions/{id}/links/{kind}", decisionsH.AddLink)
	root.Delete("/v1/decisions/{id}/links/{kind}/{targetID}", decisionsH.RemoveLink)
	// Slice 022: policy library. Routes appended per the parallel-batch
	// convention (chi rejects two Mounts at "/"). Sub-resource transitions
	// (submit/approve/publish) are declared before /{id} so chi's
	// declaration-order match keeps the literal-segment routes first
	// within the same method. Approve + Publish enforce IsApprover at the
	// handler (slice 034 credential flag).
	// Slice 107: NewWithPool wires the *pgxpool.Pool the
	// `?include=ack_rate` path uses (it opens a tenant-GUC-bearing tx
	// for the joined query). Backwards-compatible: callers without the
	// extension continue through the store as before.
	policiesH := policiesapi.NewWithPool(policy.NewStore(s.dbPool), s.dbPool)
	root.Post("/v1/policies", policiesH.CreatePolicy)
	root.Get("/v1/policies", policiesH.ListPolicies)
	root.Patch("/v1/policies/{id}/submit", policiesH.Submit)
	root.Patch("/v1/policies/{id}/approve", policiesH.Approve)
	root.Post("/v1/policies/{id}/publish", policiesH.Publish)
	root.Get("/v1/policies/{id}/pdf", policiesH.PDF)
	root.Get("/v1/policies/{id}", policiesH.GetPolicy)
	// Slice 023: policy acknowledgment workflow. Three routes appended
	// per the parallel-batch convention (chi rejects two Mounts at "/").
	// The literal-segment route /v1/policies/{id}/acknowledgment-rate is
	// declared before /v1/policies/{id} above would have shadowed it --
	// but slice 022 only added literal sub-resources (/submit, /approve,
	// /publish, /pdf) and the bare /{id}, so there is no shadowing risk
	// because chi resolves declaration order within the same method.
	// POST /v1/policies/{id}/acknowledge mounts only when the ingest
	// service is wired (the ack writes an evidence record); without it
	// the handler 503s. GET routes always mount because they only read.
	policyAcksH := policyacksapi.New(policy.NewAckStore(s.dbPool), s.ingestService)
	root.Get("/v1/me/acknowledgments", policyAcksH.MyAcknowledgments)
	root.Get("/v1/policies/{id}/acknowledgment-rate", policyAcksH.AcknowledgmentRate)
	if s.ingestService != nil {
		root.Post("/v1/policies/{id}/acknowledge", policyAcksH.Acknowledge)
	}
}
