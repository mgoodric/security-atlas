package controlimport

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"sort"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/mgoodric/security-atlas/internal/crosswalktier"
	"github.com/mgoodric/security-atlas/internal/llm"
	"github.com/mgoodric/security-atlas/internal/tenancy"
)

const (
	promptVersion     = "controlimport-scf-match-v0"
	maxMatchTokens    = 768
	generationTimeout = 45 * time.Second
	maxAnchorCands    = 8
	minConfidence     = 0.35
)

var (
	ErrNoAnchorCitation  = errors.New("controlimport: suggestion has no SCF citation")
	ErrUnresolvedAnchor  = errors.New("controlimport: suggestion cites unknown SCF anchor")
	ErrApproverRequired  = errors.New("controlimport: approval requires a human_approver")
	ErrProposalNotFound  = errors.New("controlimport: proposal not found")
	ErrControlNotMatched = errors.New("controlimport: control has no match to approve")
)

// AnchorCandidate is one real SCF control the matcher may cite. The production
// retriever should obtain these from the SCF catalog and not from tenant data.
type AnchorCandidate struct {
	SCFID       string
	Title       string
	Description string
}

// AnchorRetriever retrieves and resolves SCF anchors for the current tenant's
// import session. Implementations must apply the caller's tenant context to
// staged-control reads; SCF anchor catalog reads are global reference data.
type AnchorRetriever interface {
	RetrieveAnchors(ctx context.Context, control StagedControl) ([]AnchorCandidate, error)
	ResolveAnchor(ctx context.Context, scfID string) (AnchorCandidate, bool, error)
}

// MappingStore persists proposals and writes canonical mappings after human
// approval. Implementations must apply the caller's tenant context to proposal
// reads and approval/rejection writes. Approval writes must land no higher than
// community/draft governance unless a separate review workflow promotes them.
type MappingStore interface {
	PersistProposal(ctx context.Context, proposal Proposal) (string, error)
	ApproveProposal(ctx context.Context, proposalID, humanApprover string, editedSCFID *string) (ApprovedMapping, error)
	RejectProposal(ctx context.Context, proposalID, humanApprover, reason string) error
}

type Service struct {
	retriever AnchorRetriever
	client    llm.Client
	store     MappingStore
}

func NewService(retriever AnchorRetriever, client llm.Client, store MappingStore) *Service {
	return &Service{retriever: retriever, client: client, store: store}
}

type Proposal struct {
	ID                  string             `json:"id,omitempty"`
	Control             StagedControl      `json:"control"`
	SuggestedSCFID      string             `json:"suggested_scf_id,omitempty"`
	Confidence          float64            `json:"confidence,omitempty"`
	Rationale           string             `json:"rationale,omitempty"`
	Citation            *AnchorCitation    `json:"citation,omitempty"`
	AIAssisted          bool               `json:"ai_assisted"`
	HumanApproved       bool               `json:"human_approved"`
	HumanApprover       string             `json:"human_approver,omitempty"`
	MappingTier         crosswalktier.Tier `json:"mapping_tier"`
	InsufficientMatch   bool               `json:"insufficient_match"`
	Suppressed          bool               `json:"suppressed"`
	Reason              string             `json:"reason,omitempty"`
	ModelName           string             `json:"model_name,omitempty"`
	ModelVersion        string             `json:"model_version,omitempty"`
	ModelProvider       string             `json:"model_provider,omitempty"`
	CloudRouted         bool               `json:"cloud_routed"`
	GenerationContext   map[string]any     `json:"generation_context,omitempty"`
	ResolvedAnchorTitle string             `json:"resolved_anchor_title,omitempty"`
}

type AnchorCitation struct {
	SCFID string `json:"scf_id"`
	Title string `json:"title"`
}

type ApprovedMapping struct {
	ProposalID    string             `json:"proposal_id"`
	ControlID     string             `json:"control_id"`
	SCFID         string             `json:"scf_id"`
	AIAssisted    bool               `json:"ai_assisted"`
	HumanApprover string             `json:"human_approver"`
	MappingTier   crosswalktier.Tier `json:"mapping_tier"`
}

