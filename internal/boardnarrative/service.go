package boardnarrative

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/google/uuid"

	"github.com/mgoodric/security-atlas/internal/llm"
)

// RollupSource assembles the deterministic rollup for a section under the
// caller's RLS context: it reuses the existing board.Brief data path for the
// numbers and reads the bounded, tenant-owned citable control/evidence excerpts
// behind them. Production is *Store; tests supply a fake so the suppression +
// cross-tenant branches are exercised without a live Postgres.
type RollupSource interface {
	// CoverageRollup computes the Brief-grounded coverage rollup for `periodEnd`
	// plus the bounded citable excerpts, all under the requesting tenant's RLS
	// context. Kept for the slice-440 one-section call site + its tests.
	CoverageRollup(ctx context.Context, periodEnd string) (Rollup, error)

	// SectionRollup computes the deterministic rollup for ANY section key
	// (slice 501) for `periodEnd`, under the requesting tenant's RLS context. It
	// projects the SAME board.Brief data path through the section's
	// SectionDef.buildRollup, so every section grounds on the same frozen Brief
	// + the same bounded citable excerpts. Returns ErrUnknownSection for a key
	// that has no SectionDef.
	SectionRollup(ctx context.Context, section SectionKey, periodEnd string) (Rollup, error)
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
	return s.GenerateSection(ctx, SectionControlCoverage, p)
}

// GenerateSection produces (and persists, on success) a validated DRAFT of ANY
// AI-drafted section (slice 501) in the caller's tenant context. It is the
// section-agnostic generalization of the slice-440 coverage pipeline: it looks
// the section's SectionDef up, assembles that section's deterministic rollup,
// runs ONE bounded generation through the per-tenant inference client, and
// validates the draft through the SAME FOUR pre-operator gates (shape, tone,
// numeric, citations) in the SAME order — no gate added, none weakened
// (P0-501-6). The outcome shape (Drafted | Suppressed) is identical to Generate.
//
// Returns ErrUnknownSection for a key with no SectionDef (a programmer error).
func (s *Service) GenerateSection(ctx context.Context, section SectionKey, p GenerateParams) (SectionResult, error) {
	def, ok := sectionDef(section)
	if !ok {
		return SectionResult{}, fmt.Errorf("%w: %q", ErrUnknownSection, section)
	}
	out := SectionResult{Section: section}

	// Guardrail 1 (input shape) — assemble the deterministic rollup + bounded
	// cited excerpts under the caller's RLS, projected through THIS section's
	// SectionDef from the existing board.Brief data path.
	rollup, err := s.rollups.SectionRollup(ctx, section, p.PeriodEnd)
	if err != nil {
		return SectionResult{}, err // ErrNoBriefData / ErrUnknownSection / infra
	}
	sortExcerpts(rollup.Excerpts)

	system := def.systemPrompt() + "\n\n" + def.userPrompt(rollup)
	req := llm.GenerateRequest{
		Surface:       llm.SurfaceBoardNarrative,
		PromptVersion: def.PromptVersion,
		SystemPrompt:  system,
		Context:       sectionContextInputs(section, rollup),
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
	// Local Ollama by default (slice 499 per-tenant routing); a cloud provider
	// sets the banner flag the frontend renders.
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
			PromptVersion:  def.PromptVersion,
			ModelName:      res.ModelName,
			ModelVersion:   res.ModelVersion,
			ModelProvider:  res.ModelProvider,
			SystemPrompt:   req.SystemPrompt,
			ContextInputs:  req.Context,
			RawDraft:       res.Text,
			SurfaceSubject: p.PeriodEnd + ":" + string(section),
		}
		if aerr := s.audit.Write(ctx, gen); aerr != nil {
			return SectionResult{}, fmt.Errorf("boardnarrative: audit write: %w", aerr)
		}
	}

	// Guardrail 6 (section shape) — freestyle output is rejected. Checked first
	// because a misshapen draft is the cheapest reject and the others assume the
	// numbered structure. Section-parameterized via the SectionDef.
	if !enforceShapeFor(res.Text, def.Heading, def.ExpectedItems) {
		out.Suppressed = true
		out.Reason = ReasonBadShape
		return out, nil
	}

	// Guardrail 7 (tone) — a banned marketing phrase rejects the draft. The
	// banned-phrase check is section-agnostic (one list, allow-list-honoring).
	if containsBannedPhrase(res.Text) {
		out.Suppressed = true
		out.Reason = ReasonBannedPhrase
		return out, nil
	}

	// Guardrail 5 (numeric verification) — THE defining board-narrative
	// guardrail, via the reusable numeric library (slice 501). Every number in
	// the draft must be a value THIS section's deterministic rollup produced; a
	// single mismatch auto-rejects the draft BEFORE the operator sees it.
	if !VerifyNumbers(res.Text, rollup.AllowedNumbers(), rollup.PeriodEnd) {
		out.Suppressed = true
		out.Reason = ReasonNumericMismatch
		return out, nil
	}

	// Guardrail 4 (mandatory citations) — every cited id must be in the
	// grounding set AND resolve to a tenant-owned row. A single failure
	// suppresses the whole draft; because a suppressed draft must never reach
	// the operator (P0-501-2), NOTHING is persisted.
	citations, cok, reason, verr := validateCitations(ctx, s.res, res.Text, rollup.allowedExcerptIDs())
	if verr != nil {
		out.Suppressed = true
		out.Reason = ReasonUnresolvedCitation
		return out, nil
	}
	if !cok {
		out.Suppressed = true
		out.Reason = reason
		return out, nil
	}

	// Valid, fully-validated draft. Persist as an UNAPPROVED per-section record
	// (P0-501-1). The operator approves it in a separate one-click action.
	citationsJSON, jerr := json.Marshal(citations)
	if jerr != nil {
		return SectionResult{}, fmt.Errorf("boardnarrative: marshal citations: %w", jerr)
	}
	recordID, perr := s.store.PersistDraft(ctx, section, p.PeriodEnd, res.Text, citationsJSON, Provenance{
		AuthoredBy:    p.AuthoredBy,
		PromptVersion: def.PromptVersion,
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

// GenerateAll drafts EVERY AI-drafted section (the full narrative) for the
// period, in canonical order (slice 501 — AC-1/AC-7). Each section runs the
// independent four-gate pipeline; a section that fails a gate is SUPPRESSED
// (Suppressed=true with a Reason) and persists nothing, exactly as in the
// one-section path — one section's suppression does NOT abort the others (a
// real board pack can ship the sections that passed and the operator
// regenerates the rest). The returned slice is one SectionResult per section in
// canonical order.
//
// A genuine infra error (DB unreachable, audit-write failure) is returned as
// the error and aborts — those are not "the operator can retry" outcomes. An
// ErrNoBriefData (no posture to summarize) is returned as the error because it
// applies to the WHOLE narrative, not one section.
func (s *Service) GenerateAll(ctx context.Context, p GenerateParams) ([]SectionResult, error) {
	keys := sortedSectionKeys()
	out := make([]SectionResult, 0, len(keys))
	for _, key := range keys {
		res, err := s.GenerateSection(ctx, key, p)
		if err != nil {
			return nil, err
		}
		out = append(out, res)
	}
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
