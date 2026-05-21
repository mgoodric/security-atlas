// Slice 173: MCP write tools + HITL approval flow.
//
// Five new tools join the slice-172 read surface:
//
//   - create_risk           — propose a new risk register entry
//   - update_control_state  — propose a control-state override (synthesised
//                             as an evidence record per invariant #2)
//   - push_evidence         — propose an evidence record append
//   - update_risk_treatment — propose a risk treatment + owner change
//   - confirm_write         — operator-driven approval of a pending proposal
//
// Each write tool dispatches to POST /v1/mcp/write-proposals; the platform
// stores the proposal at state=ai_proposed with ai_assisted=true and
// human_approved=false. A separate confirm_write tool (or the web-UI
// approval button) flips the proposal to state=applied; the platform
// runs the canonical Applier and records human_approver in the same
// transaction.
//
// All four write tools share the AI model name + version as constants so
// the per-tool input schema can omit them; the LLM never picks the model
// it claims to be. Operators wanting a different model run a different
// MCP-server build (slice 182 will land the per-tenant override path).
//
// User-Agent on outbound write requests is `(mcp; ai_assisted=write)`
// per slice 173 — the read-tool User-Agent stays `(mcp; ai_assisted=
// read-only)`. Platform-side audit aggregators distinguish the two.

package tools

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/google/uuid"

	"github.com/mgoodric/security-atlas/internal/mcp"
)

// Default AI provenance — replaced at process start by atlas-mcp's
// build env. Keeping defaults here lets unit tests pass without an
// env var fixture.
const (
	DefaultAIModelName    = "atlas-mcp-client"
	DefaultAIModelVersion = "dev"
)

// AIModelName + AIModelVersion are mutable package-level vars so the
// atlas-mcp main can set the active model identity at process start.
// They are read inside each write tool's Handle() so a runtime override
// (e.g., set via cmd/atlas-mcp/main.go from an env var) takes effect
// without re-constructing the tool slice.
var (
	AIModelName    = DefaultAIModelName
	AIModelVersion = DefaultAIModelVersion
)

// proposalCreateReq mirrors the api/mcpwriteproposals createReq wire
// shape. Kept in this package so the write tool can construct it
// without importing the api/* tree (the api/* tree imports this tree —
// a back-import would cycle).
type proposalCreateReq struct {
	ToolName       string          `json:"tool_name"`
	ToolInput      json.RawMessage `json:"tool_input"`
	AIModelName    string          `json:"ai_model_name"`
	AIModelVersion string          `json:"ai_model_version"`
}

// proposalEnvelope is the response shape both POST and GET return.
type proposalEnvelope struct {
	Proposal map[string]any `json:"proposal"`
}

// ===== create_risk =====

const createRiskInputSchema = `{
  "type": "object",
  "additionalProperties": false,
  "required": ["title", "category"],
  "properties": {
    "title": {
      "type": "string",
      "minLength": 1,
      "maxLength": 200,
      "description": "Short risk title."
    },
    "description": {
      "type": "string",
      "maxLength": 4000,
      "description": "Free-text risk narrative."
    },
    "category": {
      "type": "string",
      "description": "Risk category slug (e.g., operational, technical, legal)."
    },
    "methodology": {
      "type": "string",
      "description": "Optional; defaults to nist_800_30. Other valid values: qualitative_5x5, fair."
    },
    "inherent_score": {
      "type": "object",
      "description": "Methodology-shaped scoring envelope; structure depends on methodology."
    },
    "treatment": {
      "type": "string",
      "enum": ["avoid", "accept", "transfer", "mitigate"],
      "description": "Optional; defaults to avoid (safest)."
    },
    "treatment_owner": {
      "type": "string",
      "description": "User/credential id responsible for the treatment plan."
    }
  }
}`

// CreateRiskTool files a create_risk proposal. The proposal sits at
// state=ai_proposed until an approver invokes confirm_write or the
// operator clicks Approve in the UI.
type CreateRiskTool struct {
	client *mcp.Client
}

// NewCreateRisk constructs the tool.
func NewCreateRisk(c *mcp.Client) *CreateRiskTool { return &CreateRiskTool{client: c} }

// Definition implements mcp.Tool.
func (t *CreateRiskTool) Definition() mcp.ToolDefinition {
	return mcp.ToolDefinition{
		Name: "create_risk",
		Description: "Propose a new risk register entry. The proposal is staged at " +
			"state='ai_proposed' until an authorized approver confirms or rejects it. " +
			"Use confirm_write(proposal_id) to commit. No audit-binding artifact is " +
			"published until a human approves.",
		InputSchema: json.RawMessage(createRiskInputSchema),
	}
}

// Handle implements mcp.Tool.
func (t *CreateRiskTool) Handle(ctx context.Context, args json.RawMessage) (any, error) {
	return fileProposal(ctx, t.client, "create_risk", args)
}

// ===== update_control_state =====

