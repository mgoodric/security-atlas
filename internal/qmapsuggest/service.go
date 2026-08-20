package qmapsuggest

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/google/uuid"

	"github.com/mgoodric/security-atlas/internal/db/dbx"
	"github.com/mgoodric/security-atlas/internal/llm"
)

type Reader interface {
	QuestionTextForMapping(ctx context.Context, questionID uuid.UUID) (string, error)
	RetrieveCandidates(ctx context.Context, keywords []string) ([]Candidate, error)
	ResolveAnchor(ctx context.Context, scfID string) (Candidate, bool, error)
}

type ProposalStore interface {
	PersistProposal(ctx context.Context, questionID uuid.UUID, anchor Candidate, rationale string, candidateIDs []string, prov Provenance) (string, error)
	Approve(ctx context.Context, proposalID uuid.UUID, approver string) (ApprovedProposal, error)
	Reject(ctx context.Context, proposalID uuid.UUID, actor string) (RejectedProposal, error)
	RecordSuppression(ctx context.Context, questionID uuid.UUID, actor, reason string, prov Provenance) error
}

type AuditSink interface {
	Write(ctx context.Context, g llm.Generation) (dbx.AiGeneration, error)
}

type Service struct {
	reader Reader
	client llm.Client
	store  ProposalStore
	audit  AuditSink
}

func NewService(reader Reader, client llm.Client, store ProposalStore, audit AuditSink) *Service {
	return &Service{reader: reader, client: client, store: store, audit: audit}
}

type SuggestParams struct {
	QuestionID uuid.UUID
	Actor      string
}

func (s *Service) Suggest(ctx context.Context, p SuggestParams) (Proposal, error) {
	out := Proposal{QuestionID: p.QuestionID.String(), PromptVersion: promptVersion}

	questionText, err := s.reader.QuestionTextForMapping(ctx, p.QuestionID)
	if err != nil {
		return Proposal{}, err
	}
	keywords := keywordsFrom(questionText)
	cands, err := s.reader.RetrieveCandidates(ctx, keywords)
	if err != nil {
		return Proposal{}, fmt.Errorf("qmapsuggest: retrieve: %w", err)
	}
	ranked := rankCandidates(cands, keywords, maxCandidates)
	if len(ranked) == 0 {
		prov := Provenance{PromptVersion: promptVersion, CreatedBy: p.Actor}
		if err := s.store.RecordSuppression(ctx, p.QuestionID, p.Actor, ReasonNoCandidates, prov); err != nil {
			return Proposal{}, fmt.Errorf("qmapsuggest: audit no-candidates suppression: %w", err)
		}
		out.ManualOnly = true
		out.Reason = ReasonNoCandidates
		return out, nil
	}

	fullPrompt := systemPrompt + "\n\n" + buildPrompt(questionText, ranked)
	ctxInputs := candidateContext(questionText, ranked)
	res, err := s.client.Generate(ctx, llm.GenerateRequest{
		Surface:       llm.SurfaceQuestionnaire,
		PromptVersion: promptVersion,
		SystemPrompt:  fullPrompt,
		Context:       ctxInputs,
		MaxTokens:     MaxMappingTokens,
		Timeout:       GenerationTimeout,
	})
	if err != nil {
		prov := Provenance{PromptVersion: promptVersion, CreatedBy: p.Actor}
		if err := s.store.RecordSuppression(ctx, p.QuestionID, p.Actor, ReasonGenerationUnavailable, prov); err != nil {
			return Proposal{}, fmt.Errorf("qmapsuggest: audit generation-unavailable suppression: %w", err)
		}
		out.Suppressed = true
		out.Reason = ReasonGenerationUnavailable
		return out, nil
	}

	out.ModelName = res.ModelName
	out.ModelVersion = res.ModelVersion
	out.ModelProvider = res.ModelProvider
	out.CloudRouted = isCloudProvider(res.ModelProvider)

	prov := Provenance{
		PromptVersion: promptVersion,
		ModelName:     res.ModelName,
		ModelVersion:  res.ModelVersion,
		ModelProvider: res.ModelProvider,
		CreatedBy:     p.Actor,
	}
	if s.audit != nil {
		if _, err := s.audit.Write(ctx, llm.Generation{
			Surface:        llm.SurfaceQuestionnaire,
			PromptVersion:  promptVersion,
			ModelName:      res.ModelName,
			ModelVersion:   res.ModelVersion,
			ModelProvider:  res.ModelProvider,
			SystemPrompt:   fullPrompt,
			ContextInputs:  ctxInputs,
			RawDraft:       res.Text,
			SurfaceSubject: p.QuestionID.String(),
		}); err != nil {
			return Proposal{}, fmt.Errorf("qmapsuggest: audit generation: %w", err)
		}
	}

	pick, ok := parsePick(res.Text)
	if !ok {
		if err := s.store.RecordSuppression(ctx, p.QuestionID, p.Actor, ReasonInvalidModelResponse, prov); err != nil {
			return Proposal{}, fmt.Errorf("qmapsuggest: audit invalid-response suppression: %w", err)
		}
		out.Suppressed = true
		out.Reason = ReasonInvalidModelResponse
		return out, nil
	}
	grounded, ok := candidateBySCFID(ranked, pick.SCFID)
	if !ok {
		if err := s.store.RecordSuppression(ctx, p.QuestionID, p.Actor, ReasonOutOfGrounding, prov); err != nil {
			return Proposal{}, fmt.Errorf("qmapsuggest: audit out-of-grounding suppression: %w", err)
		}
		out.Suppressed = true
		out.Reason = ReasonOutOfGrounding
		return out, nil
	}
	resolved, ok, err := s.reader.ResolveAnchor(ctx, pick.SCFID)
	if err != nil {
		return Proposal{}, fmt.Errorf("qmapsuggest: resolve anchor: %w", err)
	}
	if !ok || resolved.AnchorUUID == "" {
		if err := s.store.RecordSuppression(ctx, p.QuestionID, p.Actor, ReasonUnknownCatalogAnchor, prov); err != nil {
			return Proposal{}, fmt.Errorf("qmapsuggest: audit unknown-anchor suppression: %w", err)
		}
		out.Suppressed = true
		out.Reason = ReasonUnknownCatalogAnchor
		return out, nil
	}
	// Preserve the grounded title/excerpt shown to the model, but require the
	// real catalog row to exist before persistence.
	grounded.AnchorUUID = resolved.AnchorUUID
	grounded.SCFID = resolved.SCFID

	proposalID, err := s.store.PersistProposal(ctx, p.QuestionID, grounded, pick.Rationale, candidateIDs(ranked), prov)
	if err != nil {
		return Proposal{}, fmt.Errorf("qmapsuggest: persist: %w", err)
	}
	out.ProposalID = proposalID
	out.SCFAnchorID = grounded.SCFID
	out.SCFAnchorUUID = grounded.AnchorUUID
	out.Title = grounded.Title
	out.Rationale = pick.Rationale
	return out, nil
}

