package boardnarrative

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/google/uuid"

	"github.com/mgoodric/security-atlas/internal/llm"
)

// RollupSource assembles the deterministic rollup for the coverage section
// under the caller's RLS context: it reuses the existing board.Brief data path
// for the numbers and reads the bounded, tenant-owned citable control/evidence
// excerpts behind them. Production is *Store; tests supply a fake so the
// suppression + cross-tenant branches are exercised without a live Postgres.
type RollupSource interface {
	// CoverageRollup computes the Brief-grounded rollup for `periodEnd` plus the
	// bounded citable excerpts, all under the requesting tenant's RLS context.
	CoverageRollup(ctx context.Context, periodEnd string) (Rollup, error)
}

// AuditSink persists the full append-only generation record (system prompt +
// context inputs + raw draft + provenance) — guardrail 3's forensic half. The
// production sink wraps internal/llm's AuditWriter (see NewAuditSink); the
// interface keeps the Service testable.
type AuditSink interface {
	Write(ctx context.Context, g llm.Generation) error
}

// auditSink adapts an *llm.AuditWriter (whose Write returns the stored row) to
// the narrow error-only AuditSink the Service needs. The full append-only
// ai_generations row is the slice-498 shared audit record; this surface writes
// to it with Surface=board_narrative (guardrail 3).
type auditSink struct{ w *llm.AuditWriter }

// NewAuditSink wraps an *llm.AuditWriter as the Service's AuditSink.
func NewAuditSink(w *llm.AuditWriter) AuditSink { return &auditSink{w: w} }

func (a *auditSink) Write(ctx context.Context, g llm.Generation) error {
	_, err := a.w.Write(ctx, g)
	return err
}

// DraftStore persists a valid draft as a per-section record (ai_assisted,
// unapproved) and approves a section on a separate operator action — guardrail
// 2/3's current-state half. Production is *Store.
type DraftStore interface {
	PersistDraft(ctx context.Context, section SectionKey, periodEnd, rawDraft string, citationsJSON []byte, prov Provenance) (string, error)
	Approve(ctx context.Context, recordID uuid.UUID, finalText, approver string) (ApprovedSection, error)
}

// Provenance is the model + author metadata persisted onto a draft (the
// snapshot-at-generation columns — slice-182 schema contract).
type Provenance struct {
	AuthoredBy    string
	PromptVersion string
	ModelName     string
	ModelVersion  string
	ModelProvider string
}

// Service is the board-narrative AI v0 orchestrator. For ONE section it builds
// the deterministic rollup, runs ONE bounded local-Ollama generation, validates
// the draft through FOUR pre-operator gates (citations, numeric, shape, tone),
// persists a DRAFT + the full audit row on success, and approves a section on a
// separate operator action. Every constitutional invariant (no fabricated
// coverage, no fabricated numbers, no cross-tenant bleed, one-click approval,
// local-only) is enforced here + at the DB layer.
type Service struct {
	rollups RollupSource
	client  llm.Client
	res     CitationResolver
	audit   AuditSink
	store   DraftStore
}

// NewService wires the rollup source, the local-inference client (Ollama in
// production, Stub in CI), the citation resolver, the audit sink, and the draft
// store. In production the DB seams are all backed by the same *Store.
func NewService(rollups RollupSource, client llm.Client, res CitationResolver, audit AuditSink, store DraftStore) *Service {
	return &Service{rollups: rollups, client: client, res: res, audit: audit, store: store}
}

// GenerateParams carries the request: the report period to summarize + who is
// asking (recorded as the draft's authored_by provenance).
type GenerateParams struct {
	PeriodEnd  string // YYYY-MM-DD
	AuthoredBy string
}

