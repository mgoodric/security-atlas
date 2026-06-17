package api

import (
	"github.com/go-chi/chi/v5"

	controldetailapi "github.com/mgoodric/security-atlas/internal/api/controldetail"
	controlstateapi "github.com/mgoodric/security-atlas/internal/api/controlstate"
	evidencesummaryapi "github.com/mgoodric/security-atlas/internal/api/evidencesummary"
	gapexplainapi "github.com/mgoodric/security-atlas/internal/api/gapexplain"
	oscalprovenanceapi "github.com/mgoodric/security-atlas/internal/api/oscalprovenance"
	"github.com/mgoodric/security-atlas/internal/eval"
	"github.com/mgoodric/security-atlas/internal/evidencesummary"
	"github.com/mgoodric/security-atlas/internal/gapexplain"
	"github.com/mgoodric/security-atlas/internal/scope"
)

// registerControlState registers its domain's routes onto the shared root
// router. It is one of the slice-436 per-domain registrars extracted from
// the former monolithic httpHandler(); the root it receives already carries
// the full shared middleware chain (security headers, request ID, CORS,
// JWT, credential gate, tenancy, authz, feature-flag cache), so every route
// registered here is gated identically to how it was inline (slice 436
// AC-6). Behavior is unchanged: routes, handlers, and declaration order are
// preserved verbatim.
func (s *Server) registerControlState(root *chi.Mux) {
	// Slice 012: control state evaluation engine. Two read-only endpoints
	// over the control_evaluations ledger. Routes appended per the
	// parallel-batch convention (chi rejects two Mounts at "/"). Both are
	// literal-segment sub-resources under /v1/controls/{id}/ alongside slice
	// 017's /applicability and slice 018's /effective-scope -- chi resolves
	// declaration order within the same method, so no shadowing. The engine
	// is a pure read+append surface (it never writes evidence_records --
	// constitutional invariant #2), so it needs only the DB pool; the NATS
	// consumer + scheduler that drive evaluation are wired in cmd/atlas.
	controlStateEngine := eval.NewEngine(eval.NewStore(s.dbPool), scope.NewStore(s.dbPool))
	controlStateH := controlstateapi.New(controlStateEngine)
	root.Get("/v1/controls/{id}/state", controlStateH.State)
	root.Get("/v1/controls/{id}/effectiveness", controlStateH.Effectiveness)
	// Slice 064: control-detail backend read endpoints. Four pure reads that
	// fill the four binding placeholders slice 041's control-detail view
	// shipped (evidence stream, linked policies, linked risks, control
	// history). Routes appended per the parallel-batch convention (chi
	// rejects two Mounts at "/"). The three /v1/controls/{id}/ sub-resources
	// sit alongside slice 012's /state + /effectiveness -- chi resolves
	// declaration order within the same method, so no shadowing. The Store
	// is a pure read surface over existing tables (evidence_records,
	// policies, risk_control_links, control_evaluations) -- this slice adds
	// no migration and no write path (constitutional invariant #2).
	controlDetailH := controldetailapi.New(controldetailapi.NewStore(s.dbPool))
	root.Get("/v1/evidence", controlDetailH.Evidence)
	root.Get("/v1/controls/{id}/policies", controlDetailH.Policies)
	root.Get("/v1/controls/{id}/risks", controlDetailH.Risks)
	root.Get("/v1/controls/{id}/history", controlDetailH.History)
	// Slice 444: AI gap-explanation v0 — the first AI-assist surface, the
	// lowest-risk one. GET /v1/controls/{id}/gap-explanation returns the
	// DETERMINISTIC freshness/evidence rollup ALWAYS, plus a plain-language,
	// cited, NON-BINDING local-Ollama explanation of that rollup when
	// available AND every citation resolves to a tenant-owned row (AC-4). The
	// explanation is a comprehension aid in the control-detail view — never an
	// audit artifact, never persisted (P0-444-4), no approve/publish/export
	// path (P0-444-3). Route appended per the parallel-batch convention (chi
	// rejects two Mounts at "/"); the /v1/controls/{id}/ sub-resource sits
	// alongside slice 064's /policies,/risks,/history — chi resolves
	// declaration order within the same method, so no shadowing. The inference
	// client is local Ollama (slice 498), built from the all-defaults local
	// config; when Ollama is unreachable the surface degrades gracefully to
	// the deterministic rollup (AC-7). The gapexplain.Store is a pure read
	// surface over evidence_freshness + controls + evidence_records (invariant
	// #2) and runs under app.current_tenant RLS (invariant #6).
	gapExplainStore := gapexplain.NewStore(s.dbPool)
	gapExplainSvc := gapexplain.NewService(
		gapExplainStore,
		// Slice 499: per-tenant inference router (local-Ollama default, cloud
		// opt-in resolved under app.current_tenant). Call site unchanged.
		s.inferenceClient(),
		gapExplainStore,
	)
	gapExplainH := gapexplainapi.New(gapExplainSvc)
	root.Get("/v1/controls/{id}/gap-explanation", gapExplainH.GapExplanation)
	// Slice 502: AI evidence-summarization v0 — the §10.2 sibling of slice-444
	// gap-explanation. GET /v1/controls/{id}/evidence-summary returns the
	// DETERMINISTIC bounded CURRENT LIVE evidence set ALWAYS (top-N most-recent
	// records — P0-502-8), plus a plain-language, cited, NON-BINDING summary of
	// that evidence when available AND every citation resolves to a tenant-owned
	// row (AC-4). The summary is a comprehension aid in the control-detail view —
	// never an audit artifact, never persisted (P0-502-4), no
	// approve/publish/export path (P0-502-3). Route appended per the
	// parallel-batch convention (chi rejects two Mounts at "/"); the
	// /v1/controls/{id}/ sub-resource sits alongside slice 064's
	// /policies,/risks,/history and slice 444's /gap-explanation — chi resolves
	// declaration order within the same method, so no shadowing. The inference
	// client is the slice-499 per-tenant router (local-Ollama default, cloud only
	// under the tenant's opt-in + banner — P0-502-6); when generation is
	// unreachable the surface degrades gracefully to the deterministic evidence
	// list (AC-7, P0-502-7). The evidencesummary.Store is a pure read surface
	// over controls + evidence_records (invariant #2) and runs under
	// app.current_tenant RLS (invariant #6); it reads current live evidence only,
	// never a frozen audit-period population (P0-502-5, invariant #10).
	evidenceSummaryStore := evidencesummary.NewStore(s.dbPool)
	evidenceSummarySvc := evidencesummary.NewService(
		evidenceSummaryStore,
		s.inferenceClient(),
		evidenceSummaryStore,
	)
	evidenceSummaryH := evidencesummaryapi.New(evidenceSummarySvc)
	root.Get("/v1/controls/{id}/evidence-summary", evidenceSummaryH.EvidenceSummary)
	// Slice 750: portfolio / multi-control AI evidence-summary. GENERALIZES the
	// slice-502 single-control surface to a FILTERED control SET (by control-family
	// or by framework version; no filter = the whole program).
	// GET /v1/evidence-summary/portfolio returns the DETERMINISTIC TWO-LEVEL bounded
	// cross-control rollup ALWAYS (cap controls-per-summary AND records-per-control
	// — P0-750-2), plus a plain-language, cited, NON-BINDING summary of that rollup
	// when available AND every citation resolves to a tenant-owned row in the
	// cross-control grounding set (AC-2) AND every numeric claim matches the
	// deterministic rollup (AC-3, the slice-501 pattern — a fabricated count
	// auto-suppresses). The summary is a comprehension aid on the DASHBOARD — never
	// an audit artifact, never persisted (P0-502-4), no approve/publish/export path
	// (P0-502-3). Route appended per the parallel-batch convention (chi rejects two
	// Mounts at "/"). The PortfolioStore is a pure read surface over controls +
	// evidence_records + scf_anchors (invariant #2) and runs under
	// app.current_tenant RLS (invariant #6); it reads current live evidence only,
	// never a frozen audit-period population (P0-502-5, invariant #10). The inference
	// client is the slice-499 per-tenant router (local-Ollama default, cloud only
	// under the tenant's opt-in + banner — P0-502-6). The PortfolioStore's embedded
	// single-control Store is the citation resolver — the cross-control grounding gate
	// scopes citations to the summarized control set.
	portfolioSummaryStore := evidencesummary.NewPortfolioStore(s.dbPool)
	portfolioSummarySvc := evidencesummary.NewPortfolioService(
		portfolioSummaryStore,
		s.inferenceClient(),
		portfolioSummaryStore.Resolver(),
	)
	portfolioSummaryH := evidencesummaryapi.NewPortfolio(portfolioSummarySvc)
	root.Get("/v1/evidence-summary/portfolio", portfolioSummaryH.PortfolioEvidenceSummary)
	// Slice 599: OSCAL resolved-chain provenance read. A single pure read
	// over the slice-578 provenance persisted into the append-only
	// imported_catalog_audit_log.detail JSON of the `profile_imported`
	// success row. It surfaces, for one imported profile baseline, the
	// ordered chain of resolved documents + their roles + sha256 hashes (the
	// "diligence the diligence tool" provenance story for chained imports).
	// Routes appended per the parallel-batch convention (chi rejects two
	// Mounts at "/"). The path is a fresh top-level segment -- no shadowing.
	// The Store is a pure read surface (it never writes the audit-log ledger
	// -- constitutional invariant #2) and never touches the oscal-bridge: the
	// provenance is already in Postgres, so the read is bridge-free.
	oscalProvenanceH := oscalprovenanceapi.New(oscalprovenanceapi.NewStore(s.dbPool))
	root.Get("/v1/oscal/imported-profiles/{id}/provenance", oscalProvenanceH.Provenance)
}
