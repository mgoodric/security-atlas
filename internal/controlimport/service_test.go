package controlimport

import (
	"context"
	"errors"
	"testing"

	"github.com/google/uuid"

	"github.com/mgoodric/security-atlas/internal/crosswalktier"
	"github.com/mgoodric/security-atlas/internal/llm"
	"github.com/mgoodric/security-atlas/internal/tenancy"
)

type fakeRetriever struct {
	candidatesByTenant map[string][]AnchorCandidate
	resolve            map[string]AnchorCandidate
	seenTenants        []string
}

func (f *fakeRetriever) RetrieveAnchors(ctx context.Context, _ StagedControl) ([]AnchorCandidate, error) {
	tenant, err := tenancy.TenantFromContext(ctx)
	if err != nil {
		return nil, err
	}
	f.seenTenants = append(f.seenTenants, tenant)
	return f.candidatesByTenant[tenant], nil
}

func (f *fakeRetriever) ResolveAnchor(_ context.Context, scfID string) (AnchorCandidate, bool, error) {
	c, ok := f.resolve[scfID]
	return c, ok, nil
}

type fakeStore struct {
	proposals []Proposal
	approved  []ApprovedMapping
	rejected  []string
}

func (s *fakeStore) PersistProposal(_ context.Context, p Proposal) (string, error) {
	id := NewProposalID()
	p.ID = id
	s.proposals = append(s.proposals, p)
	return id, nil
}

func (s *fakeStore) ApproveProposal(_ context.Context, proposalID, humanApprover string, editedSCFID *string) (ApprovedMapping, error) {
	if humanApprover == "" {
		return ApprovedMapping{}, ErrApproverRequired
	}
	scfID := "IAC-06"
	if editedSCFID != nil {
		scfID = *editedSCFID
	}
	out := ApprovedMapping{
		ProposalID:    proposalID,
		ControlID:     "ACME-1",
		SCFID:         scfID,
		AIAssisted:    true,
		HumanApprover: humanApprover,
		MappingTier:   crosswalktier.TierDraft,
	}
	s.approved = append(s.approved, out)
	return out, nil
}

func (s *fakeStore) RejectProposal(_ context.Context, proposalID, humanApprover, _ string) error {
	if humanApprover == "" {
		return ErrApproverRequired
	}
	s.rejected = append(s.rejected, proposalID)
	return nil
}

func tenantCtx(t *testing.T, tenant string) context.Context {
	t.Helper()
	ctx, err := tenancy.WithTenant(context.Background(), tenant)
	if err != nil {
		t.Fatalf("WithTenant: %v", err)
	}
	return ctx
}

func testControl() StagedControl {
	return StagedControl{
		RowNumber:    2,
		ExternalID:   "ACME-1",
		Title:        "Multifactor authentication",
		Description:  "Privileged and remote users must authenticate with MFA.",
		SourceFormat: "csv",
	}
}

func testService(result string) (*Service, *fakeRetriever, *fakeStore) {
	tenant := "00000000-0000-0000-0000-000000000001"
	anchor := AnchorCandidate{
		SCFID:       "IAC-06",
		Title:       "Multi-Factor Authentication",
		Description: "Multi-factor authentication is required for privileged access.",
	}
	retriever := &fakeRetriever{
		candidatesByTenant: map[string][]AnchorCandidate{tenant: {anchor}},
		resolve:            map[string]AnchorCandidate{anchor.SCFID: anchor},
	}
	store := &fakeStore{}
	client := &llm.StubClient{Result: llm.GenerateResult{
		Text:          result,
		ModelName:     "llama3.1",
		ModelVersion:  "8b",
		ModelProvider: "ollama-local",
	}}
	return NewService(retriever, client, store), retriever, store
}

func TestSuggestPersistsAIProposedSCFMappingWithResolvedCitation(t *testing.T) {
	svc, _, store := testService(`{"scf_id":"IAC-06","confidence":0.86,"rationale":"The imported MFA control aligns to Multi-Factor Authentication."}`)
	got, err := svc.Suggest(tenantCtx(t, "00000000-0000-0000-0000-000000000001"), testControl())
	if err != nil {
		t.Fatalf("Suggest: %v", err)
	}
	if got.ID == "" || got.SuggestedSCFID != "IAC-06" || got.Citation == nil {
		t.Fatalf("proposal missing match/citation: %+v", got)
	}
	if got.HumanApproved || got.HumanApprover != "" {
		t.Fatalf("proposal auto-approved: %+v", got)
	}
	if !got.AIAssisted || got.MappingTier != crosswalktier.TierDraft {
		t.Fatalf("proposal boundary/tier mismatch: %+v", got)
	}
	if got.CloudRouted {
		t.Fatalf("local ollama proposal should not set cloud banner: %+v", got)
	}
	if len(store.proposals) != 1 {
		t.Fatalf("persisted proposals = %d, want 1", len(store.proposals))
	}
}

