package api

import (
	"github.com/go-chi/chi/v5"

	checklistapi "github.com/mgoodric/security-atlas/internal/api/checklist"
	questionnairesapi "github.com/mgoodric/security-atlas/internal/api/questionnaires"
	"github.com/mgoodric/security-atlas/internal/checklist"
	"github.com/mgoodric/security-atlas/internal/llm"
	"github.com/mgoodric/security-atlas/internal/qaisuggest"
	"github.com/mgoodric/security-atlas/internal/questionnaire"
)

// registerQuestionnaire registers its domain's routes onto the shared root
// router. It is one of the slice-436 per-domain registrars extracted from
// the former monolithic httpHandler(); the root it receives already carries
// the full shared middleware chain (security headers, request ID, CORS,
// JWT, credential gate, tenancy, authz, feature-flag cache), so every route
// registered here is gated identically to how it was inline (slice 436
// AC-6). Behavior is unchanged: routes, handlers, and declaration order are
// preserved verbatim.
func (s *Server) registerQuestionnaire(root *chi.Mux) {
	// Slice 155: questionnaire tracer-bullet — Excel import + manual
	// authoring + AnswerLibrary skeleton (SCF-anchor keyed) + PDF export.
	// Routes appended per the parallel-batch convention (chi rejects two
	// Mounts at "/"). Tenant scoping enforced by RLS via the Store; the
	// PDF render reuses the chromedp pattern established by
	// internal/board/pdf.go (slice 022/027/137 precedent — zero new
	// go.mod dependency for PDF). NO AI-assist at v1 — the AnswerLibrary
	// suggestion path is a deterministic SCF-anchor lookup, not inference.
	questionnaireStore := questionnaire.NewStore(s.dbPool)
	// Slice 441: AI-answer suggestion v0 (cited drafts, one-click approve) —
	// the FIRST AI-write surface. The qaisuggest.Store does keyword-first-pass
	// retrieval + citation resolution + draft persistence under RLS (invariant
	// #6); the inference rides local Ollama (slice 498, P0-441-6 local-only).
	// Constitutional invariants (no fabricated coverage, no cross-tenant bleed,
	// one-click human approval) are enforced by the service + the DB CHECK on
	// questionnaire_answers (the slice-498 shared ai_assist guard, adopted by
	// this slice's migration).
	qaiStore := qaisuggest.NewStore(s.dbPool)
	qaiSvc := qaisuggest.NewService(
		qaiStore,
		llm.NewOllamaClient(llm.ConfigFromEnv()),
		qaiStore,
		qaiStore,
	)
	questionnairesH := questionnairesapi.NewWithSuggest(questionnaireStore, qaiSvc)
	questionnairesH.RegisterRoutes(root)
	// Slice 471: role-scoped control-implementation checklist generator v0
	// (cited, non-binding). The which-control -> which-role split is
	// DETERMINISTIC (owner_role + applicability_expr normalization, never
	// LLM-guessed); the local-Ollama task-breakdown turns each in-scope
	// control's text into 1..N cited task statements. Every item is cited to a
	// real tenant-owned control/scf-anchor/policy id (validated before the
	// operator sees it); the checklist is a DRAFT approved one section (role) at
	// a time. Constitutional invariants (no fabricated coverage, no cross-tenant
	// bleed, one-click approval, local-only) enforced by the service + the DB
	// CHECK on checklist_sections (the slice-498 shared ai_assist guard). Routes
	// append per the parallel-batch convention.
	checklistStore := checklist.NewStore(s.dbPool)
	checklistSvc := checklist.NewService(
		checklistStore,
		llm.NewOllamaClient(llm.ConfigFromEnv()),
		checklistStore,
		checklistStore,
		llm.NewAuditWriter(s.dbPool),
	)
	checklistH := checklistapi.New(checklistSvc, checklistStore)
	checklistH.RegisterRoutes(root)
}