func (s *Service) Approve(ctx context.Context, proposalID uuid.UUID, approver string) (ApprovedProposal, error) {
	if strings.TrimSpace(approver) == "" {
		return ApprovedProposal{}, ErrApproverRequired
	}
	if err := llm.EnforceApproval(llm.ApprovalState{
		AIAssisted:    true,
		HumanApproved: true,
		HumanApprover: approver,
	}); err != nil {
		return ApprovedProposal{}, ErrApproverRequired
	}
	return s.store.Approve(ctx, proposalID, approver)
}

func (s *Service) Reject(ctx context.Context, proposalID uuid.UUID, actor string) (RejectedProposal, error) {
	return s.store.Reject(ctx, proposalID, actor)
}

type modelPick struct {
	SCFID     string `json:"scf_id"`
	Rationale string `json:"rationale"`
}

func parsePick(raw string) (modelPick, bool) {
	var p modelPick
	if err := json.Unmarshal([]byte(strings.TrimSpace(raw)), &p); err != nil {
		return modelPick{}, false
	}
	p.SCFID = strings.TrimSpace(p.SCFID)
	p.Rationale = oneLine(p.Rationale)
	if p.SCFID == "" || p.Rationale == "" {
		return modelPick{}, false
	}
	return p, true
}

func candidateBySCFID(cands []Candidate, scfID string) (Candidate, bool) {
	for _, c := range cands {
		if c.SCFID == scfID {
			return c, true
		}
	}
	return Candidate{}, false
}

func candidateIDs(cands []Candidate) []string {
	out := make([]string, 0, len(cands))
	for _, c := range cands {
		out = append(out, c.SCFID)
	}
	return out
}

func isCloudProvider(provider string) bool {
	switch strings.ToLower(provider) {
	case "", "ollama", "ollama-local", "local", "stub":
		return false
	default:
		return true
	}
}
