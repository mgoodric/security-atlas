// list_audit_periods tool — wraps GET /v1/audit-periods (slice 028).
// Slice 172 AC-11.
//
// The platform endpoint requires admin or grc_engineer role on
// detail/freeze/control-state routes; the bare list route is gated
// only by tenant context (any authenticated tenant member can list
// audit periods for their tenant). That's the right surface for a
// read tool — audit-period FREEZE metadata is bounded and not
// secret-shaped.

package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"net/url"
	"time"

	"github.com/mgoodric/security-atlas/internal/mcp"
)

const listAuditPeriodsInputSchema = `{
  "type": "object",
  "additionalProperties": false,
  "properties": {
    "status": {
      "type": "string",
      "enum": ["open", "frozen", "closed"],
      "description": "Filter by audit period status. Optional; default returns all statuses."
    },
    "limit": {
      "type": "integer",
      "minimum": 1,
      "maximum": 500,
      "description": "Result cap; default 100, max 500."
    }
  }
}`

type listAuditPeriodsArgs struct {
	Status string `json:"status"`
	Limit  int    `json:"limit"`
}

// ListAuditPeriodsTool wraps GET /v1/audit-periods.
type ListAuditPeriodsTool struct {
	client *mcp.Client
}

// NewListAuditPeriods constructs the tool.
func NewListAuditPeriods(c *mcp.Client) *ListAuditPeriodsTool {
	return &ListAuditPeriodsTool{client: c}
}

// Definition implements mcp.Tool.
func (t *ListAuditPeriodsTool) Definition() mcp.ToolDefinition {
	return mcp.ToolDefinition{
		Name:        "list_audit_periods",
		Description: "List audit periods for the bearer tenant (open / frozen / closed). Includes freeze metadata (frozen_at, frozen_hash, frozen_by) when applicable.",
		InputSchema: json.RawMessage(listAuditPeriodsInputSchema),
	}
}

// Handle implements mcp.Tool.
func (t *ListAuditPeriodsTool) Handle(ctx context.Context, args json.RawMessage) (any, error) {
	var in listAuditPeriodsArgs
	if err := strictDecode(args, &in); err != nil {
		return nil, err
	}
	if in.Status != "" {
		switch in.Status {
		case "open", "frozen", "closed":
		default:
			return nil, fmt.Errorf("status must be one of: open, frozen, closed")
		}
	}
	limit, err := clampLimit(in.Limit)
	if err != nil {
		return nil, err
	}

	// The platform's GET /v1/audit-periods does not currently accept
	// a server-side ?status= filter; we filter the result client-side
	// for v1. Slice-174 spillover can land a server-side filter when
	// LLM agents drive enough traffic to make the bandwidth matter.
	var resp struct {
		AuditPeriods []auditPeriodRow `json:"audit_periods"`
		Count        int              `json:"count"`
	}
	if err := t.client.Get(ctx, "/v1/audit-periods", url.Values{}, &resp); err != nil {
		return nil, err
	}

	rows := resp.AuditPeriods
	if in.Status != "" {
		filtered := make([]auditPeriodRow, 0, len(rows))
		for _, r := range rows {
			if r.Status == in.Status {
				filtered = append(filtered, r)
			}
		}
		rows = filtered
	}
	if len(rows) > limit {
		rows = rows[:limit]
	}
	return map[string]any{
		"audit_periods": rows,
		"count":         len(rows),
		"limit":         limit,
		"capped":        len(resp.AuditPeriods) > limit,
	}, nil
}

// auditPeriodRow mirrors the canonical /v1/audit-periods wire shape
// (slice 028's periodWire). FrozenHash is the hex-encoded form; we
// inherit the platform's pre-formatted string.
type auditPeriodRow struct {
	ID                 string     `json:"id"`
	Name               string     `json:"name"`
	FrameworkVersionID string     `json:"framework_version_id"`
	PeriodStart        time.Time  `json:"period_start"`
	PeriodEnd          time.Time  `json:"period_end"`
	Status             string     `json:"status"`
	FrozenAt           *time.Time `json:"frozen_at,omitempty"`
	FrozenHashHex      string     `json:"frozen_hash,omitempty"`
	FrozenBy           string     `json:"frozen_by,omitempty"`
	CreatedBy          string     `json:"created_by"`
	CreatedAt          time.Time  `json:"created_at"`
	UpdatedAt          time.Time  `json:"updated_at"`
}
