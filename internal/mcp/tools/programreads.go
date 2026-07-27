// Program-record read tools — list_policies / list_vendors /
// list_exceptions / list_action_plans. These widen the slice-172
// read surface (controls, risks, evidence, audit periods) to the rest
// of the program records a security team asks about.
//
// Each wraps an existing platform list endpoint whose wire shape is a
// flat `{"<key>": [...]}` object. Rather than re-declare a typed row
// per domain (which would silently drop fields if the platform shape
// evolves — P0-A2), these tools pass the platform rows through verbatim
// as json.RawMessage and only enforce the shared list cap. The output
// shape mirrors the existing read tools: `{"<key>": [...], "count",
// "limit", "capped"}`.

package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"net/url"

	"github.com/mgoodric/security-atlas/internal/mcp"
)

// programListInputSchema is shared by every program-record list tool:
// a single optional `limit` (default 100, max 500 — P0-A9, no
// "give me all" footgun).
const programListInputSchema = `{
  "type": "object",
  "additionalProperties": false,
  "properties": {
    "limit": {
      "type": "integer",
      "minimum": 1,
      "maximum": 500,
      "description": "Result cap; default 100, max 500. A request over 500 is a tool error."
    }
  }
}`

type programListArgs struct {
	Limit int `json:"limit"`
}

// programListTool is a generic read tool over a platform list endpoint
// that returns `{"<listKey>": [...]}`. Constructed via the New* helpers
// below so each registered tool carries its own name/description.
type programListTool struct {
	client   *mcp.Client
	name     string
	desc     string
	endpoint string
	listKey  string
}

// Definition implements mcp.Tool.
func (t *programListTool) Definition() mcp.ToolDefinition {
	return mcp.ToolDefinition{
		Name:        t.name,
		Description: t.desc,
		InputSchema: json.RawMessage(programListInputSchema),
	}
}

// Handle implements mcp.Tool. It GETs the endpoint, decodes only the
// list under listKey (leaving each row as raw platform JSON), caps it,
// and returns the canonical envelope.
func (t *programListTool) Handle(ctx context.Context, args json.RawMessage) (any, error) {
	var in programListArgs
	if err := strictDecode(args, &in); err != nil {
		return nil, err
	}
	limit, err := clampLimit(in.Limit)
	if err != nil {
		return nil, err
	}

	var resp map[string]json.RawMessage
	if err := t.client.Get(ctx, t.endpoint, url.Values{}, &resp); err != nil {
		return nil, err
	}

	rows := []json.RawMessage{}
	if raw, ok := resp[t.listKey]; ok && len(raw) > 0 {
		if err := json.Unmarshal(raw, &rows); err != nil {
			return nil, fmt.Errorf("decode %q from %s: %w", t.listKey, t.endpoint, err)
		}
	}

	capped := false
	if len(rows) > limit {
		rows = rows[:limit]
		capped = true
	}

	return map[string]any{
		t.listKey: rows,
		"count":   len(rows),
		"limit":   limit,
		"capped":  capped,
	}, nil
}

// NewListPolicies wraps GET /v1/policies.
func NewListPolicies(c *mcp.Client) mcp.Tool {
	return &programListTool{
		client:   c,
		name:     "list_policies",
		desc:     "List the bearer tenant's policies (the policy library with acknowledgment state). Returns canonical policy rows.",
		endpoint: "/v1/policies",
		listKey:  "policies",
	}
}

// NewListVendors wraps GET /v1/vendors.
func NewListVendors(c *mcp.Client) mcp.Tool {
	return &programListTool{
		client:   c,
		name:     "list_vendors",
		desc:     "List the bearer tenant's third-party vendors (the vendor register with review cadence). Returns canonical vendor rows.",
		endpoint: "/v1/vendors",
		listKey:  "vendors",
	}
}

// NewListExceptions wraps GET /v1/exceptions.
func NewListExceptions(c *mcp.Client) mcp.Tool {
	return &programListTool{
		client:   c,
		name:     "list_exceptions",
		desc:     "List the bearer tenant's exceptions (accepted non-compliance with compensating controls and an expiry). Returns canonical exception rows.",
		endpoint: "/v1/exceptions",
		listKey:  "exceptions",
	}
}

// NewListActionPlans wraps GET /v1/action-plans.
func NewListActionPlans(c *mcp.Client) mcp.Tool {
	return &programListTool{
		client:   c,
		name:     "list_action_plans",
		desc:     "List the bearer tenant's action plans (forward-looking remediation commitments with milestones; OSCAL POA&M). Returns canonical action-plan rows.",
		endpoint: "/v1/action-plans",
		listKey:  "action_plans",
	}
}