const updateControlStateInputSchema = `{
  "type": "object",
  "additionalProperties": false,
  "required": ["control_id", "state"],
  "properties": {
    "control_id": {
      "type": "string",
      "description": "Control UUID to override."
    },
    "state": {
      "type": "string",
      "enum": ["pass", "fail", "na", "inconclusive"],
      "description": "Proposed override state."
    },
    "rationale": {
      "type": "string",
      "maxLength": 4000,
      "description": "Why this override is needed; required by the operator before confirm."
    }
  }
}`

// UpdateControlStateTool files an update_control_state proposal.
type UpdateControlStateTool struct {
	client *mcp.Client
}

// NewUpdateControlState constructs the tool.
func NewUpdateControlState(c *mcp.Client) *UpdateControlStateTool {
	return &UpdateControlStateTool{client: c}
}

// Definition implements mcp.Tool.
func (t *UpdateControlStateTool) Definition() mcp.ToolDefinition {
	return mcp.ToolDefinition{
		Name: "update_control_state",
		Description: "Propose a control-state override (manual.control_state_override.v1 " +
			"evidence record). Constitutional invariant #2 forbids direct writes to " +
			"control_evaluations; this tool routes through evidence so the eval engine " +
			"observes the override on the same pipeline as connector-pushed evidence. " +
			"Proposal sits at state='ai_proposed' until confirmed.",
		InputSchema: json.RawMessage(updateControlStateInputSchema),
	}
}

// Handle implements mcp.Tool.
func (t *UpdateControlStateTool) Handle(ctx context.Context, args json.RawMessage) (any, error) {
	return fileProposal(ctx, t.client, "update_control_state", args)
}

// ===== push_evidence =====

const pushEvidenceInputSchema = `{
  "type": "object",
  "additionalProperties": false,
  "required": ["control_id", "kind", "result"],
  "properties": {
    "control_id": {
      "type": "string",
      "description": "Control UUID this evidence supports."
    },
    "kind": {
      "type": "string",
      "minLength": 1,
      "description": "evidence_kind slug (e.g., aws.s3.bucket_encryption.v1)."
    },
    "result": {
      "type": "string",
      "enum": ["pass", "fail", "na", "inconclusive"]
    },
    "observed_at": {
      "type": "string",
      "format": "date-time",
      "description": "ISO-8601 timestamp; defaults to now if omitted."
    },
    "payload": {
      "type": "object",
      "description": "Evidence payload (structured per evidence_kind schema). Bounded."
    }
  }
}`

// PushEvidenceTool files a push_evidence proposal.
type PushEvidenceTool struct {
	client *mcp.Client
}

// NewPushEvidence constructs the tool.
func NewPushEvidence(c *mcp.Client) *PushEvidenceTool {
	return &PushEvidenceTool{client: c}
}

// Definition implements mcp.Tool.
func (t *PushEvidenceTool) Definition() mcp.ToolDefinition {
	return mcp.ToolDefinition{
		Name: "push_evidence",
		Description: "Propose an evidence record append. Same wire shape as the push SDK " +
			"(slice 003) but mediated by HITL approval because the LLM is the origin. " +
			"Proposal sits at state='ai_proposed' until an approver confirms.",
		InputSchema: json.RawMessage(pushEvidenceInputSchema),
	}
}

// Handle implements mcp.Tool.
func (t *PushEvidenceTool) Handle(ctx context.Context, args json.RawMessage) (any, error) {
	return fileProposal(ctx, t.client, "push_evidence", args)
}

// ===== update_risk_treatment =====

const updateRiskTreatmentInputSchema = `{
  "type": "object",
  "additionalProperties": false,
  "required": ["risk_id", "treatment"],
  "properties": {
    "risk_id": {
      "type": "string",
      "description": "Risk UUID to update."
    },
    "treatment": {
      "type": "string",
      "enum": ["avoid", "accept", "transfer", "mitigate"]
    },
    "treatment_owner": {
      "type": "string",
      "description": "Optional; the user/credential id that owns the new treatment plan."
    }
  }
}`

// UpdateRiskTreatmentTool files an update_risk_treatment proposal.
type UpdateRiskTreatmentTool struct {
	client *mcp.Client
}

// NewUpdateRiskTreatment constructs the tool.
func NewUpdateRiskTreatment(c *mcp.Client) *UpdateRiskTreatmentTool {
	return &UpdateRiskTreatmentTool{client: c}
}

// Definition implements mcp.Tool.
func (t *UpdateRiskTreatmentTool) Definition() mcp.ToolDefinition {
	return mcp.ToolDefinition{
		Name: "update_risk_treatment",
		Description: "Propose a risk treatment change (and optionally a treatment_owner " +
			"reassignment). Proposal sits at state='ai_proposed' until an approver confirms.",
		InputSchema: json.RawMessage(updateRiskTreatmentInputSchema),
	}
}

// Handle implements mcp.Tool.
func (t *UpdateRiskTreatmentTool) Handle(ctx context.Context, args json.RawMessage) (any, error) {
	return fileProposal(ctx, t.client, "update_risk_treatment", args)
}

