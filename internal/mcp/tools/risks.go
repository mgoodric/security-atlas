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
	"strconv"
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
      "description": "Filter by treatment status: untreated | avoid | accept | transfer | mitigate. Optional; default returns all treatments."
    },
    "category": {
      "type": "string",
      "description": "Filter by risk category slug, one of the organisation's own categories (call list_risk_categories for the valid slugs; archived ones are filterable). An unknown slug is an error naming the valid ones. Optional."
    },
    "include_rollups": {
      "type": "boolean",
      "description": "Also list roll-ups (risks with at least one child risk linked). Default false: a roll-up summarises its children, so listing both would count the same exposure twice. Included roll-ups carry child_count."
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
	Scope          string `json:"scope"`
	Status         string `json:"status"`
	Category       string `json:"category"`
	IncludeRollUps bool   `json:"include_rollups"`
	Limit          int    `json:"limit"`
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
		Description: "List risks from the bearer tenant's risk register. Roll-ups (risks with at least one child linked) are left out unless include_rollups is true. Optional filters: status (treatment), category. RLS scopes to the caller's tenant.",
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
	if in.IncludeRollUps {
		q.Set("include_rollups", "true")
	}
	// The platform pages the register (#2132), so ask it for limit rows
	// and read capped from its filtered total (#2482): with more than 500
	// risks, a full page no longer means the list was whole.
	q.Set("limit", strconv.Itoa(limit))

	var resp struct {
		Risks []riskRow `json:"risks"`
		Count int       `json:"count"`
		Total *int      `json:"total"`
	}
	if err := t.client.Get(ctx, "/v1/risks", q, &resp); err != nil {
		return nil, err
	}
	out := resp.Risks
	if len(out) > limit {
		out = out[:limit]
	}
	capped := len(resp.Risks) > limit
	if resp.Total != nil {
		capped = *resp.Total > len(out)
	}
	return map[string]any{
		"risks":  out,
		"count":  len(out),
		"limit":  limit,
		"capped": capped,
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
	CategoryID          string          `json:"category_id,omitempty"`
	CategoryName        string          `json:"category_name,omitempty"`
	Methodology         string          `json:"methodology"`
	InherentScore       json.RawMessage `json:"inherent_score"`
	Treatment           string          `json:"treatment"`
	AcceptanceStatus    string          `json:"acceptance_status,omitempty"`
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
	ChildCount          int             `json:"child_count,omitempty"`
	CreatedAt           time.Time       `json:"created_at"`
	UpdatedAt           time.Time       `json:"updated_at"`
}

// ===== list_risk_categories =====

// #1815: risk categories are organisation-owned (#1745), so an assistant
// learns the valid values here rather than from a fixed list in a schema.

const listRiskCategoriesInputSchema = `{
  "type": "object",
  "additionalProperties": false,
  "properties": {
    "include_archived": {
      "type": "boolean",
      "description": "Also list archived categories. They stay on the risks that have them and are filterable, but cannot be given to a new risk. Default false."
    }
  }
}`

type listRiskCategoriesArgs struct {
	IncludeArchived bool `json:"include_archived"`
}

// riskCategoryRow mirrors the GET /v1/risks/categories wire shape.
type riskCategoryRow struct {
	ID          string    `json:"id"`
	Slug        string    `json:"slug"`
	Name        string    `json:"name"`
	Description string    `json:"description"`
	Archived    bool      `json:"archived"`
	IsDefault   bool      `json:"is_default"`
	RiskCount   int64     `json:"risk_count"`
	RuleCount   int64     `json:"rule_count"`
	CreatedAt   time.Time `json:"created_at"`
	UpdatedAt   time.Time `json:"updated_at"`
}

// ListRiskCategoriesTool wraps GET /v1/risks/categories.
type ListRiskCategoriesTool struct {
	client *mcp.Client
}

// NewListRiskCategories constructs the tool.
func NewListRiskCategories(c *mcp.Client) *ListRiskCategoriesTool {
	return &ListRiskCategoriesTool{client: c}
}

// Definition implements mcp.Tool.
func (t *ListRiskCategoriesTool) Definition() mcp.ToolDefinition {
	return mcp.ToolDefinition{
		Name:        "list_risk_categories",
		Description: "List the bearer tenant's own risk categories (slug, name, default flag, usage counts). These are the valid values for create_risk's category (by slug or name) and list_risks' category filter (by slug).",
		InputSchema: json.RawMessage(listRiskCategoriesInputSchema),
	}
}

// Handle implements mcp.Tool.
func (t *ListRiskCategoriesTool) Handle(ctx context.Context, args json.RawMessage) (any, error) {
	var in listRiskCategoriesArgs
	if err := strictDecode(args, &in); err != nil {
		return nil, err
	}
	q := url.Values{}
	if in.IncludeArchived {
		q.Set("include_archived", "true")
	}
	var resp struct {
		Categories []riskCategoryRow `json:"categories"`
		Count      int               `json:"count"`
	}
	if err := t.client.Get(ctx, "/v1/risks/categories", q, &resp); err != nil {
		return nil, err
	}
	return map[string]any{"categories": resp.Categories, "count": len(resp.Categories)}, nil
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
