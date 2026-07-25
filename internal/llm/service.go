package llm

import (
	"context"
	"fmt"

	"github.com/mgoodric/security-atlas/internal/db/dbx"
)

// Service is the thin internal substrate that ties a Client to the
// ai_generations AuditWriter: generate a draft, then persist the forensic
// record. It is the smoke path that proves the substrate end-to-end
// (client -> ai_generations write -> enforcement) against real Postgres + the
// stub client (AC-10). It is NOT a user-facing surface (P0-498-3): there is
// no HTTP route, no auth gate of its own -- the eventual surfaces
// (440/441/444/471) own those and call this.
//
// Service holds no per-call state; each Generate is independent.
type Service struct {
	client Client
	writer *AuditWriter
}

// NewService wires a Client (Ollama in production, Stub in CI) to an
// AuditWriter.
func NewService(client Client, writer *AuditWriter) *Service {
	return &Service{client: client, writer: writer}
}

// GenerateAndRecord runs one bounded generation and persists the
// ai_generations audit row in the caller's tenant context, returning both the
// draft result and the stored record. The order is load-bearing: the draft is
// produced first, then recorded -- a generation that fails never writes a row,
// and a recorded row always reflects an actual generation (R-mitigation).
//
// The context MUST carry a tenant (tenancy.WithTenant). The request's
// mandatory caps (MaxTokens, Timeout) are enforced by the Client before any
// inference (P0-498-6).
func (s *Service) GenerateAndRecord(
	ctx context.Context,
	req GenerateRequest,
	subject string,
) (GenerateResult, dbx.AiGeneration, error) {
	res, err := s.client.Generate(ctx, req)
	if err != nil {
		return GenerateResult{}, dbx.AiGeneration{}, fmt.Errorf("llm: generate: %w", err)
	}

	row, err := s.writer.Write(ctx, Generation{
		Surface:        req.Surface,
		PromptVersion:  req.PromptVersion,
		ModelName:      res.ModelName,
		ModelVersion:   res.ModelVersion,
		ModelProvider:  res.ModelProvider,
		SystemPrompt:   req.SystemPrompt,
		ContextInputs:  req.Context,
		RawDraft:       res.Text,
		SurfaceSubject: subject,
	})
	if err != nil {
		return GenerateResult{}, dbx.AiGeneration{}, fmt.Errorf("llm: record generation: %w", err)
	}
	return res, row, nil
}
