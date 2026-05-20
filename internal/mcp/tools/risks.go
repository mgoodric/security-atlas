// list_risks / get_risk tools — wrap GET /v1/risks + GET /v1/risks/{id}.
// Slice 172 AC-8 + AC-9.
//
// Both tools mirror the canonical risk wire shape from
// internal/api/risks/handlers.go (slices 019 + 020 + 053 + 066 + 067).
// We carry every documented field; new fields the platform adds in a
// future slice flow through transparently as long as the JSON name
// matches (encoding/json's permissive default — we DO NOT
// DisallowUnknownFields on platform responses; only on LLM-supplied
// inputs).

package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"net/url"
	"time"

	"github.com/mgoodric/security-atlas/internal/mcp"
)

// ===== list_risks =====

const listRisksInputSchema = `{
  "type": "object",
  "additionalProperties": false,
  "properties": {
    "scope": {
      "type": "string",
      "description": "Scope cell filter (NOT YET SUPPORTED in v1; the platform /v1/risks endpoint does not accept a scope query param yet)."
    },
    "status": {
      "type": "string",
      "description": "Filter by treatment status: avoid | accept | transfer | mitigate. Optional; default returns all treatments."
    },
    "category": {
      "type": "string",
      "description": "Filter by risk category (free-form string matching the platform's risk_categories list). Optional."
    },
    "limit": {
      "type": "integer",
      "minimum": 1,
      "maximum": 500,
      "description": "Result cap; default 100, max 500."
    }
  }
}`

type listRisksArgs struct {
	Scope    string `json:"scope"`
	Status   string `json:"status"`
	Category string `json:"category"`
	Limit    int    `json:"limit"`
}

// ListRisksTool wraps GET /v1/risks.
type ListRisksTool struct {
	client *mcp.Client
}

// NewListRisks constructs the tool.
func NewListRisks(c *mcp.Client) *ListRisksTool { return &ListRisksTool{client: c} }

// Definition implements mcp.Tool.
func (t *ListRisksTool) Definition() mcp.ToolDefinition {
	return mcp.ToolDefinition{
		Name:        "list_risks",
		Description: "List risks from the bearer tenant's risk register. Optional filters: status (treatment), category. RLS scopes to the caller's tenant.",
		InputSchema: json.RawMessage(listRisksInputSchema),
	}
}

// Handle implements mcp.Tool.
func (t *ListRisksTool) Handle(ctx context.Context, args json.RawMessage) (any, error) {
	var in listRisksArgs
	if err := strictDecode(args, &in); err != nil {
		return nil, err
	}
	if in.Scope != "" {
		return nil, fmt.Errorf("scope filter not yet supported in v1: the platform /v1/risks endpoint does not accept a scope query param; filter results client-side")
	}
	limit, err := clampLimit(in.Limit)
	if err != nil {
		return nil, err
	}

	q := url.Values{}
	if in.Status != "" {
		// Status maps to the platform's `treatment` param.
		q.Set("treatment", in.Status)
	}
	if in.Category != "" {
		q.Set("category", in.Category)
	}

	var resp struct {
		Risks []riskRow `json:"risks"`
		Count int       `json:"count"`
	}
	if err := t.client.Get(ctx, "/v1/risks", q, &resp); err != nil {
		return nil, err
	}
	out := resp.Risks
	if len(out) > limit {
		out = out[:limit]
	}
	return map[string]any{
		"risks":  out,
		"count":  len(out),
		"limit":  limit,
		"capped": len(resp.Risks) > limit,
	}, nil
}

// riskRow mirrors the canonical /v1/risks wire shape (slices 019 + 067).
// We include the hierarchy + severity fields slice 067 surfaced. We
// deliberately INCLUDE inherent_score + residual_score JSONB — these
// are program-level metadata (likelihood × impact + treatment scoring),
// NOT raw evidence payload. P0-A5 forbids `payload_json`-style
// free-text content payloads (e.g., a SAST scan's full output); the
// risk score fields are bounded structured JSON.
type riskRow struct {
	ID                  string          `json:"id"`
	Title               string          `json:"title"`
	Description         string          `json:"description"`
	Category            string          `json:"category"`
	Methodology         string          `json:"methodology"`
	InherentScore       json.RawMessage `json:"inherent_score"`
	Treatment           string          `json:"treatment"`
	TreatmentOwner      string          `json:"treatment_owner"`
	ResidualScore       json.RawMessage `json:"residual_score"`
	ReviewDueAt         *time.Time      `json:"review_due_at,omitempty"`
	AcceptedUntil       *string         `json:"accepted_until,omitempty"`
	Accepter            string          `json:"accepter"`
	InstrumentReference string          `json:"instrument_reference"`
	LinkedControlIDs    []string        `json:"linked_control_ids"`
	OrgUnitID           *string         `json:"org_unit_id,omitempty"`
	Themes              []string        `json:"themes"`
	Severity            int             `json:"severity"`
	CreatedAt           time.Time       `json:"created_at"`
	UpdatedAt           time.Time       `json:"updated_at"`
}

// ===== get_risk =====

const getRiskInputSchema = `{
  "type": "object",
  "additionalProperties": false,
  "required": ["risk_id"],
  "properties": {
    "risk_id": {
      "type": "string",
      "description": "Risk UUID. Required."
    }
  }
}`

type getRiskArgs struct {
	RiskID string `json:"risk_id"`
}

// GetRiskTool wraps GET /v1/risks/{id}.
type GetRiskTool struct {
	client *mcp.Client
}

// NewGetRisk constructs the tool.
func NewGetRisk(c *mcp.Client) *GetRiskTool { return &GetRiskTool{client: c} }

// Definition implements mcp.Tool.
func (t *GetRiskTool) Definition() mcp.ToolDefinition {
	return mcp.ToolDefinition{
		Name:        "get_risk",
		Description: "Fetch a single risk by UUID. Returns the canonical risk row + linked controls. When the platform's residual-deriver is wired, also returns the derived residual score and per-link effectiveness.",
		InputSchema: json.RawMessage(getRiskInputSchema),
	}
}

// Handle implements mcp.Tool.
func (t *GetRiskTool) Handle(ctx context.Context, args json.RawMessage) (any, error) {
	var in getRiskArgs
	if err := strictDecode(args, &in); err != nil {
		return nil, err
	}
	id, err := parseUUIDArg("risk_id", in.RiskID)
	if err != nil {
		return nil, err
	}

	// The platform returns {"risk": {...}, "residual": {...}?}. We
	// pass the envelope through verbatim — both fields are canonical
	// wire shapes, and the optional residual is the slice-020 derived
	// view (live recompute=false; pure read).
	var resp struct {
		Risk     riskRow         `json:"risk"`
		Residual json.RawMessage `json:"residual,omitempty"`
	}
	if err := t.client.Get(ctx, "/v1/risks/"+id.String(), url.Values{}, &resp); err != nil {
		return nil, err
	}
	out := map[string]any{"risk": resp.Risk}
	if len(resp.Residual) > 0 && string(resp.Residual) != "null" {
		out["residual"] = resp.Residual
	}
	return out, nil
}
