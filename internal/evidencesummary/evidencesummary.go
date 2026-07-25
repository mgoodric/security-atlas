// Package evidencesummary is the slice-502 AI evidence-summarization v0 surface
// — the §10.2 sibling of slice-444 gap-explanation. For ONE control's CURRENT
// LIVE evidence set it feeds a bounded set of cited evidence excerpts to the
// per-tenant inference client (slice 499; local Ollama by default) and renders
// a plain-language summary — "what this evidence collectively shows" — with
// cited evidence IDs, in the control-detail view, ALONGSIDE (never replacing)
// the deterministic evidence list.
//
// The output is NON-BINDING: it is an informational comprehension aid in the
// operator's own control-detail view. It never goes to an auditor, a board, or
// a customer. Because it is non-binding there is NO approval gate — but the
// AI-assist invariants that are BINDINGNESS-INDEPENDENT still hold and are
// enforced here, exactly as in slice 444:
//
//   - No fabricated coverage. Every evidence/control ID the model cites is
//     validated to resolve to a real tenant-owned row BEFORE the operator sees
//     the draft. A summary referencing an unresolvable ID is SUPPRESSED — the
//     caller falls back to the deterministic evidence list (AC-4, P0-502-1,
//     threat-model T). A false summary that asserts unsupported coverage
//     misleads the operator just as badly as a binding one, so the
//     no-fabricated-coverage invariant applies in full.
//   - No cross-tenant bleed. The evidence retrieval + the cited excerpts + the
//     citation resolution all run under the requesting tenant's RLS context
//     (app.current_tenant). A Tenant-B summary can never cite a Tenant-A record
//     because the resolution query never sees the other tenant's rows (AC-10,
//     P0-502-2, threat-model I).
//   - Local-only inference by default. The generation rides the slice-499
//     per-tenant inference client, whose off-by-default backend is local Ollama;
//     cloud egress happens only under the tenant's explicit opt-in + banner
//     (P0-502-6).
//
// SCOPE DISCIPLINE (anti-criteria, block merge):
//   - The summary is NOT persisted as an audit artifact — it is regenerated on
//     demand (P0-502-4). This surface deliberately does NOT write an
//     ai_generations row: that row is a snapshot-at-generation forensic record
//     for approvable/binding surfaces, and a non-binding comprehension aid that
//     regenerates on every view would only grow that ledger without forensic
//     value. Model provenance is surfaced to the operator in the RESPONSE
//     (Summary.Model*), satisfying transparency without persistence (AC-6).
//   - NO approve/publish/export path (P0-502-3, AC-5). The Service produces a
//     read-only Summary value; there is no state to approve.
//   - The deterministic evidence list is NEVER blocked on the LLM (P0-502-7,
//     AC-7): Summarize always returns the bounded evidence list; the summary
//     text is suppressed (Suppressed=true) on any generation or citation
//     failure.
//   - CURRENT LIVE evidence only — no audit-period-frozen mixing (P0-502-5,
//     invariant #10). The bounded retrieval reads the live evidence_records
//     ordered by observed_at DESC; it does not draw from a frozen
//     audit-period sample population. The UI labels the summary as
//     live-evidence-only. A period-scoped summary that respects audit-period
//     freezing is a NAMED follow-on, not built here.
//   - BOUNDED corpus — top-N excerpts, never the full history (P0-502-8, AC-1).
package evidencesummary

import (
	"errors"
	"time"

	"github.com/google/uuid"
)

// ErrNoControl is returned by the EvidenceReader when the control does not
// exist or is not visible to the requesting tenant (RLS). The handler maps it
// to a 404; there is no evidence set to summarize for a control the tenant
// cannot see.
var ErrNoControl = errors.New("evidencesummary: control not found")

// promptVersion is this surface's prompt-template version tag, recorded on the
// llm.GenerateRequest for forensic reconstruction (slice-182 schema contract).
// Bump it whenever the system prompt or context shape changes materially.
const promptVersion = "evidencesummary-v0"

// MaxSummaryTokens bounds the generation. An evidence summary is a short
// paragraph — well under internal/llm's MaxTokenBudget (4096). Kept small so
// the model stays terse and the latency stays low (D-mitigation).
const MaxSummaryTokens = 512

