package qaisuggest

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/google/uuid"

	"github.com/mgoodric/security-atlas/internal/llm"
)

// Retriever assembles tenant-owned keyword candidates + reads the question
// text under the caller's RLS context (AC-1). The production implementation is
// *Store; tests supply a fake so the suppression + cross-tenant branches are
// exercised on the unit surface without a live Postgres (the integration tier
// uses the real *Store).
type Retriever interface {
	QuestionText(ctx context.Context, questionID uuid.UUID) (string, error)
	RetrieveCandidates(ctx context.Context, keywords []string) ([]Candidate, error)
}

// Service is the questionnaire AI-suggestion orchestrator. It retrieves
// tenant-owned candidates, runs ONE bounded local-Ollama generation, validates
// every citation against tenant-owned rows, persists a DRAFT on success, and
// approves a draft on a separate operator action. The constitutional
// invariants (no fabricated coverage, no cross-tenant bleed, one-click
// approval, local-only) are all enforced here + at the DB layer.
type Service struct {
	retriever Retriever
	client    llm.Client
	resolver  CitationResolver
	store     ApprovalStore
}

// ApprovalStore is the narrow persistence seam the Service needs: persist a
// draft, approve a draft. Production is *Store; the interface keeps the Service
// testable.
type ApprovalStore interface {
	PersistDraft(ctx context.Context, questionID uuid.UUID, narrative string, citationsJSON []byte, prov Provenance) (string, error)
	Approve(ctx context.Context, answerID uuid.UUID, finalNarrative, answerValue, approver string) (ApprovedAnswer, error)
}

// NewService wires the retriever, the local-inference client (Ollama in
// production, Stub in CI), the citation resolver, and the draft store. In
// production all the DB seams are backed by the same *Store.
func NewService(retriever Retriever, client llm.Client, resolver CitationResolver, store ApprovalStore) *Service {
	return &Service{retriever: retriever, client: client, resolver: resolver, store: store}
}

// SuggestParams carries the request: the question to answer + who is asking
// (recorded as the draft's authored_by provenance).
type SuggestParams struct {
	QuestionID uuid.UUID
	AuthoredBy string
}

// Suggest produces (and persists, on success) a cited DRAFT answer for one
// question in the caller's tenant context. The outcome is one of three shapes,
// conveyed on the returned Suggestion (NOT via error — a suppressed or
// insufficient suggestion is a normal outcome):
//
//   - Drafted: a valid, fully-cited answer persisted as an unapproved draft.
//   - Insufficient evidence: no candidate material backed the question; nothing
//     persisted; operator answers manually (AC-5, no-fabricated-coverage).
//   - Suppressed: a draft failed the citation gate or the backend was
//     unavailable; nothing persisted (a draft the operator must not see is
//     never written — P0-441-4).
//
// The only error Suggest returns is a genuine infrastructure failure (the DB is
// unreachable, the tenant context is missing, the question does not exist).
func (s *Service) Suggest(ctx context.Context, p SuggestParams) (Suggestion, error) {
	out := Suggestion{QuestionID: p.QuestionID.String()}

	questionText, err := s.retriever.QuestionText(ctx, p.QuestionID)
	if err != nil {
		return Suggestion{}, err // ErrQuestionNotFound or infra failure
	}

	// AC-1: keyword first-pass retrieval (NO pgvector — P0-441-5).
	keywords := keywordsFrom(questionText)
	candidates, err := s.retriever.RetrieveCandidates(ctx, keywords)
	if err != nil {
		return Suggestion{}, fmt.Errorf("qaisuggest: retrieve: %w", err)
	}
	ranked := rankCandidates(candidates, keywords, maxCandidates)

	// AC-5 (structural): no candidate material at all => insufficient evidence.
	// The model is NEVER asked to answer a question with nothing to ground it;
	// fabrication is impossible because there is nothing to fabricate from.
	if len(ranked) == 0 {
		out.InsufficientEvidence = true
		out.Reason = ReasonInsufficientEvidence
		return out, nil
	}

	res, err := s.client.Generate(ctx, llm.GenerateRequest{
		Surface:       llm.SurfaceQuestionnaire,
		PromptVersion: promptVersion,
		SystemPrompt:  systemPrompt + "\n\n" + buildPrompt(questionText, ranked),
		Context:       candidateContext(questionText, ranked),
		MaxTokens:     MaxAnswerTokens,
		Timeout:       GenerationTimeout,
	})
	if err != nil {
		// Backend down/timeout/malformed — degrade gracefully. The fixed
		// reason carries no backend detail (slice-367 leak discipline).
		out.Suppressed = true
		out.Reason = ReasonGenerationUnavailable
		return out, nil
	}

	out.ModelName = res.ModelName
	out.ModelVersion = res.ModelVersion
	out.ModelProvider = res.ModelProvider
	// v0 is local Ollama only (P0-441-6). A non-local provider would set the
	// banner flag; in v0 this is structurally false.
	out.CloudRouted = isCloudProvider(res.ModelProvider)

	// AC-5 (model-judged): the model emitted the insufficient sentinel because
	// the candidates do not actually support an answer.
	if isInsufficient(res.Text) {
		out.InsufficientEvidence = true
		out.Reason = ReasonInsufficientEvidence
		return out, nil
	}

	// AC-4: mandatory-citation enforcement. Every cited id must be in the
	// grounding set AND resolve to a tenant-owned row. A single failure
	// suppresses the whole draft (the strict JUDGMENT call) — and because a
	// suppressed draft must never reach the operator (P0-441-4), NOTHING is
	// persisted.
	citations, ok, reason, verr := validateCitations(ctx, s.resolver, res.Text, allowedIDs(ranked))
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

	// Valid, fully-cited draft. Persist as an UNAPPROVED draft (AC-6,
	// P0-441-1). The operator approves it in a separate one-click action.
	citationsJSON, jerr := json.Marshal(citations)
	if jerr != nil {
		return Suggestion{}, fmt.Errorf("qaisuggest: marshal citations: %w", jerr)
	}
	answerID, perr := s.store.PersistDraft(ctx, p.QuestionID, res.Text, citationsJSON, Provenance{
		AuthoredBy:    p.AuthoredBy,
		PromptVersion: promptVersion,
		ModelName:     res.ModelName,
		ModelVersion:  res.ModelVersion,
		ModelProvider: res.ModelProvider,
	})
	if perr != nil {
		return Suggestion{}, fmt.Errorf("qaisuggest: persist: %w", perr)
	}

	out.AnswerID = answerID
	out.Draft = res.Text
	out.Citations = citations
	return out, nil
}