func TestSuggestSuppressesUnresolvedCitation(t *testing.T) {
	svc, _, _ := testService(`{"scf_id":"DNE-99","confidence":0.91,"rationale":"Invented."}`)
	got, err := svc.Suggest(tenantCtx(t, "00000000-0000-0000-0000-000000000001"), testControl())
	if err != nil {
		t.Fatalf("Suggest: %v", err)
	}
	if !got.Suppressed || got.SuggestedSCFID != "" || got.Citation != nil {
		t.Fatalf("unresolved citation was not suppressed: %+v", got)
	}
}

func TestSuggestLeavesLowConfidenceUnmapped(t *testing.T) {
	svc, _, _ := testService(`{"scf_id":"UNMAPPED","confidence":0.12,"rationale":"No SCF candidate matches."}`)
	got, err := svc.Suggest(tenantCtx(t, "00000000-0000-0000-0000-000000000001"), testControl())
	if err != nil {
		t.Fatalf("Suggest: %v", err)
	}
	if !got.InsufficientMatch || got.SuggestedSCFID != "" {
		t.Fatalf("low confidence should stay unmapped: %+v", got)
	}
}

func TestApproveRequiresHumanApprover(t *testing.T) {
	svc, _, store := testService(`{"scf_id":"IAC-06","confidence":0.86,"rationale":"MFA."}`)
	_, err := svc.Approve(context.Background(), ApproveParams{ProposalID: "p1"})
	if !errors.Is(err, ErrApproverRequired) {
		t.Fatalf("want ErrApproverRequired, got %v", err)
	}
	if len(store.approved) != 0 {
		t.Fatalf("blank approver wrote canonical mapping")
	}
	got, err := svc.Approve(context.Background(), ApproveParams{ProposalID: "p1", HumanApprover: "user:security-lead"})
	if err != nil {
		t.Fatalf("Approve: %v", err)
	}
	if got.HumanApprover != "user:security-lead" || got.MappingTier != crosswalktier.TierDraft {
		t.Fatalf("approved mapping mismatch: %+v", got)
	}
}

func TestSuggestTenantIsolationInputs(t *testing.T) {
	tenantA := uuid.MustParse("00000000-0000-0000-0000-00000000000a").String()
	tenantB := uuid.MustParse("00000000-0000-0000-0000-00000000000b").String()
	anchorA := AnchorCandidate{SCFID: "IAC-06", Title: "Multi-Factor Authentication", Description: "MFA for privileged access"}
	anchorB := AnchorCandidate{SCFID: "CFG-01", Title: "MFA configuration baseline", Description: "Configuration baselines mention MFA but are not IAC-06"}
	retriever := &fakeRetriever{
		candidatesByTenant: map[string][]AnchorCandidate{
			tenantA: {anchorA},
			tenantB: {anchorB},
		},
		resolve: map[string]AnchorCandidate{anchorA.SCFID: anchorA, anchorB.SCFID: anchorB},
	}
	store := &fakeStore{}
	client := &llm.StubClient{Result: llm.GenerateResult{
		Text:          `{"scf_id":"IAC-06","confidence":0.8,"rationale":"MFA."}`,
		ModelName:     "anthropic",
		ModelVersion:  "claude",
		ModelProvider: "anthropic",
	}}
	svc := NewService(retriever, client, store)
	gotA, err := svc.Suggest(tenantCtx(t, tenantA), testControl())
	if err != nil {
		t.Fatalf("Suggest A: %v", err)
	}
	if gotA.SuggestedSCFID != "IAC-06" || !gotA.CloudRouted {
		t.Fatalf("tenant A/cloud banner mismatch: %+v", gotA)
	}
	gotB, err := svc.Suggest(tenantCtx(t, tenantB), testControl())
	if err != nil {
		t.Fatalf("Suggest B: %v", err)
	}
	if !gotB.Suppressed {
		t.Fatalf("tenant B should not accept tenant A candidate/citation: %+v", gotB)
	}
	if len(retriever.seenTenants) != 2 || retriever.seenTenants[0] != tenantA || retriever.seenTenants[1] != tenantB {
		t.Fatalf("retriever did not receive tenant-scoped contexts: %v", retriever.seenTenants)
	}
}
