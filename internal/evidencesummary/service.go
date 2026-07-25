package evidencesummary

import (
	"context"
	"fmt"

	"github.com/google/uuid"

	"github.com/mgoodric/security-atlas/internal/llm"
)

// EvidenceReader assembles the deterministic bounded evidence set for one
// control under the caller's RLS context (AC-1). The production implementation
// is *Store; tests supply a fake so the suppression + cross-tenant branches are
// exercised without a live Postgres on the unit surface (the integration tier
// uses the real *Store).
type EvidenceReader interface {
	EvidenceSet(ctx context.Context, controlID uuid.UUID) (EvidenceSet, error)
}

// Service is the evidence-summarization orchestrator. It assembles the
// deterministic bounded evidence set, runs ONE bounded generation against the
// per-tenant inference client, validates every citation against tenant-owned
// rows, and returns a read-only Summary. It holds no per-call state and
// persists nothing (P0-502-4): the summary is a comprehension aid regenerated
// on demand.
type Service struct {
	reader   EvidenceReader
	client   llm.Client
	resolver CitationResolver
}

// NewService wires the evidence reader, the inference client (the slice-499
// per-tenant router in production, the Stub in CI), and the citation resolver.
// In production the reader + resolver are both backed by the same *Store.
func NewService(reader EvidenceReader, client llm.Client, resolver CitationResolver) *Service {
	return &Service{reader: reader, client: client, resolver: resolver}
}

// Summarize produces the evidence summary for one control in the caller's
// tenant context. It ALWAYS returns the deterministic bounded EvidenceSet
// (AC-7, P0-502-7); the plain-language Text + Citations are present only when
// generation succeeded AND every citation resolved to a tenant-owned row
// (AC-4). On any failure the returned Summary has Suppressed=true and a fixed
// Reason, and the caller renders the evidence list alone.
//
// The only error Summarize returns is a genuine evidence-read failure (the DB
// is unreachable, the tenant context is missing) — in that case there is no
// evidence set to render either. A model/citation failure is NOT an error: it
// is graceful degradation conveyed via Suppressed.
func (s *Service) Summarize(ctx context.Context, controlID uuid.UUID) (Summary, error) {
	set, err := s.reader.EvidenceSet(ctx, controlID)
	if err != nil {
		return Summary{}, fmt.Errorf("evidencesummary: assemble evidence set: %w", err)
	}
	return runSummary(ctx, s.client, s.resolver, set), nil
}

// runSummary is the shared NON-BINDING summarization pipeline used by BOTH the
// live control-detail surface (Service.Summarize, slice 502) and the
// period-scoped audit-workspace surface (PeriodService.PeriodSummarize, slice
// 749). Given an already-assembled deterministic EvidenceSet, it runs ONE bounded
// generation against the inference client, validates EVERY citation against the
// supplied resolver + the grounding set (allowedIDs over the EvidenceSet), and
// returns a read-only Summary. It persists nothing (P0-502-4) and never mutates
// the EvidenceSet.
//
// The contract is identical regardless of corpus: the EvidenceSet is ALWAYS
// echoed back (AC-7, P0-502-7); the Text + Citations are present only when
// generation succeeded AND every citation resolved to a tenant-owned row in the
// grounding set (no fabricated coverage — P0-502-1). On any failure the returned
// Summary has Suppressed=true and a fixed, leak-safe Reason. Factoring this out
// is what makes slice 749 a literal thin variant of 502 (the ONLY differences
// are the corpus the caller assembled and the resolver it passed — the live
// surface passes a live resolver, the frozen surface passes a horizon-bound one).
func runSummary(ctx context.Context, client llm.Client, resolver CitationResolver, set EvidenceSet) Summary {
	sum := Summary{EvidenceSet: set}

	// Nothing to summarize: degrade to the (empty) deterministic list without
	// burning a model call. The model could only fabricate coverage from no
	// evidence — exactly the threat-model-T failure we suppress.
	if len(set.Records) == 0 {
		sum.Suppressed = true
		sum.Reason = ReasonNoEvidence
		return sum
	}

	res, err := client.Generate(ctx, llm.GenerateRequest{
		Surface:       llm.SurfaceSummary,
		PromptVersion: promptVersion,
		SystemPrompt:  systemPrompt + "\n\nEvidence:\n" + buildPrompt(set),
		// No Context: this surface does not persist an ai_generations row
		// (P0-502-4), and the inference client consumes only the system prompt.
		// The deterministic facts are already inlined above.
		Context:   nil,
		MaxTokens: MaxSummaryTokens,
		Timeout:   GenerationTimeout,
	})
	if err != nil {
		// Backend down, timeout, or a malformed request — degrade gracefully.
		// The fixed reason carries no backend detail (slice-367 leak
		// discipline); the real error is the caller's to log, not the UI's to
		// show.
		sum.Suppressed = true
		sum.Reason = ReasonGenerationUnavailable
		return sum
	}

	// Provenance is surfaced even if the citation gate later suppresses the
	// text — the operator should still see which model ran (AC-6 transparency).
	sum.ModelName = res.ModelName
	sum.ModelVersion = res.ModelVersion
	sum.ModelProvider = res.ModelProvider

	citations, ok, reason, err := validateCitations(ctx, resolver, res.Text, allowedIDs(set))
	if err != nil {
		// A resolution query failed (DB error mid-validation). Treat as
		// suppression, not a hard error — the evidence list still renders. We do
		// not trust a partially-validated draft.
		sum.Suppressed = true
		sum.Reason = ReasonUnresolvedCitation
		return sum
	}
	if !ok {
		sum.Suppressed = true
		sum.Reason = reason
		return sum
	}

	sum.Text = res.Text
	sum.Citations = citations
	return sum
}
