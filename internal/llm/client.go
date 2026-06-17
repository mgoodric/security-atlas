// Package llm is the shared local-inference substrate for security-atlas's
// AI-assist surfaces (slice 498). It is the foundation the four ready v0
// surfaces (440 questionnaire-answer suggestion, 441 SCF-mapping suggestion,
// 444 gap explanation, 471 board-narrative draft) all consume, so each
// becomes a thin caller rather than re-authoring the transport + audit +
// enforcement inline.
//
// The package ships three things and deliberately nothing more:
//
//  1. A narrow, provider-agnostic Client interface (Generate). The local
//     Ollama implementation (ollama.go) is the default; slice 499 adds a
//     cloud implementation behind the SAME interface (no caller change),
//     and slice 500 layers pgvector retrieval IN FRONT of it (the client
//     takes already-assembled context, it never retrieves). A Stub
//     implementation (stub.go) is the CI seam every consumer reuses so
//     tests never need a live Ollama.
//
//  2. The ai_generations audit writer (audit.go) — one append-only,
//     tenant-scoped, snapshot-at-generation row per generation, written via
//     parameterized sqlc (model output is bound as a value, never SQL).
//
//  3. The reusable ai_assisted <-> human_approver enforcement guard
//     (enforce.go) — the Go mirror of the DB CHECK template
//     ai_assist_human_approver_guard, for friendly early rejection before
//     the DB round-trip. The DB CHECK remains the authoritative gate.
//
// SCOPE DISCIPLINE (anti-criteria, block merge):
//   - LOCAL OLLAMA ONLY. No cloud routing (P0-498-1; slice 499).
//   - NO pgvector / retrieval. The client takes pre-assembled context
//     (P0-498-2; slice 500).
//   - NO user-facing surface. Library + a thin internal smoke consumer only
//     (P0-498-3; surfaces stay 440/441/444/471).
//   - Token budget + timeout are MANDATORY on every request (P0-498-6).
//   - Model output is opaque text in/out; never executed, eval'd, or
//     interpolated into SQL/shell/path (P0-498-7).
package llm

import (
	"context"
	"errors"
	"fmt"
	"time"
)

// Surface enumerates the AI-assist surfaces that write generations. It MUST
// mirror migrations/sql/20260607000000_ai_generations.sql
// (ai_generations_surface_chk). Adding a surface requires extending BOTH the
// migration CHECK and this set.
type Surface string

const (
	SurfaceQuestionnaire  Surface = "questionnaire"
	SurfaceBoardNarrative Surface = "board_narrative"
	SurfaceGapExplanation Surface = "gap_explanation"
	SurfaceChecklist      Surface = "checklist"
	SurfaceSummary        Surface = "summary"
)

// validSurfaces is the canonical set; ValidSurface gates writer + request
// validation so a typo surfaces as a clean Go error rather than a 23514
// check_violation at the DB.
var validSurfaces = map[Surface]bool{
	SurfaceQuestionnaire:  true,
	SurfaceBoardNarrative: true,
	SurfaceGapExplanation: true,
	SurfaceChecklist:      true,
	SurfaceSummary:        true,
}

// ValidSurface reports whether s is a known AI-assist surface.
func ValidSurface(s Surface) bool { return validSurfaces[s] }

// MaxTokenBudget is the upper bound a single request may ask for. A request
// exceeding this is rejected with ErrTokenBudgetExceeded BEFORE any inference
// is launched (D-mitigation, P0-498-6). The cap is a shared primitive every
// surface inherits; surfaces may request LESS but never more.
const MaxTokenBudget = 4096

// Sentinel errors. Callers match these with errors.Is. Keeping them flat +
// provider-agnostic means the Ollama, Stub, and (future) cloud
// implementations all reject the same way.
var (
	// ErrInvalidRequest is the umbrella for a malformed GenerateRequest
	// (missing system prompt, model id, non-positive budget/timeout, etc.).
	ErrInvalidRequest = errors.New("llm: invalid request")
	// ErrTokenBudgetExceeded is returned when MaxTokens exceeds the
	// configured cap (MaxTokenBudget) -- never launch an unbounded job.
	ErrTokenBudgetExceeded = errors.New("llm: token budget exceeds cap")
	// ErrTimeout is returned when generation exceeds the request's Timeout
	// (the context deadline fires).
	ErrTimeout = errors.New("llm: generation timed out")
	// ErrBackend wraps a transport/backend failure (Ollama unreachable,
	// non-2xx, decode error). Lets callers distinguish a backend outage from
	// a bad request.
	ErrBackend = errors.New("llm: backend failure")
)

