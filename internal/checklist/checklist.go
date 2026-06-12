package checklist

import (
	"errors"
	"time"
)

// promptVersion is this surface's prompt-template version tag, recorded on the
// llm.GenerateRequest + the persisted section + the ai_generations audit row
// (slice-182 schema contract). Bump it when the system prompt or context shape
// changes materially.
const promptVersion = "checklist-v0"

// MaxControls bounds a single generation request (AC-3, D-mitigation,
// P0-471-7). A request whose in-scope control set exceeds this is rejected with
// ErrTooManyControls rather than launching an unbounded job. Sized for the v0
// tracer bullet: one control set for a solo program is well under this.
const MaxControls = 60

// MaxTasksPerControl caps how many task statements the model may emit per
// control (AC-4, D-mitigation, P0-471-7). The model is instructed to this cap;
// the service truncates any over-cap output as belt-and-suspenders.
const MaxTasksPerControl = 5

// MaxSectionTokens bounds one role-section's generation. A section is a handful
// of controls' worth of terse task statements — well under internal/llm's
// MaxTokenBudget (4096). Kept bounded so latency stays bounded (D-mitigation).
const MaxSectionTokens = 1536

// GenerationTimeout caps the wall-clock per role-section generation. Local
// Ollama on commodity hardware answers a bounded prompt in seconds; beyond this
// the section degrades to "generation unavailable" rather than hanging the
// operator (D-mitigation).
const GenerationTimeout = 60 * time.Second

// CitationKind classifies one resolved reference a task item makes. A task may
// cite its control (always), its SCF anchor (when the control links one), and a
// linked policy (when the control links one).
type CitationKind string

const (
	KindControl   CitationKind = "control"
	KindSCFAnchor CitationKind = "scf_anchor"
	KindPolicy    CitationKind = "policy"
)

// Citation is one resolved, tenant-grounded reference a task item makes. For a
// control/policy the ID is the canonical UUID string; for an SCF anchor the ID
// is the SCF id string (e.g. "IAC-06") since anchors are catalog-global, not
// tenant rows — the grounding for an anchor citation is the tenant-owned
// control that carries it (Ref).
type Citation struct {
	Kind CitationKind `json:"kind"`
	ID   string       `json:"id"`
	// Ref is the tenant-owned control id this citation hangs off (the grounding
	// anchor). For a control citation Ref == ID. For an scf_anchor/policy
	// citation Ref is the control that links it, proving the reference is
	// reachable in-tenant.
	Ref string `json:"ref,omitempty"`
}

// ControlInput is one in-scope control fed to a role section's generation: its
// id, text, the deterministic role it was assigned, and the citable references
// (its own id + its SCF id + its linked policy ids), plus whether it has any
// evidence backing (AC-6).
type ControlInput struct {
	ID          string
	Title       string
	Description string
	Role        Role
	SCFID       string   // empty when the control links no SCF anchor
	PolicyIDs   []string // tenant-owned policy ids linked to this control
	HasEvidence bool
}

// Item is one generated, cited task statement in a role section.
type Item struct {
	ItemID      string     `json:"item_id,omitempty"`
	ControlID   string     `json:"control_id"`
	Task        string     `json:"task"`
	Citations   []Citation `json:"citations"`
	NoEvidence  bool       `json:"no_evidence"`
	ControlText string     `json:"control_title,omitempty"`
}

// Section is one role's slice of a generation: the role, its (persisted) id,
// its approval state, and its cited items. The unassigned bucket is a Section
// with Role==RoleUnassigned, AIAssisted==false, carrying the controls that
// matched no role (Items list their control + a "no role assigned" task).
type Section struct {
	SectionID     string `json:"section_id,omitempty"`
	Role          Role   `json:"role"`
	AIAssisted    bool   `json:"ai_assisted"`
	HumanApproved bool   `json:"human_approved"`
	HumanApprover string `json:"human_approver,omitempty"`
	Items         []Item `json:"items"`

	// Suppressed is true when this role's AI section was withheld because a
	// generated citation failed to resolve or the backend was unavailable.
	// Nothing is persisted for a suppressed section (a draft the operator must
	// not see is never written — P0-471-2/P0-471-4). Reason carries the fixed
	// cause.
	Suppressed bool   `json:"suppressed"`
	Reason     string `json:"reason,omitempty"`

	// Model provenance, surfaced for transparency. Present for an AI section.
	ModelName     string `json:"model_name,omitempty"`
	ModelVersion  string `json:"model_version,omitempty"`
	ModelProvider string `json:"model_provider,omitempty"`
	// CloudRouted is true when generation routed to a cloud LLM (renders a
	// banner). Always false in v0 (local Ollama only, P0-471-5).
	CloudRouted bool `json:"cloud_routed"`
}

// Checklist is the result of Service.Generate: the generation id + the
// role-sectioned, cited draft. Every AI section is UNAPPROVED; approval is a
// separate per-section operator action (Service.ApproveSection).
type Checklist struct {
	GenerationID string    `json:"generation_id"`
	Sections     []Section `json:"sections"`
	// CloudRouted is the OR of the sections' routing (banner). Always false v0.
	CloudRouted bool `json:"cloud_routed"`
}

// ApprovedSection is the result of Service.ApproveSection: the now-approved
// section's boundary state, proving human_approved=TRUE with a recorded
// approver.
type ApprovedSection struct {
	SectionID     string `json:"section_id"`
	Role          Role   `json:"role"`
	HumanApproved bool   `json:"human_approved"`
	HumanApprover string `json:"human_approver"`
}

// Suppression / outcome reasons (fixed vocabulary — safe to render; never carry
// model text or backend error detail, slice-367 leak discipline).
const (
	ReasonGenerationUnavailable = "generation_unavailable"
	ReasonUnresolvedCitation    = "unresolved_citation"
	ReasonNoCitations           = "no_citations"
	ReasonNoTasks               = "no_tasks"
)

// Sentinel errors. Callers match these with errors.Is.
var (
	// ErrTooManyControls is returned by Generate when the in-scope control set
	// exceeds MaxControls (AC-3, P0-471-7).
	ErrTooManyControls = errors.New("checklist: too many controls for one generation")

	// ErrNoControls is returned when the tenant has no active controls to build
	// a checklist from (nothing to ground a generation).
	ErrNoControls = errors.New("checklist: no in-scope controls")

	// ErrSectionNotFound is returned by ApproveSection when the id names no
	// tenant-owned, AI-assisted, unapproved section.
	ErrSectionNotFound = errors.New("checklist: approvable section not found")

	// ErrApproverRequired is returned by ApproveSection when the approver is
	// blank — the Go mirror of the DB CHECK (P0-471-6).
	ErrApproverRequired = errors.New("checklist: approval requires a human_approver")
)