// ApproveParams carries the one-click approval: which draft, the operator's
// (possibly edited) final text, and the approver id.
type ApproveParams struct {
	AnswerID    uuid.UUID
	Narrative   string
	AnswerValue string
	Approver    string
}

// Approve is the one-click human approval of a draft (AC-6/AC-7/AC-12). It
// records the approver and flips human_approved=TRUE — the approved text is
// what the questionnaire stores. There is NO auto-approve path; this is the
// ONLY way an AI-suggested answer becomes approved.
//
// The blank-approver guard fires here (the Go mirror of the DB CHECK,
// P0-441-8) so a confused caller gets ErrApproverRequired rather than a raw
// 23514 check_violation. The DB CHECK remains authoritative.
func (s *Service) Approve(ctx context.Context, p ApproveParams) (ApprovedAnswer, error) {
	if strings.TrimSpace(p.Approver) == "" {
		return ApprovedAnswer{}, ErrApproverRequired
	}
	// Belt-and-suspenders: the shared llm guard, the exact DB predicate.
	if err := llm.EnforceApproval(llm.ApprovalState{
		AIAssisted:    true,
		HumanApproved: true,
		HumanApprover: p.Approver,
	}); err != nil {
		return ApprovedAnswer{}, ErrApproverRequired
	}
	approved, err := s.store.Approve(ctx, p.AnswerID, p.Narrative, p.AnswerValue, p.Approver)
	if err != nil {
		return ApprovedAnswer{}, err
	}
	return approved, nil
}

// candidateContext builds the structured context map recorded on the
// ai_generations audit row (R-mitigation: which candidate ids backed the
// answer is reconstructable). The model only consumes the system prompt; this
// map is the forensic record, not an additional prompt.
func candidateContext(questionText string, cands []Candidate) map[string]any {
	ids := make([]string, 0, len(cands))
	for _, c := range cands {
		ids = append(ids, string(c.Kind)+":"+c.ID)
	}
	return map[string]any{
		"question_text": questionText,
		"candidate_ids": ids,
	}
}

// isCloudProvider reports whether the resolved model provider is a cloud LLM
// (triggers the visible routing banner). v0 only ever serves local Ollama or
// the stub, so this is structurally false; the helper exists so the cloud
// opt-in follow-on surfaces the banner without a wire change.
func isCloudProvider(provider string) bool {
	switch strings.ToLower(provider) {
	case "", "ollama", "ollama-local", "local", "stub":
		return false
	default:
		return true
	}
}
