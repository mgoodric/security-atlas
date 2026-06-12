// Package qaisuggest is the slice-441 AI questionnaire-answer suggestion v0
// surface — the FIRST AI-WRITE surface in security-atlas. For ONE unanswered
// questionnaire row it retrieves candidate evidence/policy material owned by
// the requesting tenant (a keyword first-pass — NO pgvector in v0), drafts a
// cited answer via local Ollama, validates every citation against a real
// tenant-owned row, and persists the result as a DRAFT the operator approves
// (or edits/rejects) one click at a time.
//
// This surface is governed by the CLAUDE.md AI-assist boundary (hard). Unlike
// the slice-444 gap-explanation surface (non-binding, never persisted), a
// questionnaire answer — once approved — is sent to an external customer. The
// boundary invariants are therefore enforced, not merely honored:
//
//   - No fabricated coverage (AC-4/AC-5, P0-441-2). Every evidence/policy ID
//     the model cites is validated to (a) be in the grounding set the prompt
//     put in front of it AND (b) resolve to a real tenant-owned row, BEFORE
//     the operator sees the draft. A single unresolvable citation suppresses
//     the whole draft. When no candidate material backs the question the
//     surface returns "insufficient evidence — answer manually" rather than a
//     fabricated answer (the no-fabricated-coverage guardrail in action).
//
//   - No cross-tenant bleed (AC-16, P0-441-3). Retrieval, prompt assembly,
//     citation resolution, and persistence all run under the requesting
//     tenant's RLS context (app.current_tenant). A Tenant-B suggestion can
//     never cite or quote a Tenant-A record because every query is invisible
//     to the other tenant's rows (invariant #6).
//
//   - One-click human approval (AC-6, P0-441-1). A suggestion persists as a
//     DRAFT (ai_assisted=TRUE, human_approved=FALSE, human_approver=NULL).
//     Approval is a SEPARATE operator action that records the approver. The DB
//     CHECK ai_assist_human_approver_guard (slice 498, adopted on
//     questionnaire_answers by this slice's migration) makes
//     human_approved=TRUE without a human_approver impossible (P0-441-8). NO
//     code path auto-approves.
//
//   - Local Ollama only (P0-441-6). The generation rides internal/llm, whose
//     v0 backend is local Ollama; no data leaves the deployment. A cloud-LLM
//     opt-in is a follow-on; v0 renders a banner only when routing is cloud
//     (it never is in v0).
//
// SCOPE DISCIPLINE (anti-criteria, block merge):
//   - ONE row, end-to-end. No batch "answer all rows" (P0-441-7).
//   - Keyword first-pass retrieval. NO pgvector (P0-441-5) — a follow-on.
//   - Local Ollama only (P0-441-6).
//   - The draft is never returned to a customer without approval (P0-441-1).
package qaisuggest

import (
	"errors"
	"time"
)

// promptVersion is this surface's prompt-template version tag, recorded on the
// llm.GenerateRequest + the persisted draft for forensic reconstruction
// (slice-182 schema contract). Bump it when the system prompt or context
// shape changes materially.
const promptVersion = "qaisuggest-v0"

// MaxAnswerTokens bounds the generation. A questionnaire answer is a short
// paragraph — well under internal/llm's MaxTokenBudget (4096). Kept small so
// the model stays terse and the latency stays bounded (D-mitigation).
const MaxAnswerTokens = 768

// GenerationTimeout caps the wall-clock per suggestion. Local Ollama on
// commodity hardware answers a short prompt in a few seconds; beyond this
// deadline the suggestion degrades to "generation unavailable" rather than
// hanging the operator's review session (D-mitigation).
const GenerationTimeout = 45 * time.Second

// maxCandidates bounds how many evidence/policy candidates the keyword
// first-pass feeds the model as cited excerpts (AC-1 "capped candidate set").
// A handful of the best keyword matches is enough grounding without letting
// the prompt — or the set of citable IDs — grow unbounded (D-mitigation).
const maxCandidates = 8

// CandidateKind classifies a retrieved candidate. The model may cite a
// candidate by its id; the kind drives the resolver's ownership check.
type CandidateKind string

const (
	KindPolicy   CandidateKind = "policy"
	KindEvidence CandidateKind = "evidence"
)

// Candidate is one tenant-owned piece of retrieved material the model is
// allowed to ground an answer in. Excerpt is a BOUNDED slice of the source
// text (AC-2 "bounded excerpts", not the full corpus). The model may cite
// only candidate IDs (the grounding gate); a cited ID outside the candidate
// set is a fabrication even if it happens to name another tenant-owned row.
type Candidate struct {
	ID      string        `json:"id"`
	Kind    CandidateKind `json:"kind"`
	Title   string        `json:"title"`
	Excerpt string        `json:"excerpt"`
}

