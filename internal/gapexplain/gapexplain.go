// Package gapexplain is the slice-444 AI gap-explanation v0 surface — the
// LOWEST-risk AI-assist surface in security-atlas. For ONE control's gap
// state it feeds the DETERMINISTIC gap rollup (the same freshness facts the
// control-detail page computes) to local Ollama and renders a plain-language
// explanation, with cited evidence/control IDs, in the control-detail view.
//
// The output is NON-BINDING: it is an informational comprehension aid in the
// operator's own control-detail view. It never goes to an auditor, a board,
// or a customer. Because it is non-binding there is NO approval gate — but
// the AI-assist invariants that are BINDINGNESS-INDEPENDENT still hold and
// are enforced here:
//
//   - No fabricated coverage. Every evidence/control ID the model cites is
//     validated to resolve to a real tenant-owned row BEFORE the operator
//     sees the draft. An explanation referencing an unresolvable ID is
//     SUPPRESSED — the caller falls back to the deterministic rollup display
//     (AC-4, P0-444-1, threat-model T).
//   - No cross-tenant bleed. The rollup + the cited excerpts + the citation
//     resolution all run under the requesting tenant's RLS context
//     (app.current_tenant). A Tenant-B explanation can never cite a Tenant-A
//     record because the resolution query never sees the other tenant's rows
//     (AC-10, P0-444-2, threat-model I).
//   - Local-only inference. The generation rides internal/llm (slice 498),
//     whose only v0 backend is local Ollama; no data leaves the deployment
//     (P0-444-5).
//
// SCOPE DISCIPLINE (anti-criteria, block merge):
//   - The explanation is NOT persisted as an audit artifact — it is
//     regenerated on demand (P0-444-4). This surface deliberately does NOT
//     write an ai_generations row: the row is a snapshot-at-generation
//     forensic record meant for approvable/binding surfaces, and a
//     non-binding comprehension aid that regenerates on every view would only
//     grow that ledger without forensic value. Model provenance is surfaced
//     to the operator in the RESPONSE (Explanation.Model*), satisfying the
//     transparency requirement without persistence.
//   - NO approve/publish/export path (P0-444-3, AC-5). The Service produces a
//     read-only Explanation value; there is no state to approve.
//   - The deterministic rollup is NEVER blocked on the LLM (P0-444-7, AC-7):
//     Explain always returns the rollup; the explanation text is suppressed
//     (Suppressed=true) on any generation or citation failure.
//   - Local Ollama only — no cloud routing (P0-444-5). Inherited from
//     internal/llm's v0 backend.
//   - Does NOT ship evidence-summarization (P0-444-6) — sibling follow-on.
package gapexplain

import (
	"errors"
	"time"

	"github.com/google/uuid"
)

// promptVersion is this surface's prompt-template version tag, recorded on the
// llm.GenerateRequest for forensic reconstruction (slice-182 schema contract).
// Bump it whenever the system prompt or context shape changes materially.
const promptVersion = "gapexplain-v0"

// MaxExplanationTokens bounds the generation. A gap explanation is a few
// sentences — well under internal/llm's MaxTokenBudget (4096). Kept small so
// the model stays terse and the latency stays low (D-mitigation).
const MaxExplanationTokens = 512

// GenerationTimeout caps the wall-clock per explanation. Local Ollama on
// commodity hardware answers a short prompt in a few seconds; this is the
// graceful-degradation deadline beyond which the caller falls back to the
// deterministic rollup (AC-7, D-mitigation).
const GenerationTimeout = 30 * time.Second

// maxCitedExcerpts bounds how many evidence records are fed to the model as
// cited excerpts (AC-2 "bounded cited excerpts"). The rollup is one control's
// gap facts; a handful of the freshest records is enough grounding without
// letting the prompt — or the set of citable IDs — grow unbounded.
const maxCitedExcerpts = 8

// Sentinel errors. Callers match these with errors.Is. Note that NONE of
// these are returned to the HTTP caller as a failure: per AC-7 the handler
// always renders the deterministic rollup, and a suppressed/failed
// explanation is conveyed via Explanation.Suppressed, not an error. These
// exist for internal logging + test assertions.
var (
	// ErrNoRollup is returned when the control has no freshness rollup at all
	// (the freshness read-model has never been refreshed for it). The caller
	// renders an empty rollup; there is nothing to explain.
	ErrNoRollup = errors.New("gapexplain: no freshness rollup for control")
)

// EvidenceFact is one cited evidence record distilled to the bounded fields
// the prompt needs and the citation validator can resolve. It is NOT the full
// evidence row — only the id (for citation resolution) plus the few
// deterministic facts the model is allowed to explain.
type EvidenceFact struct {
	EvidenceID   uuid.UUID
	EvidenceKind string
	Result       string
	ObservedAt   time.Time
}

// Rollup is the DETERMINISTIC per-control gap rollup — the same freshness
// facts the control-detail page computes, assembled server-side under the
// requesting tenant's RLS (AC-1). Every number the model is permitted to
// state comes from here, NOT from the model (threat-model T). The Rollup is
// always returned to the caller, with or without an explanation (AC-7).
type Rollup struct {
	ControlID      uuid.UUID
	ControlTitle   string
	FreshnessClass string

	// LatestObservedAt / ValidUntil are nil when the control has no evidence.
	LatestObservedAt *time.Time
	ValidUntil       *time.Time

	// IsStale is the deterministic gap signal: the control's freshest evidence
	// has decayed past its freshness-class horizon (or it has no evidence).
	IsStale bool

	// EvidenceCount is the number of evidence records in the freshness window.
	EvidenceCount int

	// Evidence is the bounded set of cited excerpts (newest-first, capped at
	// maxCitedExcerpts) the prompt grounds the explanation in. The set of
	// citable evidence IDs the model may reference is exactly these IDs plus
	// the ControlID.
	Evidence []EvidenceFact
}

// Explanation is the result of Service.Explain. The Rollup is ALWAYS present
// (AC-7). The plain-language Text + Citations are present only when generation
// succeeded AND every citation resolved (Suppressed=false). On any failure
// Suppressed=true, Reason carries a short machine-readable cause, and the
// caller renders the Rollup alone.
//
// There is NO approval/publish/export field — the value is read-only by
// construction (AC-5, P0-444-3).
type Explanation struct {
	Rollup Rollup

	// Text is the model's plain-language explanation. Empty when Suppressed.
	Text string

	// Citations are the resolved-and-validated IDs the Text refers to. Every
	// entry is proven tenant-owned (AC-4). Empty when Suppressed.
	Citations []Citation

	// Suppressed is true when the explanation was withheld and the caller must
	// fall back to the deterministic rollup. Reasons: generation unavailable
	// (backend down / timeout) or a citation failed to resolve.
	Suppressed bool

	// Reason is a short, non-sensitive cause when Suppressed. One of:
	// "generation_unavailable", "unresolved_citation", "no_citations". It
	// NEVER carries model text or backend error detail (slice-367 leak
	// discipline) — it is a fixed vocabulary safe to surface in the UI.
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
)

// Citation is one resolved, tenant-owned reference the explanation makes. Kind
// is "control" or "evidence"; ID is the canonical UUID string.
type Citation struct {
	Kind string `json:"kind"`
	ID   string `json:"id"`
}

// Citation kinds.
const (
	KindControl  = "control"
	KindEvidence = "evidence"
)
