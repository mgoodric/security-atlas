// Package boardnarrative is the slice-440 board-narrative AI v0 surface — the
// HIGHEST-RISK AI-assist surface in security-atlas. For ONE numbered section of
// the board narrative (the control-coverage-summary section — the most
// rollup-grounded, every claim numeric and citable) it:
//
//   - computes a DETERMINISTIC pre-computation rollup from the existing
//     board.Brief (the frozen monthly-brief data path — internal/board), under
//     the requesting tenant's RLS context;
//   - assembles a HYBRID prompt (the rollup PLUS bounded, tenant-owned cited
//     evidence/control excerpts — never raw evidence records, never pure
//     rollup — guardrail 1);
//   - drafts the section against local Ollama (Llama 3.1 8B default — CLAUDE.md;
//     internal/llm, StubClient in CI);
//   - validates the draft through FOUR pre-operator gates — citation resolution,
//     numeric-claim verification, section-shape conformance, and banned-phrase
//     tone enforcement — BEFORE the operator ever sees it (guardrails 4/5/6 +
//     tone);
//   - persists a valid draft as ai_assisted=TRUE, human_approved=FALSE
//     (guardrail 3 audit + per-section approval state, guardrail 2);
//   - approves a draft on a SEPARATE one-click operator action recording the
//     human_approver (the constitutional boundary).
//
// Board narratives are consumed by non-technical board members who take the
// output at face value, so the hallucination cost is asymmetric. The seven
// guardrails are NOT optional — they are the constitutional boundary
// (CLAUDE.md §"Board-narrative AI-assist (load-bearing — OQ #14 resolved)"):
//
//	G1 hybrid input        — buildPrompt: rollup + bounded cited excerpts.
//	G2 per-section approval — the record carries a per-section approval state;
//	                          Approve flips ONE section.
//	G3 full audit           — the llm.AuditWriter ai_generations row (system
//	                          prompt + context inputs + raw draft) + the
//	                          per-section record's operator-edit/final columns.
//	G4 mandatory citations  — validateCitations: every cited id is in the
//	                          grounding set AND resolves to a tenant-owned row;
//	                          one unresolved id rejects the WHOLE draft.
//	G5 numeric verification — verifyNumbers: every number in the draft is
//	                          matched against a rollup field; a mismatch
//	                          AUTO-REJECTS the draft. THE defining guardrail.
//	G6 section-shape        — enforceShape: the draft must conform to the
//	                          numbered template; freestyle is rejected.
//	G7 editor-mode UX        — the frontend (cannot approve with an unresolved
//	                          citation); the per-section approval action here.
//
// PLUS banned-phrase tone enforcement (slice 182's list wired into the system
// prompt AND a post-generation rejection — guardrail-adjacent, P0-440-6).
//
// SCOPE DISCIPLINE (anti-criteria, block merge):
//   - ONE section only (control-coverage-summary). Remaining sections are
//     follow-ons (P0-440-7).
//   - LOCAL OLLAMA ONLY. No cloud routing in v0 (P0-440-5).
//   - The draft never reaches the operator if it failed citation / numeric /
//     shape / tone validation (P0-440-4).
//   - Bounded cited excerpts only — never the raw evidence corpus (P0-440-8).
//   - No audit-binding artifact published without one-click human approval; no
//     auto-approve (P0-440-1); human_approved=TRUE requires human_approver
//     (P0-440-2).
package boardnarrative

import (
	"errors"
	"time"
)

// SectionKey identifies the ONE numbered section this v0 implements. Recorded
// on the persisted record so the follow-on sections (each its own key) coexist
// in the same table without ambiguity.
type SectionKey string

const (
	// SectionControlCoverage is the control-coverage-summary section — the
	// most rollup-grounded section, chosen for v0 because every claim is a
	// number drawn from the deterministic Brief and every supporting reference
	// is a citable tenant-owned control/evidence id (decisions log D1).
	SectionControlCoverage SectionKey = "control_coverage_summary"
)

// promptVersion is this surface's prompt-template version tag, recorded on the
// llm.GenerateRequest + the persisted record + the ai_generations audit row for
// forensic reconstruction (slice-182 schema contract). Bump on any material
// change to the system prompt, the rollup shape, or the section template.
const promptVersion = "boardnarrative-coverage-v0"

// MaxSectionTokens bounds the generation. One coverage-summary section is a
// few short numbered paragraphs — well under internal/llm's MaxTokenBudget
// (4096). Kept small so the model stays terse and latency stays bounded
// (D-mitigation).
const MaxSectionTokens = 1024

// GenerationTimeout caps wall-clock per section. Local Ollama on commodity
// hardware drafts a short section in a few seconds; beyond this deadline the
// generation degrades to "unavailable" rather than hanging the operator's
// review (D-mitigation).
const GenerationTimeout = 60 * time.Second