// Generate produces (and persists, on success) a validated DRAFT of the
// control-coverage-summary section in the caller's tenant context. The outcome
// is one of two shapes on the returned SectionResult (NOT via error — a
// suppressed draft is a normal outcome):
//
//   - Drafted: a section that passed ALL four gates, persisted unapproved.
//   - Suppressed: a draft failed a gate (unresolved citation / numeric mismatch
//     / bad shape / banned phrase) or the backend was unavailable; NOTHING is
//     persisted (a draft the operator must not see is never written — P0-440-4).
//
// The only error Generate returns is a genuine infrastructure failure (the DB
// is unreachable, the tenant context is missing) or ErrNoBriefData (no posture
// to summarize).
func (s *Service) Generate(ctx context.Context, p GenerateParams) (SectionResult, error) {
	out := SectionResult{Section: SectionControlCoverage}

	// Guardrail 1 (input shape) — assemble the deterministic rollup + bounded
	// cited excerpts under the caller's RLS. The numbers come from the existing
	// board.Brief data path; the excerpts are the tenant-owned controls/evidence
	// behind them.
	rollup, err := s.rollups.CoverageRollup(ctx, p.PeriodEnd)
	if err != nil {
		return SectionResult{}, err // ErrNoBriefData or infra failure
	}
	sortExcerpts(rollup.Excerpts)

	system := buildSystemPrompt() + "\n\n" + buildPrompt(rollup)
	req := llm.GenerateRequest{
		Surface:       llm.SurfaceBoardNarrative,
		PromptVersion: promptVersion,
		SystemPrompt:  system,
		Context:       promptContextInputs(rollup),
		MaxTokens:     MaxSectionTokens,
		Timeout:       GenerationTimeout,
	}
	res, gerr := s.client.Generate(ctx, req)
	if gerr != nil {
		// Backend down/timeout/malformed — degrade gracefully. The fixed reason
		// carries no backend detail (slice-367 leak discipline).
		out.Suppressed = true
		out.Reason = ReasonGenerationUnavailable
		return out, nil
	}

	out.ModelName = res.ModelName
	out.ModelVersion = res.ModelVersion
	out.ModelProvider = res.ModelProvider
	// v0 is local Ollama only (P0-440-5). A non-local provider would set the
	// banner flag; in v0 this is structurally false.
	out.CloudRouted = isCloudProvider(res.ModelProvider)

	// Guardrail 3 (audit) — persist the FULL forensic record of THIS generation
	// (system prompt + context inputs + raw draft + provenance) regardless of
	// whether the draft passes the gates. The audit row is the immutable record
	// of what the model was told and what it produced (R-mitigation); a
	// SUPPRESSED draft is exactly the case auditors most want reconstructable.
	// An audit-write failure is a real infra error (we must not lose the record).
	if s.audit != nil {
		gen := llm.Generation{
			Surface:        llm.SurfaceBoardNarrative,
			PromptVersion:  promptVersion,
			ModelName:      res.ModelName,
			ModelVersion:   res.ModelVersion,
			ModelProvider:  res.ModelProvider,
			SystemPrompt:   req.SystemPrompt,
			ContextInputs:  req.Context,
			RawDraft:       res.Text,
			SurfaceSubject: p.PeriodEnd + ":" + string(SectionControlCoverage),
		}
		if aerr := s.audit.Write(ctx, gen); aerr != nil {
			return SectionResult{}, fmt.Errorf("boardnarrative: audit write: %w", aerr)
		}
	}

	// Guardrail 6 (section shape) — freestyle output is rejected. Checked first
	// because a misshapen draft is the cheapest reject and the others assume the
	// numbered structure.
	if !enforceShape(res.Text) {
		out.Suppressed = true
		out.Reason = ReasonBadShape
		return out, nil
	}

	// Guardrail 7 (tone) — a banned marketing phrase rejects the draft.
	if containsBannedPhrase(res.Text) {
		out.Suppressed = true
		out.Reason = ReasonBannedPhrase
		return out, nil
	}

	// Guardrail 5 (numeric verification) — THE defining board-narrative
	// guardrail. Every number in the draft must be a value the deterministic
	// rollup produced; a single mismatch auto-rejects the draft.
	if !verifyNumbers(res.Text, rollup) {
		out.Suppressed = true
		out.Reason = ReasonNumericMismatch
		return out, nil
	}

	// Guardrail 4 (mandatory citations) — every cited id must be in the
	// grounding set AND resolve to a tenant-owned row. A single failure
	// suppresses the whole draft; because a suppressed draft must never reach
	// the operator (P0-440-4), NOTHING is persisted.
	citations, ok, reason, verr := validateCitations(ctx, s.res, res.Text, rollup.allowedExcerptIDs())
	if verr != nil {
		out.Suppressed = true
		out.Reason = ReasonUnresolvedCitation
		return out, nil
	}
	if !ok {
		out.Suppressed = true
		out.Reason = reason
		return out, nil
	}

	// Valid, fully-validated draft. Persist as an UNAPPROVED per-section record
	// (P0-440-1). The operator approves it in a separate one-click action.
	citationsJSON, jerr := json.Marshal(citations)
	if jerr != nil {
		return SectionResult{}, fmt.Errorf("boardnarrative: marshal citations: %w", jerr)
	}
	recordID, perr := s.store.PersistDraft(ctx, SectionControlCoverage, p.PeriodEnd, res.Text, citationsJSON, Provenance{
		AuthoredBy:    p.AuthoredBy,
		PromptVersion: promptVersion,
		ModelName:     res.ModelName,
		ModelVersion:  res.ModelVersion,
		ModelProvider: res.ModelProvider,
	})
	if perr != nil {
		return SectionResult{}, fmt.Errorf("boardnarrative: persist: %w", perr)
	}

	out.RecordID = recordID
	out.Draft = res.Text
	out.Citations = citations
	return out, nil
}

// ApproveParams carries the one-click approval: which section record, the
// operator's (possibly edited) final text, and the approver id.
type ApproveParams struct {
	RecordID  uuid.UUID
	FinalText string
	Approver  string
}

// Approve is the one-click human approval of a section draft (guardrail 2 — per
// section). It records the approver and flips human_approved=TRUE — the approved
// text is what ships into the board pack. There is NO auto-approve path; this is
// the ONLY way an AI-drafted section becomes approved.
//
// The blank-approver guard fires here (the Go mirror of the DB CHECK, P0-440-2)
// so a confused caller gets ErrApproverRequired rather than a raw 23514
// check_violation. The DB CHECK remains authoritative.
func (s *Service) Approve(ctx context.Context, p ApproveParams) (ApprovedSection, error) {
	if strings.TrimSpace(p.Approver) == "" {
		return ApprovedSection{}, ErrApproverRequired
	}
	// Belt-and-suspenders: the shared llm guard, the exact DB predicate.
	if err := llm.EnforceApproval(llm.ApprovalState{
		AIAssisted:    true,
		HumanApproved: true,
		HumanApprover: p.Approver,
	}); err != nil {
		return ApprovedSection{}, ErrApproverRequired
	}
	approved, err := s.store.Approve(ctx, p.RecordID, p.FinalText, p.Approver)
	if err != nil {
		return ApprovedSection{}, err
	}
	return approved, nil
}

// isCloudProvider reports whether the resolved model provider is a cloud LLM
// (triggers the visible routing banner). v0 only ever serves local Ollama or
// the stub, so this is structurally false; the helper exists so the cloud
// opt-in follow-on surfaces the banner without a wire change. Mirrors
// qaisuggest.isCloudProvider.
func isCloudProvider(provider string) bool {
	switch strings.ToLower(provider) {
	case "", "ollama", "ollama-local", "local", "stub":
		return false
	default:
		return true
	}
}