// Suggest proposes one SCF anchor for one imported control. It persists only
// an unapproved ai_proposed mapping, never a canonical mapping.
func (s *Service) Suggest(ctx context.Context, control StagedControl) (Proposal, error) {
	if _, err := tenancy.TenantFromContext(ctx); err != nil {
		return Proposal{}, err
	}
	candidates, err := s.retriever.RetrieveAnchors(ctx, control)
	if err != nil {
		return Proposal{}, fmt.Errorf("controlimport: retrieve anchors: %w", err)
	}
	candidates = rankAnchorCandidates(control, candidates, maxAnchorCands)
	base := Proposal{
		Control:     control,
		AIAssisted:  true,
		MappingTier: crosswalktier.TierDraft,
	}
	if len(candidates) == 0 {
		base.InsufficientMatch = true
		base.Reason = "no_candidate_anchors"
		return s.persist(ctx, base)
	}

	res, err := s.client.Generate(ctx, llm.GenerateRequest{
		Surface:       llm.SurfaceQuestionnaire,
		PromptVersion: promptVersion,
		SystemPrompt:  buildMatchPrompt(control, candidates),
		Context:       generationContext(control, candidates),
		MaxTokens:     maxMatchTokens,
		Timeout:       generationTimeout,
	})
	if err != nil {
		base.Suppressed = true
		base.Reason = "generation_unavailable"
		return s.persist(ctx, base)
	}
	base.ModelName = res.ModelName
	base.ModelVersion = res.ModelVersion
	base.ModelProvider = res.ModelProvider
	base.CloudRouted = isCloudProvider(res.ModelProvider)
	base.GenerationContext = generationContext(control, candidates)

	match, err := parseMatchJSON(res.Text)
	if err != nil {
		base.Suppressed = true
		base.Reason = "malformed_generation"
		return s.persist(ctx, base)
	}
	if strings.EqualFold(match.SCFID, "UNMAPPED") || match.Confidence < minConfidence {
		base.InsufficientMatch = true
		base.Reason = "low_confidence"
		base.Confidence = clampConfidence(match.Confidence)
		base.Rationale = strings.TrimSpace(match.Rationale)
		return s.persist(ctx, base)
	}
	if !candidateContains(candidates, match.SCFID) {
		base.Suppressed = true
		base.Reason = ErrUnresolvedAnchor.Error()
		return s.persist(ctx, base)
	}
	anchor, ok, err := s.retriever.ResolveAnchor(ctx, match.SCFID)
	if err != nil {
		return Proposal{}, fmt.Errorf("controlimport: resolve anchor: %w", err)
	}
	if !ok {
		base.Suppressed = true
		base.Reason = ErrUnresolvedAnchor.Error()
		return s.persist(ctx, base)
	}
	base.SuggestedSCFID = anchor.SCFID
	base.Confidence = clampConfidence(match.Confidence)
	base.Rationale = strings.TrimSpace(match.Rationale)
	base.Citation = &AnchorCitation{SCFID: anchor.SCFID, Title: anchor.Title}
	base.ResolvedAnchorTitle = anchor.Title
	return s.persist(ctx, base)
}

func (s *Service) persist(ctx context.Context, p Proposal) (Proposal, error) {
	id, err := s.store.PersistProposal(ctx, p)
	if err != nil {
		return Proposal{}, fmt.Errorf("controlimport: persist proposal: %w", err)
	}
	p.ID = id
	return p, nil
}

type ApproveParams struct {
	ProposalID    string
	HumanApprover string
	EditedSCFID   *string
}

// Approve is the one-click human approval path. It is the only method in this
// package that asks the MappingStore to write a canonical mapping.
func (s *Service) Approve(ctx context.Context, p ApproveParams) (ApprovedMapping, error) {
	if _, err := tenancy.TenantFromContext(ctx); err != nil {
		return ApprovedMapping{}, err
	}
	if strings.TrimSpace(p.HumanApprover) == "" {
		return ApprovedMapping{}, ErrApproverRequired
	}
	editedSCFID := p.EditedSCFID
	if p.EditedSCFID != nil {
		anchor, ok, err := s.retriever.ResolveAnchor(ctx, *p.EditedSCFID)
		if err != nil {
			return ApprovedMapping{}, fmt.Errorf("controlimport: resolve edited anchor: %w", err)
		}
		if !ok {
			return ApprovedMapping{}, ErrUnresolvedAnchor
		}
		editedSCFID = &anchor.SCFID
	}
	return s.store.ApproveProposal(ctx, p.ProposalID, p.HumanApprover, editedSCFID)
}

func (s *Service) Reject(ctx context.Context, proposalID, humanApprover, reason string) error {
	if _, err := tenancy.TenantFromContext(ctx); err != nil {
		return err
	}
	if strings.TrimSpace(humanApprover) == "" {
		return ErrApproverRequired
	}
	return s.store.RejectProposal(ctx, proposalID, humanApprover, reason)
}

type modelMatch struct {
	SCFID      string  `json:"scf_id"`
	Confidence float64 `json:"confidence"`
	Rationale  string  `json:"rationale"`
}

