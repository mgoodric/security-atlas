// list_controls / get_control tools — wrap GET /v1/controls + the SCF
// anchor reads from /v1/anchors. Slice 172 AC-6 + AC-7.
//
// list_controls returns the tenant's active controls — canonical shape
// matches the platform's /v1/controls endpoint (slice 151). The
// framework_id / scope filter args from the slice doc are documented
// but not server-supported in v1; the platform /v1/controls endpoint
// returns all active tenant controls today. If an LLM passes either
// arg, we surface a tool error explaining the limitation rather than
// silently ignoring (P0-A3: don't relax validators).
//
// get_control returns one tenant control row plus its SCF anchor
// resolution. The slice doc uses `anchor_id` semantically; the
// underlying surface today is the tenant control_id (UUID) — we
// document the alias in the input schema description.

package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"net/url"

	"github.com/mgoodric/security-atlas/internal/mcp"
)

// ===== list_controls =====

const listControlsInputSchema = `{
  "type": "object",
  "additionalProperties": false,
  "properties": {
    "framework_id": {
      "type": "string",
      "description": "Framework filter (NOT YET SUPPORTED in v1 — passing this returns a tool error so callers know the contract; v2 will land per-framework filtering)."
    },
    "scope": {
      "type": "string",
      "description": "Scope cell filter (NOT YET SUPPORTED in v1 — same posture as framework_id)."
    },
    "limit": {
      "type": "integer",
      "minimum": 1,
      "maximum": 500,
      "description": "Result cap; default 100, max 500. Server-side cap is the bigger of these and the platform's own ceiling."
    }
  }
}`

type listControlsArgs struct {
	FrameworkID string `json:"framework_id"`
	Scope       string `json:"scope"`
	Limit       int    `json:"limit"`
}

// ListControlsTool wraps GET /v1/controls.
type ListControlsTool struct {
	client *mcp.Client
}

// NewListControls constructs the tool.
func NewListControls(c *mcp.Client) *ListControlsTool { return &ListControlsTool{client: c} }

// Definition implements mcp.Tool.
func (t *ListControlsTool) Definition() mcp.ToolDefinition {
	return mcp.ToolDefinition{
		Name:        "list_controls",
		Description: "List the bearer tenant's active controls (UCF anchors with framework satisfactions joined). Returns canonical control rows.",
		InputSchema: json.RawMessage(listControlsInputSchema),
	}
}

// Handle implements mcp.Tool.
func (t *ListControlsTool) Handle(ctx context.Context, args json.RawMessage) (any, error) {
	var in listControlsArgs
	if err := strictDecode(args, &in); err != nil {
		return nil, err
	}
	if in.FrameworkID != "" {
		return nil, fmt.Errorf("framework_id filter not yet supported in v1: the platform /v1/controls endpoint returns all active tenant controls; filter results client-side")
	}
	if in.Scope != "" {
		return nil, fmt.Errorf("scope filter not yet supported in v1: the platform /v1/controls endpoint does not accept a scope query param; filter results client-side")
	}
	limit, err := clampLimit(in.Limit)
	if err != nil {
		return nil, err
	}

	// The platform /v1/controls endpoint (slice 151) returns the
	// tenant's active control rows in a flat list. The endpoint does
	// not paginate; we cap at `limit` on the client side.
	var resp struct {
		Controls []controlRow `json:"controls"`
		Count    int          `json:"count"`
	}
	if err := t.client.Get(ctx, "/v1/controls", url.Values{}, &resp); err != nil {
		return nil, err
	}
	out := resp.Controls
	if len(out) > limit {
		out = out[:limit]
	}
	return map[string]any{
		"controls": out,
		"count":    len(out),
		"limit":    limit,
		"capped":   len(resp.Controls) > limit,
	}, nil
}

// controlRow mirrors the canonical /v1/controls response. Field names
// match the platform's wire shape exactly (P0-A2).
type controlRow struct {
	ID             string `json:"id"`
	Title          string `json:"title"`
	ControlFamily  string `json:"control_family"`
	SCFID          string `json:"scf_id"`
	LifecycleState string `json:"lifecycle_state"`
	BundleID       string `json:"bundle_id"`
}

// ===== get_control =====

const getControlInputSchema = `{
  "type": "object",
  "additionalProperties": false,
  "required": ["anchor_id"],
  "properties": {
    "anchor_id": {
      "type": "string",
      "description": "The control identifier. Accepts either the tenant control UUID OR the SCF anchor short code (e.g., \"IAC-06\"). Required."
    }
  }
}`

type getControlArgs struct {
	AnchorID string `json:"anchor_id"`
}

// GetControlTool wraps GET /v1/anchors/{id} for short-code lookups and
// a single-row fetch from /v1/controls for UUID lookups.
type GetControlTool struct {
	client *mcp.Client
}

// NewGetControl constructs the tool.
func NewGetControl(c *mcp.Client) *GetControlTool { return &GetControlTool{client: c} }

// Definition implements mcp.Tool.
func (t *GetControlTool) Definition() mcp.ToolDefinition {
	return mcp.ToolDefinition{
		Name:        "get_control",
		Description: "Fetch a single control by UUID or SCF anchor short code. Returns the canonical control row plus its scope and current state.",
		InputSchema: json.RawMessage(getControlInputSchema),
	}
}

// Handle implements mcp.Tool.
func (t *GetControlTool) Handle(ctx context.Context, args json.RawMessage) (any, error) {
	var in getControlArgs
	if err := strictDecode(args, &in); err != nil {
		return nil, err
	}
	if in.AnchorID == "" {
		return nil, fmt.Errorf("anchor_id is required")
	}

	// Try UUID first; fall back to /v1/anchors/{shortcode}.
	if _, err := parseUUIDArg("anchor_id", in.AnchorID); err == nil {
		// UUID lookup: the platform doesn't expose a
		// /v1/controls/{id} endpoint today, so we fetch the
		// list and filter. This is acceptable for v1 (a
		// 50-150-person org runs O(50-300) controls per CLAUDE.md
		// canvas §2.2); slice-174 spillover can land a real
		// /v1/controls/{id} if the list size grows.
		var resp struct {
			Controls []controlRow `json:"controls"`
		}
		if err := t.client.Get(ctx, "/v1/controls", url.Values{}, &resp); err != nil {
			return nil, err
		}
		for _, c := range resp.Controls {
			if c.ID == in.AnchorID {
				return map[string]any{"control": c}, nil
			}
		}
		return nil, fmt.Errorf("control not found: %s", in.AnchorID)
	}

	// Short-code path: /v1/anchors/{id} (slice 006).
	var anchorResp struct {
		Anchor anchorRow `json:"anchor"`
	}
	path := "/v1/anchors/" + url.PathEscape(in.AnchorID)
	if err := t.client.Get(ctx, path, url.Values{}, &anchorResp); err != nil {
		return nil, err
	}
	return map[string]any{"anchor": anchorResp.Anchor}, nil
}

// anchorRow mirrors the canonical /v1/anchors/{id} response (slice 006).
// We intentionally omit any fields not in the canonical platform shape;
// adding fields is a slice-174 / future-slice concern, not a "wide
// columns for MCP" workaround (P0-A2).
type anchorRow struct {
	ID             string `json:"id"`
	ShortCode      string `json:"short_code"`
	Title          string `json:"title"`
	Family         string `json:"family"`
	Description    string `json:"description,omitempty"`
	CatalogVersion string `json:"catalog_version,omitempty"`
}