// GenerateRequest is the provider-agnostic input to Client.Generate.
//
// The request carries everything the substrate needs and NOTHING surface-
// specific: the assembled prompt + context come from the caller (slice 500
// assembles context upstream), and the caps are mandatory. There is no
// citation, template, or approval field here -- those belong to the consumer
// surfaces.
type GenerateRequest struct {
	// Surface is the AI-assist surface on whose behalf the generation runs.
	// Recorded on the ai_generations row.
	Surface Surface

	// ModelID is the model identifier to run (e.g. "llama3.1:8b-instruct-q5").
	// Empty means "use the configured default" (resolved by the
	// implementation). The RESOLVED model name/version are returned in the
	// result, not echoed from here.
	ModelID string

	// PromptVersion is the caller's prompt-template version tag, recorded for
	// forensic reconstruction (slice-182 schema contract). Mandatory.
	PromptVersion string

	// SystemPrompt is the full system prompt. Mandatory. Treated as opaque
	// text -- the substrate never parses or rewrites it.
	SystemPrompt string

	// Context is the already-assembled context the surface wants the model to
	// see (cited evidence excerpts, rollups, prior answers, ...). The
	// substrate does NOT retrieve this (P0-498-2); it passes it through and
	// records it on the audit row. JSON-serializable; keys + values are the
	// surface's concern.
	Context map[string]any

	// MaxTokens is the MANDATORY token budget. Must be > 0 and <=
	// MaxTokenBudget; otherwise the request is rejected before inference
	// (P0-498-6).
	MaxTokens int

	// Timeout is the MANDATORY wall-clock deadline for the generation. Must
	// be > 0. The implementation derives a context deadline from it and
	// returns ErrTimeout if exceeded (P0-498-6).
	Timeout time.Duration
}

// Validate runs the shared mandatory-field + cap contract. It is the exported
// entry point a cloud Client implementation in a sibling package (internal/llm/cloud,
// slice 499) calls so its rejection of an over-cap / malformed request is
// IDENTICAL to the in-package Ollama / Stub clients — there is exactly one
// definition of the contract. In-package callers use the unexported validate().
func (r GenerateRequest) Validate() error { return r.validate() }

// validate enforces the mandatory-field + cap invariants shared by every
// implementation. Called by each Client.Generate at entry so the rejection
// is identical across Ollama / Stub / cloud.
func (r GenerateRequest) validate() error {
	if !ValidSurface(r.Surface) {
		return fmt.Errorf("%w: unknown surface %q", ErrInvalidRequest, r.Surface)
	}
	if r.PromptVersion == "" {
		return fmt.Errorf("%w: prompt_version is required", ErrInvalidRequest)
	}
	if r.SystemPrompt == "" {
		return fmt.Errorf("%w: system_prompt is required", ErrInvalidRequest)
	}
	if r.Timeout <= 0 {
		return fmt.Errorf("%w: timeout must be > 0 (mandatory cap)", ErrInvalidRequest)
	}
	if r.MaxTokens <= 0 {
		return fmt.Errorf("%w: max_tokens must be > 0 (mandatory cap)", ErrInvalidRequest)
	}
	if r.MaxTokens > MaxTokenBudget {
		return fmt.Errorf("%w: max_tokens %d > cap %d", ErrTokenBudgetExceeded, r.MaxTokens, MaxTokenBudget)
	}
	return nil
}

// GenerateResult is the provider-agnostic output of Client.Generate.
type GenerateResult struct {
	// Text is the raw model draft. OPAQUE: the substrate never executes,
	// eval's, or interpolates it (P0-498-7). The consumer surface validates
	// citations / numeric claims / tone -- not this package.
	Text string

	// Resolved model provenance, recorded on the ai_generations row
	// (slice-182 schema contract). These are the ACTUAL values the backend
	// served, so history is reconstructable even if config later changes.
	ModelName     string
	ModelVersion  string
	ModelProvider string
}

// Client is the narrow, provider-agnostic inference seam. Generate is the
// ONLY method -- keeping the interface to one method is what lets slice 499
// add a cloud implementation and the Stub serve CI without touching any
// caller. Implementations MUST be stateless across calls (each Generate is
// independent; no cross-call/cross-tenant retention -- I-mitigation).
type Client interface {
	Generate(ctx context.Context, req GenerateRequest) (GenerateResult, error)
}
