// Package qmapsuggest implements slice 755: AI-suggested SCF mappings for
// questionnaire questions that are still in needs_mapping state.
package qmapsuggest

import (
	"errors"
	"time"
)

const (
	promptVersion     = "qmapsuggest-v0"
	MaxMappingTokens  = 256
	GenerationTimeout = 45 * time.Second
	maxCandidates     = 8
)

type Candidate struct {
	AnchorUUID string `json:"anchor_uuid"`
	SCFID      string `json:"scf_id"`
	Title      string `json:"title"`
	Excerpt    string `json:"excerpt"`
}

type Proposal struct {
	ProposalID    string `json:"proposal_id,omitempty"`
	QuestionID    string `json:"question_id"`
	SCFAnchorID   string `json:"scf_anchor_id,omitempty"`
	SCFAnchorUUID string `json:"scf_anchor_uuid,omitempty"`
	Title         string `json:"title,omitempty"`
	Rationale     string `json:"rationale,omitempty"`

	Suppressed bool   `json:"suppressed"`
	Reason     string `json:"reason,omitempty"`
	ManualOnly bool   `json:"manual_only"`

	ModelName     string `json:"model_name,omitempty"`
	ModelVersion  string `json:"model_version,omitempty"`
	ModelProvider string `json:"model_provider,omitempty"`
	CloudRouted   bool   `json:"cloud_routed"`
	PromptVersion string `json:"prompt_version,omitempty"`
}

type ApprovedProposal struct {
	ProposalID    string `json:"proposal_id"`
	QuestionID    string `json:"question_id"`
	SCFAnchorID   string `json:"scf_anchor_id"`
	HumanApproved bool   `json:"human_approved"`
	HumanApprover string `json:"human_approver"`
	NeedsMapping  bool   `json:"needs_mapping"`
}

type RejectedProposal struct {
	ProposalID string `json:"proposal_id"`
	QuestionID string `json:"question_id"`
	Status     string `json:"status"`
}

type Provenance struct {
	PromptVersion string
	ModelName     string
	ModelVersion  string
	ModelProvider string
	CreatedBy     string
}

const (
	ReasonNoCandidates          = "no_candidates"
	ReasonGenerationUnavailable = "generation_unavailable"
	ReasonInvalidModelResponse  = "invalid_model_response"
	ReasonOutOfGrounding        = "out_of_grounding"
	ReasonUnknownCatalogAnchor  = "unknown_catalog_anchor"
)

var (
	ErrQuestionNotFound  = errors.New("qmapsuggest: needs_mapping question not found")
	ErrProposalNotFound  = errors.New("qmapsuggest: mapping proposal not found")
	ErrApproverRequired  = errors.New("qmapsuggest: approval requires a human_approver")
	ErrQuestionCanonical = errors.New("qmapsuggest: question is already mapped")
)