// maxCitedExcerpts bounds how many tenant-owned control/evidence excerpts the
// hybrid prompt feeds the model as citable material (guardrail 1's "bounded",
// P0-440-8). A handful of the controls behind the coverage numbers is enough
// grounding without letting the prompt — or the set of citable ids — grow
// unbounded (threat-model D).
const maxCitedExcerpts = 12

// CitationKind classifies a cited, tenant-owned reference a section makes. The
// coverage section cites the controls and evidence records behind its numbers.
type CitationKind string

const (
	KindControl  CitationKind = "control"
	KindEvidence CitationKind = "evidence"
)

// Citation is one resolved, tenant-owned reference the section makes (the
// operator sees these next to the draft so they can verify each claim).
type Citation struct {
	Kind CitationKind `json:"kind"`
	ID   string       `json:"id"`
}

// SectionResult is the outcome of Service.Generate. Exactly one shape:
//
//   - Drafted: Suppressed=false. RecordID names the persisted DRAFT
//     (ai_assisted, unapproved); Draft carries the validated section text +
//     its resolved Citations; the section passed ALL four pre-operator gates.
//   - Suppressed: Suppressed=true with a Reason. The draft failed a gate
//     (unresolved citation / numeric mismatch / bad shape / banned phrase) or
//     the backend was unavailable; NOTHING is persisted (a draft the operator
//     must not see is never written — P0-440-4).
//
// A persisted draft is ALWAYS unapproved. Approval is Service.Approve.
type SectionResult struct {
	// RecordID is the persisted draft record's id. Empty when suppressed.
	RecordID string `json:"record_id,omitempty"`

	// Section echoes the section key this result is for.
	Section SectionKey `json:"section"`

	// Draft is the validated section text the operator will edit/approve.
	// Empty when suppressed.
	Draft string `json:"draft,omitempty"`

	// Citations are the resolved, tenant-owned references the section makes —
	// every entry proven in-grounding AND tenant-owned (guardrail 4). Empty
	// when suppressed.
	Citations []Citation `json:"citations,omitempty"`

	// Suppressed is true when the draft was withheld at a guardrail. Reason
	// carries the fixed cause (safe to render; never model text or backend
	// detail — slice-367 leak discipline).
	Suppressed bool   `json:"suppressed"`
	Reason     string `json:"reason,omitempty"`

	// Model provenance — the values the inference backend actually served
	// (snapshot-at-generation). Present whenever a generation ran.
	ModelName     string `json:"model_name,omitempty"`
	ModelVersion  string `json:"model_version,omitempty"`
	ModelProvider string `json:"model_provider,omitempty"`

	// CloudRouted is true when the generation routed to a cloud LLM (the
	// frontend renders a visible banner — CLAUDE.md inference-backend rule).
	// Always false in v0 (local Ollama only, P0-440-5); the field exists so the
	// cloud opt-in follow-on surfaces it without a wire-shape change.
	CloudRouted bool `json:"cloud_routed"`
}

// ApprovedSection is the result of Service.Approve: the now-approved section's
// boundary state, proving human_approved=TRUE with a recorded human_approver.
// The approved text is what ships into the board pack.
type ApprovedSection struct {
	RecordID      string     `json:"record_id"`
	Section       SectionKey `json:"section"`
	FinalText     string     `json:"final_text"`
	HumanApproved bool       `json:"human_approved"`
	HumanApprover string     `json:"human_approver"`
}

// Suppression reasons (fixed vocabulary — safe to render in the UI; never carry
// model text or backend error detail, slice-367 leak discipline).
const (
	ReasonGenerationUnavailable = "generation_unavailable"
	ReasonUnresolvedCitation    = "unresolved_citation"
	ReasonNoCitations           = "no_citations"
	ReasonNumericMismatch       = "numeric_mismatch"
	ReasonBadShape              = "section_shape_violation"
	ReasonBannedPhrase          = "banned_phrase"
)

// Sentinel errors. A suppressed draft is NOT an error — it is conveyed via the
// SectionResult fields. These exist for genuine infra failures + the approval
// path.
var (
	// ErrNoBriefData is returned when the rollup cannot be computed because the
	// tenant has no program posture data yet (no frameworks / freshness rows).
	// The operator generates a brief first; there is nothing to summarize.
	ErrNoBriefData = errors.New("boardnarrative: no program posture data to summarize")

	// ErrRecordNotFound is returned by Approve when the id names no tenant-owned
	// AI-assisted draft to approve (RLS-invisible or absent).
	ErrRecordNotFound = errors.New("boardnarrative: ai-suggested section not found")

	// ErrApproverRequired is returned by Approve when the approver id is blank —
	// the Go mirror of the DB CHECK (P0-440-2).
	ErrApproverRequired = errors.New("boardnarrative: approval requires a human_approver")
)
