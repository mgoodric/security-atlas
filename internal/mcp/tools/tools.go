// Package tools holds the six read-only MCP tool implementations.
// Each tool wraps one platform HTTP endpoint and returns canonical
// column shapes that mirror the platform's existing wire contract
// (P0-A2: no MCP-only wide columns).
//
// Common patterns enforced across the six tools:
//
//   - List tools cap at default=100 max=500 (P0-A9).
//   - Inputs decoded via stdlib encoding/json with DisallowUnknownFields
//     so an LLM-supplied typo (e.g. `framework_ide`) is a clean error
//     rather than a silently-ignored arg.
//   - Each tool's input/output JSON Schema is exported as a `Schema`
//     constant so the schema-stability snapshot (AC-15) can re-derive
//     it without round-tripping through reflection.
//   - UUID args are parsed before the HTTP call so an LLM-supplied
//     non-UUID returns a clean error rather than a 400 round trip.
package tools

import (
	"bytes"
	"encoding/json"
	"fmt"

	"github.com/google/uuid"

	"github.com/mgoodric/security-atlas/internal/mcp"
)

// Defaults for list-shaped tools. Enforced per-tool by clampLimit.
const (
	DefaultLimit = 100
	MaxLimit     = 500
)

// clampLimit honors P0-A9: default 100, max 500.
// Returns (effective, error). A negative or zero `requested` yields
// the default; an over-cap value is an error so the LLM agent sees
// the contract (rather than silently truncated results).
func clampLimit(requested int) (int, error) {
	if requested <= 0 {
		return DefaultLimit, nil
	}
	if requested > MaxLimit {
		return 0, fmt.Errorf("limit %d exceeds max %d", requested, MaxLimit)
	}
	return requested, nil
}

// strictDecode unmarshals raw into v with DisallowUnknownFields. An
// empty / null raw decodes to the zero value of v (no error). MCP
// callers may omit the arguments object entirely for nullary tools;
// honoring that is conventional.
func strictDecode(raw json.RawMessage, v any) error {
	if len(raw) == 0 || bytes.Equal(bytes.TrimSpace(raw), []byte("null")) {
		return nil
	}
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.DisallowUnknownFields()
	if err := dec.Decode(v); err != nil {
		return fmt.Errorf("invalid arguments: %w", err)
	}
	return nil
}

// parseUUIDArg parses a required UUID string argument; returns a clean
// error on malformed input so the tool layer surfaces a typed message.
func parseUUIDArg(name, raw string) (uuid.UUID, error) {
	if raw == "" {
		return uuid.Nil, fmt.Errorf("%s is required", name)
	}
	id, err := uuid.Parse(raw)
	if err != nil {
		return uuid.Nil, fmt.Errorf("%s must be a UUID: %w", name, err)
	}
	return id, nil
}

// All builds the canonical set of six read tools, wired against the
// given mcp.Client. The order matches mcp.CanonicalToolOrder.
func All(client *mcp.Client) []mcp.Tool {
	return []mcp.Tool{
		NewListControls(client),
		NewGetControl(client),
		NewListRisks(client),
		NewGetRisk(client),
		NewListEvidence(client),
		NewListAuditPeriods(client),
	}
}
