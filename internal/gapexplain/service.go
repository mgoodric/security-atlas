package gapexplain

import (
	"context"
	"fmt"

	"github.com/google/uuid"

	"github.com/mgoodric/security-atlas/internal/llm"
)

// RollupReader assembles the deterministic per-control gap rollup under the
// caller's RLS context (AC-1). The production implementation is *Store; tests
// supply a fake so the suppression + cross-tenant branches are exercised
// without a live Postgres on the unit surface (the integration tier uses the
// real *Store).
type RollupReader interface {
	Rollup(ctx context.Context, controlID uuid.UUID) (Rollup, error)
}

// Service is the gap-explanation orchestrator. It assembles the deterministic
// rollup, runs ONE bounded local-Ollama generation, validates every citation
// against tenant-owned rows, and returns a read-only Explanation. It holds no
// per-call state and persists nothing (P0-444-4): the explanation is a
// comprehension aid regenerated on demand.
type Service struct {
	rollups  RollupReader
	client   llm.Client
	resolver CitationResolver
}

// NewService wires the rollup reader, the local-inference client (Ollama in
// production, Stub in CI), and the citation resolver. In production all three
// are backed by the same *Store + llm client; the seams exist for tests.
func NewService(rollups RollupReader, client llm.Client, resolver CitationResolver) *Service {
	return &Service{rollups: rollups, client: client, resolver: resolver}
}

// Explain produces the gap explanation for one control in the caller's tenant
// context. It ALWAYS returns the deterministic Rollup (AC-7, P0-444-7); the
// plain-language Text + Citations are present only when generation succeeded
// AND every citation resolved to a tenant-owned row (AC-4). On any failure the
// returned Explanation has Suppressed=true and a fixed Reason, and the caller
// renders the rollup alone.
//
// The only error Explain returns is a genuine rollup-assembly failure (the DB
// is unreachable, the tenant context is missing) — in that case there is no
// rollup to render either. A model/citation failure is NOT an error: it is
// graceful degradation conveyed via Suppressed.
func (s *Service) Explain(ctx context.Context, controlID uuid.UUID) (Explanation, error) {
	rollup, err := s.rollups.Rollup(ctx, controlID)
	if err != nil {
		return Explanation{}, fmt.Errorf("gapexplain: assemble rollup: %w", err)
	}

	exp := Explanation{Rollup: rollup}

	res, err := s.client.Generate(ctx, llm.GenerateRequest{
		Surface:       llm.SurfaceGapExplanation,
		PromptVersion: promptVersion,
		SystemPrompt:  systemPrompt + "\n\nRollup:\n" + buildPrompt(rollup),
		// No Context: this surface does not persist an ai_generations row
		// (P0-444-4), and the local-Ollama client consumes only the system
		// prompt. The deterministic facts are already inlined above.
		Context:   nil,
		MaxTokens: MaxExplanationTokens,
		Timeout:   GenerationTimeout,
	})
	if err != nil {
		// Backend down, timeout, or a malformed request — degrade gracefully.
		// The fixed reason carries no backend detail (slice-367 leak
		// discipline); the real error is the caller's to log, not the UI's to
		// show.
		exp.Suppressed = true
		exp.Reason = ReasonGenerationUnavailable
		return exp, nil
	}

	// Provenance is surfaced even if the citation gate later suppresses the
	// text — the operator should still see which model ran (AC-6 transparency).
	exp.ModelName = res.ModelName
	exp.ModelVersion = res.ModelVersion
	exp.ModelProvider = res.ModelProvider

	citations, ok, reason, err := validateCitations(ctx, s.resolver, res.Text, allowedIDs(rollup))
	if err != nil {
		// A resolution query failed (DB error mid-validation). Treat as
		// suppression, not a hard error — the rollup still renders. We do not
		// trust a partially-validated draft.
		exp.Suppressed = true
		exp.Reason = ReasonUnresolvedCitation
		return exp, nil
	}
	if !ok {
		exp.Suppressed = true
		exp.Reason = reason
		return exp, nil
	}

	exp.Text = res.Text
	exp.Citations = citations
	return exp, nil
}