// GenerationTimeout caps the wall-clock per summary. Local Ollama on commodity
// hardware answers a short prompt in a few seconds; this is the
// graceful-degradation deadline beyond which the caller falls back to the
// deterministic evidence list (AC-7, D-mitigation).
const GenerationTimeout = 30 * time.Second

// MaxCitedExcerpts bounds how many CURRENT LIVE evidence records are fed to the
// model as cited excerpts (AC-1 "bounded top-N", P0-502-8). The retrieval is
// ordered by observed_at DESC, so this is the N most-recent live records — a
// recency bound (the JUDGMENT call; see decisions-log D3). A handful of the
// freshest records is enough grounding without letting the prompt — or the set
// of citable IDs — grow unbounded over a control's full history.
const MaxCitedExcerpts = 8

// EvidenceFact is one CURRENT LIVE evidence record distilled to the bounded
// fields the prompt needs and the citation validator can resolve. It is NOT the
// full evidence row — only the id (for citation resolution) plus the few
// deterministic facts the model is allowed to summarize.
type EvidenceFact struct {
	EvidenceID   uuid.UUID
	EvidenceKind string
	Result       string
	ObservedAt   time.Time
}

// EvidenceSet is the DETERMINISTIC bounded evidence corpus for one control,
// assembled server-side under the requesting tenant's RLS (AC-1). It is the
// SAME control-detail evidence data path the page already renders, capped at
// MaxCitedExcerpts most-recent records. Every fact the model is permitted to
// state comes from here, NOT from the model (threat-model T). The EvidenceSet
// is ALWAYS returned to the caller, with or without a summary (AC-7).
type EvidenceSet struct {
	ControlID    uuid.UUID
	ControlTitle string

	// Records is the bounded set of CURRENT LIVE cited excerpts (newest-first,
	// capped at MaxCitedExcerpts). The set of citable evidence IDs the model may
	// reference is exactly these IDs plus the ControlID.
	Records []EvidenceFact

	// TotalCount is the number of live evidence records the control has on
	// record (for the "showing N of M" UI label). It may exceed len(Records)
	// when the history is longer than the bound — the summary is over the
	// bounded set, never the full history (P0-502-8).
	TotalCount int
}

// Summary is the result of Service.Summarize. The EvidenceSet is ALWAYS present
// (AC-7). The plain-language Text + Citations are present only when generation
// succeeded AND every citation resolved (Suppressed=false). On any failure
// Suppressed=true, Reason carries a short machine-readable cause, and the
// caller renders the EvidenceSet alone.
//
// There is NO approval/publish/export field — the value is read-only by
// construction (AC-5, P0-502-3).
type Summary struct {
	EvidenceSet EvidenceSet

	// Text is the model's plain-language summary. Empty when Suppressed.
	Text string

	// Citations are the resolved-and-validated IDs the Text refers to. Every
	// entry is proven tenant-owned (AC-4). Empty when Suppressed.
	Citations []Citation

	// Suppressed is true when the summary was withheld and the caller must fall
	// back to the deterministic evidence list. Reasons: generation unavailable
	// (backend down / timeout), no evidence to summarize, or a citation failed
	// to resolve.
	Suppressed bool

	// Reason is a short, non-sensitive cause when Suppressed. One of the fixed
	// vocabulary below. It NEVER carries model text or backend error detail
	// (slice-367 leak discipline) — it is safe to surface in the UI.
	Reason string

	// Model provenance, surfaced for transparency (AC-6 disclosure). These are
	// the values the inference backend actually served. Present whenever a
	// generation ran (even if later suppressed), empty when no generation was
	// attempted.
	ModelName     string
	ModelVersion  string
	ModelProvider string
}

// Suppression reasons (fixed vocabulary — safe to render in the UI).
const (
	ReasonGenerationUnavailable = "generation_unavailable"
	ReasonUnresolvedCitation    = "unresolved_citation"
	ReasonNoCitations           = "no_citations"
	ReasonNoEvidence            = "no_evidence"
)

// Citation is one resolved, tenant-owned reference the summary makes. Kind is
// "control" or "evidence"; ID is the canonical UUID string.
type Citation struct {
	Kind string `json:"kind"`
	ID   string `json:"id"`
}

// Citation kinds.
const (
	KindControl  = "control"
	KindEvidence = "evidence"
)