// Citation is one resolved, tenant-owned reference the draft makes. Kind is
// "policy" or "evidence"; ID is the canonical UUID string.
type Citation struct {
	Kind CandidateKind `json:"kind"`
	ID   string        `json:"id"`
}

// Suggestion is the result of Service.Suggest. Exactly one of these shapes is
// returned:
//
//   - A drafted suggestion: Suppressed=false, InsufficientEvidence=false. The
//     AnswerID names the persisted DRAFT row (ai_assisted, unapproved); Draft
//     carries the model text + its resolved Citations.
//   - Insufficient evidence: InsufficientEvidence=true. No candidate material
//     backed the question; NOTHING is persisted and the operator answers
//     manually (AC-5, the no-fabricated-coverage path).
//   - Suppressed: Suppressed=true with a Reason. A draft was generated but
//     failed the citation gate (fabricated/cross-tenant/no-citations) or the
//     backend was unavailable; NOTHING is persisted (a draft the operator
//     must not see is never written — P0-441-4).
//
// A persisted draft is ALWAYS unapproved. Approval is Service.Approve.
type Suggestion struct {
	// AnswerID is the persisted draft answer's id. Empty when nothing was
	// persisted (insufficient evidence or suppressed).
	AnswerID string `json:"answer_id,omitempty"`

	// QuestionID echoes the question this suggestion is for.
	QuestionID string `json:"question_id"`

	// Draft is the model's drafted answer text. Empty when suppressed or
	// insufficient.
	Draft string `json:"draft,omitempty"`

	// Citations are the resolved, tenant-owned references the draft makes.
	// Every entry is proven tenant-owned + in-grounding (AC-4). Empty when
	// suppressed or insufficient.
	Citations []Citation `json:"citations,omitempty"`

	// InsufficientEvidence is true when no candidate material resolved the
	// question — the operator answers manually (AC-5). Mutually exclusive with
	// a drafted suggestion.
	InsufficientEvidence bool `json:"insufficient_evidence"`

	// Suppressed is true when a draft was withheld (failed citation gate or
	// backend unavailable). Reason carries the fixed cause. Mutually exclusive
	// with a drafted suggestion.
	Suppressed bool   `json:"suppressed"`
	Reason     string `json:"reason,omitempty"`

	// Model provenance, surfaced for transparency (AC-8). The values the
	// inference backend actually served. Present whenever a generation ran.
	ModelName     string `json:"model_name,omitempty"`
	ModelVersion  string `json:"model_version,omitempty"`
	ModelProvider string `json:"model_provider,omitempty"`

	// CloudRouted is true when the generation was routed to a cloud LLM rather
	// than local Ollama — the frontend renders a visible banner (CLAUDE.md
	// inference-backend rule). Always false in v0 (local Ollama only,
	// P0-441-6); the field exists so the follow-on cloud opt-in surfaces it
	// without a wire-shape change.
	CloudRouted bool `json:"cloud_routed"`
}

// ApprovedAnswer is the result of Service.Approve: the now-approved answer's
// boundary state, proving human_approved=TRUE with a recorded human_approver.
type ApprovedAnswer struct {
	AnswerID      string `json:"answer_id"`
	QuestionID    string `json:"question_id"`
	Narrative     string `json:"narrative"`
	AnswerValue   string `json:"answer_value"`
	HumanApproved bool   `json:"human_approved"`
	HumanApprover string `json:"human_approver"`
}

// Suppression / outcome reasons (fixed vocabulary — safe to render in the UI;
// never carry model text or backend error detail, slice-367 leak discipline).
const (
	ReasonGenerationUnavailable = "generation_unavailable"
	ReasonUnresolvedCitation    = "unresolved_citation"
	ReasonNoCitations           = "no_citations"
	ReasonInsufficientEvidence  = "insufficient_evidence"
)

// Sentinel errors. Callers match these with errors.Is. A suppressed or
// insufficient suggestion is NOT an error — it is conveyed via the Suggestion
// fields. These exist for genuine failures + the approval path.
var (
	// ErrQuestionNotFound is returned when the question id names no
	// tenant-owned questionnaire_questions row (RLS-invisible or absent).
	ErrQuestionNotFound = errors.New("qaisuggest: question not found")

	// ErrAnswerNotFound is returned by Approve when the answer id names no
	// tenant-owned, AI-assisted draft to approve.
	ErrAnswerNotFound = errors.New("qaisuggest: ai-suggested answer not found")

	// ErrApproverRequired is returned by Approve when the approver id is
	// blank — the Go mirror of the DB CHECK (P0-441-8). Re-exported from
	// internal/llm so callers match one error.
	ErrApproverRequired = errors.New("qaisuggest: approval requires a human_approver")
)