// ===== confirm_write =====

const confirmWriteInputSchema = `{
  "type": "object",
  "additionalProperties": false,
  "required": ["proposal_id"],
  "properties": {
    "proposal_id": {
      "type": "string",
      "description": "Proposal UUID to confirm. The caller's credential MUST hold the approver role."
    }
  }
}`

type confirmWriteArgs struct {
	ProposalID string `json:"proposal_id"`
}

// ConfirmWriteTool dispatches to POST /v1/mcp/write-proposals/{id}/confirm.
// The platform enforces IsApprover / IsAdmin gating server-side.
type ConfirmWriteTool struct {
	client *mcp.Client
}

// NewConfirmWrite constructs the tool.
func NewConfirmWrite(c *mcp.Client) *ConfirmWriteTool {
	return &ConfirmWriteTool{client: c}
}

// Definition implements mcp.Tool.
func (t *ConfirmWriteTool) Definition() mcp.ToolDefinition {
	return mcp.ToolDefinition{
		Name: "confirm_write",
		Description: "Confirm an ai_proposed write proposal. Requires the caller's credential " +
			"to hold the approver role; the platform runs the canonical Applier inside the " +
			"same transaction as the state flip. Idempotent: a second call on an applied " +
			"proposal returns a 409 conflict.",
		InputSchema: json.RawMessage(confirmWriteInputSchema),
	}
}

// Handle implements mcp.Tool.
func (t *ConfirmWriteTool) Handle(ctx context.Context, args json.RawMessage) (any, error) {
	var in confirmWriteArgs
	if err := strictDecode(args, &in); err != nil {
		return nil, err
	}
	if _, err := parseUUIDArg("proposal_id", in.ProposalID); err != nil {
		return nil, err
	}
	var resp proposalEnvelope
	if err := t.client.PostJSON(ctx,
		"/v1/mcp/write-proposals/"+in.ProposalID+"/confirm", nil, &resp); err != nil {
		return nil, err
	}
	return resp.Proposal, nil
}

// ===== shared file-proposal helper =====

// fileProposal POSTs a draft proposal and returns the proposal envelope.
// All four write tools share this helper; the tool-specific schema is
// enforced via strictDecode + the per-tool inputSchema declaration the
// MCP client consumes.
func fileProposal(ctx context.Context, c *mcp.Client, toolName string, rawArgs json.RawMessage) (any, error) {
	// We let the platform re-validate the tool_input; the local check is
	// "JSON well-formed" + "non-empty for tools that require arguments".
	if len(rawArgs) == 0 {
		return nil, fmt.Errorf("%s requires arguments", toolName)
	}
	// Defense-in-depth: strictDecode against a generic map catches a
	// non-object payload (e.g., LLM sends a bare string). Pure validation
	// — we forward the raw JSON to the platform, not the decoded map.
	var probe map[string]any
	if err := strictDecode(rawArgs, &probe); err != nil {
		return nil, err
	}
	if !json.Valid(rawArgs) {
		return nil, fmt.Errorf("%s: arguments is not valid JSON", toolName)
	}

	req := proposalCreateReq{
		ToolName:       toolName,
		ToolInput:      rawArgs,
		AIModelName:    AIModelName,
		AIModelVersion: AIModelVersion,
	}
	var resp proposalEnvelope
	if err := c.PostJSON(ctx, "/v1/mcp/write-proposals", req, &resp); err != nil {
		return nil, err
	}
	return resp.Proposal, nil
}

// AllWrites returns the five slice-173 write tools wired against the
// given mcp.Client. The order matches mcp.CanonicalWriteToolOrder; the
// reads-vs-writes split lives in tools.AllWithWrites for the top-level
// tools.All caller.
func AllWrites(client *mcp.Client) []mcp.Tool {
	return []mcp.Tool{
		NewCreateRisk(client),
		NewUpdateControlState(client),
		NewPushEvidence(client),
		NewUpdateRiskTreatment(client),
		NewConfirmWrite(client),
	}
}

// AllWithWrites returns the combined slice-172 read tools + slice-173
// write tools. cmd/atlas-mcp/main.go calls this; the older tools.All
// (six read tools) stays unchanged for backwards-compat with any
// out-of-tree consumer.
func AllWithWrites(client *mcp.Client) []mcp.Tool {
	return append(All(client), AllWrites(client)...)
}

// parseProposalID is a tiny defensive helper used by confirm_write.
// Kept as a package-level fn so the tool layer can grow a sibling
// reject_write tool later without re-typing the same one-liner.
//
//nolint:unused // reserved for slice-174 reject_write
func parseProposalID(id string) (uuid.UUID, error) {
	if id == "" {
		return uuid.Nil, fmt.Errorf("proposal_id is required")
	}
	u, err := uuid.Parse(id)
	if err != nil {
		return uuid.Nil, fmt.Errorf("proposal_id must be UUID: %w", err)
	}
	return u, nil
}
