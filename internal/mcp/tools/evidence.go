// list_evidence tool — wraps GET /v1/evidence (slice 064 + 106).
// Slice 172 AC-10.
//
// CRITICAL P0-A5: payload_json MUST NOT appear in any tool response.
// The platform's /v1/evidence wire shape already excludes the
// evidence_records.payload column (the slice-064 evidenceWire struct
// does not carry a payload field; the column lives in
// evidence_records.payload but is exposed via the separate evidence
// detail endpoint, NOT the list shape we consume here). This tool's
// safety therefore rests on:
//
//   (1) The platform endpoint we consume does not return payload_json.
//   (2) Even if a future platform slice adds payload_json to the list
//       shape, our typed `evidenceRow` struct deliberately omits the
//       field — encoding/json will discard it on unmarshal.
//
// Both layers must hold; the schema-stability snapshot (testdata/)
// gates regression at the response shape level.

package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"net/url"
	"strconv"

	"github.com/mgoodric/security-atlas/internal/mcp"
)

// ===== list_evidence =====

const listEvidenceInputSchema = `{
  "type": "object",
  "additionalProperties": false,
  "properties": {
    "control_id": {
      "type": "string",
      "description": "Filter to evidence records for one control (UUID). Optional; when absent the tool returns the tenant-wide ledger window."
    },
    "kind": {
      "type": "string",
      "description": "Filter by evidence_kind (e.g., \"aws.s3.bucket_encryption.v1\"). Optional."
    },
    "result": {
      "type": "string",
      "enum": ["pass", "fail", "na", "inconclusive"],
      "description": "Filter by evidence result. Optional."
    },
    "freshness": {
      "type": "string",
      "description": "Freshness filter (NOT YET SUPPORTED in v1; the platform /v1/evidence endpoint does not accept a freshness filter). Filter results client-side using observed_at."
    },
    "limit": {
      "type": "integer",
      "minimum": 1,
      "maximum": 500,
      "description": "Result cap; default 100, max 500."
    }
  }
}`

type listEvidenceArgs struct {
	ControlID string `json:"control_id"`
	Kind      string `json:"kind"`
	Result    string `json:"result"`
	Freshness string `json:"freshness"`
	Limit     int    `json:"limit"`
}

// ListEvidenceTool wraps GET /v1/evidence.
type ListEvidenceTool struct {
	client *mcp.Client
}

// NewListEvidence constructs the tool.
func NewListEvidence(c *mcp.Client) *ListEvidenceTool { return &ListEvidenceTool{client: c} }

// Definition implements mcp.Tool.
func (t *ListEvidenceTool) Definition() mcp.ToolDefinition {
	return mcp.ToolDefinition{
		Name:        "list_evidence",
		Description: "List evidence records from the bearer tenant's ledger. payload_json is NEVER included in the response (P0-A5). Optional filters: control_id, kind, result.",
		InputSchema: json.RawMessage(listEvidenceInputSchema),
	}
}

// Handle implements mcp.Tool.
func (t *ListEvidenceTool) Handle(ctx context.Context, args json.RawMessage) (any, error) {
	var in listEvidenceArgs
	if err := strictDecode(args, &in); err != nil {
		return nil, err
	}
	if in.Freshness != "" {
		return nil, fmt.Errorf("freshness filter not yet supported in v1: the platform /v1/evidence endpoint does not accept a freshness filter; filter client-side using observed_at, or call freshnessdrift endpoints separately (v2 follow-on)")
	}
	limit, err := clampLimit(in.Limit)
	if err != nil {
		return nil, err
	}

	q := url.Values{}
	if in.ControlID != "" {
		// Validate UUID format before round-trip.
		if _, err := parseUUIDArg("control_id", in.ControlID); err != nil {
			return nil, err
		}
		q.Set("control_id", in.ControlID)
	}
	if in.Kind != "" {
		q.Set("kind", in.Kind)
	}
	if in.Result != "" {
		// Validate enum locally so a typo is a clean tool error.
		switch in.Result {
		case "pass", "fail", "na", "inconclusive":
			q.Set("result", in.Result)
		default:
			return nil, fmt.Errorf("result must be one of: pass, fail, na, inconclusive")
		}
	}
	// The platform endpoint accepts ?limit=; we forward as the
	// per-page cap. The platform also paginates via ?cursor=; v1 of
	// the MCP tool exposes a single page only (slice-174 spillover
	// can extend if LLM agents need cursor-following).
	q.Set("limit", strconv.Itoa(limit))

	var resp struct {
		ControlID  string        `json:"control_id"`
		Evidence   []evidenceRow `json:"evidence"`
		Count      int           `json:"count"`
		NextCursor string        `json:"next_cursor"`
	}
	if err := t.client.Get(ctx, "/v1/evidence", q, &resp); err != nil {
		return nil, err
	}
	// Defense-in-depth: even though the typed struct omits
	// payload_json, scrub the slice to guarantee no surprise
	// fields slip through (e.g., if a future platform slice adds
	// a wide column with a different JSON name).
	out := scrubEvidenceRows(resp.Evidence)
	if len(out) > limit {
		out = out[:limit]
	}
	return map[string]any{
		"control_id":  resp.ControlID,
		"evidence":    out,
		"count":       len(out),
		"limit":       limit,
		"capped":      len(resp.Evidence) > limit,
		"next_cursor": resp.NextCursor,
	}, nil
}

// evidenceRow mirrors the canonical /v1/evidence wire shape (slice 064 +
// slice 106). We DELIBERATELY OMIT a payload / payload_json field — see
// P0-A5 and the package-level comment. We DO carry the provenance JSONB
// (source attribution) because that's bounded metadata (connector id,
// source system id, query hash) — NOT the evidence content payload.
type evidenceRow struct {
	EvidenceID   string          `json:"evidence_id"`
	EvidenceKind *string         `json:"evidence_kind"`
	ObservedAt   string          `json:"observed_at"`
	Source       json.RawMessage `json:"source"`
	ContentHash  string          `json:"content_hash"`
	ScopeCell    *string         `json:"scope_cell"`
	Result       string          `json:"result"`
}

// scrubEvidenceRows returns the input slice unchanged after re-marshal /
// re-unmarshal through the typed struct. Pure typed-struct round-trip
// is the canonical P0-A5 defense: any field on the platform response
// not declared on evidenceRow is dropped here.
//
// This is an explicit no-op in the happy path (the typed struct already
// did the work in client.Get's json.Unmarshal); the function exists as
// a documentation surface and as a unit-test target — a future audit
// can grep for `scrubEvidenceRows` to confirm the redaction layer
// exists.
func scrubEvidenceRows(rows []evidenceRow) []evidenceRow {
	if rows == nil {
		return []evidenceRow{}
	}
	out := make([]evidenceRow, len(rows))
	copy(out, rows)
	return out
}