func parseMatchJSON(text string) (modelMatch, error) {
	var m modelMatch
	if err := json.Unmarshal([]byte(strings.TrimSpace(text)), &m); err != nil {
		return modelMatch{}, err
	}
	m.SCFID = strings.TrimSpace(m.SCFID)
	if m.SCFID == "" {
		return modelMatch{}, ErrNoAnchorCitation
	}
	return m, nil
}

func buildMatchPrompt(control StagedControl, candidates []AnchorCandidate) string {
	var b strings.Builder
	b.WriteString(`You suggest the best Secure Controls Framework (SCF) anchor for ONE imported control. The suggestion is a draft: a human operator must approve it before it becomes canonical.

Rules:
1. Choose only from the SCF candidate list below.
2. If no candidate is a real match, return scf_id "UNMAPPED" with confidence below 0.35.
3. Do not invent SCF ids.
4. Return only compact JSON: {"scf_id":"IAC-06","confidence":0.82,"rationale":"short rationale citing the matched SCF control title"}.

Imported control:
`)
	fmt.Fprintf(&b, "id: %s\n", oneLine(control.ExternalID))
	fmt.Fprintf(&b, "title: %s\n", oneLine(control.Title))
	fmt.Fprintf(&b, "description: %s\n\n", oneLine(control.Description))
	b.WriteString("SCF candidates:\n")
	for _, c := range candidates {
		fmt.Fprintf(&b, "- %s: %s\n", c.SCFID, oneLine(c.Title))
		if c.Description != "" {
			fmt.Fprintf(&b, "  description: %s\n", oneLine(c.Description))
		}
	}
	return b.String()
}

func generationContext(control StagedControl, candidates []AnchorCandidate) map[string]any {
	ids := make([]string, 0, len(candidates))
	for _, c := range candidates {
		ids = append(ids, c.SCFID)
	}
	return map[string]any{
		"imported_control_id": control.ExternalID,
		"candidate_scf_ids":   ids,
	}
}

func rankAnchorCandidates(control StagedControl, candidates []AnchorCandidate, limit int) []AnchorCandidate {
	keywords := keywordsFrom(control.ExternalID + " " + control.Title + " " + control.Description)
	type scored struct {
		c     AnchorCandidate
		score int
	}
	ranked := make([]scored, 0, len(candidates))
	for _, c := range candidates {
		hay := strings.ToLower(c.SCFID + " " + c.Title + " " + c.Description)
		score := 0
		for _, kw := range keywords {
			if strings.Contains(hay, kw) {
				score++
			}
		}
		if score > 0 {
			ranked = append(ranked, scored{c: c, score: score})
		}
	}
	sort.SliceStable(ranked, func(i, j int) bool {
		if ranked[i].score != ranked[j].score {
			return ranked[i].score > ranked[j].score
		}
		return ranked[i].c.SCFID < ranked[j].c.SCFID
	})
	if limit > 0 && len(ranked) > limit {
		ranked = ranked[:limit]
	}
	out := make([]AnchorCandidate, 0, len(ranked))
	for _, s := range ranked {
		out = append(out, s.c)
	}
	return out
}

func keywordsFrom(text string) []string {
	fields := strings.FieldsFunc(strings.ToLower(text), func(r rune) bool {
		return !isLowerAlphaNumeric(r)
	})
	seen := make(map[string]bool, len(fields))
	out := make([]string, 0, len(fields))
	for _, f := range fields {
		if len(f) < 3 || stopwords[f] || seen[f] {
			continue
		}
		seen[f] = true
		out = append(out, f)
	}
	return out
}

func isLowerAlphaNumeric(r rune) bool {
	return r >= 'a' && r <= 'z' || r >= '0' && r <= '9'
}

var stopwords = map[string]bool{
	"the": true, "and": true, "for": true, "with": true, "that": true,
	"this": true, "control": true, "controls": true, "policy": true,
}

func candidateContains(candidates []AnchorCandidate, scfID string) bool {
	for _, c := range candidates {
		if c.SCFID == scfID {
			return true
		}
	}
	return false
}

func clampConfidence(v float64) float64 {
	if math.IsNaN(v) || math.IsInf(v, 0) {
		return 0
	}
	if v < 0 {
		return 0
	}
	if v > 1 {
		return 1
	}
	return v
}

func oneLine(s string) string { return strings.Join(strings.Fields(s), " ") }

func isCloudProvider(provider string) bool {
	switch strings.ToLower(provider) {
	case "", "ollama", "ollama-local", "local", "stub":
		return false
	default:
		return true
	}
}

func NewProposalID() string { return uuid.NewString() }
