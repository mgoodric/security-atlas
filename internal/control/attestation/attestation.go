// Package attestation holds the slice-011 helpers for the manual control
// attestation flow. Two responsibilities live here:
//
//  1. ValidateAttestationData — runs a control bundle's
//     `manual_evidence_schema` (slice 009) against an inbound payload's
//     `attestation_data` field. The platform-level
//     `manual.attestation.v1` schema (slice 014) validates the envelope
//     (attestor + statement); this validator enforces the per-control
//     shape on top.
//
//  2. FormProperties — extracts the top-level properties from a JSON
//     Schema so the frontend renderer can iterate them deterministically.
//     v1 deliberately supports flat schemas only — nested objects fall
//     back to plain JSON inputs at the client.
//
// JSON Schema dialect: draft 2020-12, matching the platform registry
// (`internal/api/schemaregistry`). We reuse santhosh-tekuri/jsonschema/v6
// so behavior is identical across server-side enforcement points.
package attestation

import (
	"bytes"
	"encoding/json"
	"fmt"
	"sort"

	"github.com/santhosh-tekuri/jsonschema/v6"
)

// ValidateAttestationData compiles controlSchema (a JSON Schema object
// from the control bundle's manual_evidence_schema field) and validates
// data against it. Returns nil when controlSchema is empty (the control
// declined to declare per-control fields), or a wrapped validation
// error otherwise.
//
// The caller has already validated the envelope (attestor + statement)
// at the registry level via the platform schema; this validator only
// covers the control-specific extension.
func ValidateAttestationData(controlSchema map[string]any, data map[string]any) error {
	if len(controlSchema) == 0 {
		return nil
	}
	schemaJSON, err := json.Marshal(controlSchema)
	if err != nil {
		return fmt.Errorf("marshal manual_evidence_schema: %w", err)
	}
	doc, err := jsonschema.UnmarshalJSON(bytes.NewReader(schemaJSON))
	if err != nil {
		return fmt.Errorf("manual_evidence_schema is not valid JSON Schema: %w", err)
	}
	compiler := jsonschema.NewCompiler()
	const url = "mem://manual_evidence_schema"
	if err := compiler.AddResource(url, doc); err != nil {
		return fmt.Errorf("compile resource: %w", err)
	}
	compiled, err := compiler.Compile(url)
	if err != nil {
		return fmt.Errorf("compile schema: %w", err)
	}

	// jsonschema.Validate operates over the generic interface tree
	// (map[string]any, []any). Marshal/unmarshal through JSON gives us
	// that shape directly without manual coercion.
	if data == nil {
		data = map[string]any{}
	}
	if err := compiled.Validate(any(data)); err != nil {
		return fmt.Errorf("schema rejected attestation_data: %w", err)
	}
	return nil
}

// FormProperty describes one top-level property on a control's
// manual_evidence_schema. The frontend uses this list to render one
// input per property without parsing JSON Schema itself.
type FormProperty struct {
	Name        string `json:"name"`
	Type        string `json:"type"`         // string | integer | number | boolean | array | object
	Title       string `json:"title"`        // schema-author-facing label
	Description string `json:"description"`  // help text
	Required    bool   `json:"required"`     // present in schema.required
	Format      string `json:"format"`       // optional JSON Schema format (uri, date-time, etc.)
	Enum        []any  `json:"enum"`         // optional enum constraint
	MinLength   *int   `json:"min_length,omitempty"`
	MaxLength   *int   `json:"max_length,omitempty"`
}

// FormProperties walks the top-level `properties` of a control's
// manual_evidence_schema and returns one descriptor per property. The
// returned slice is sorted lexicographically so the frontend renders
// fields in a deterministic order (no map-iteration flicker between
// requests). Nested objects are reported with Type="object" — the
// renderer falls back to a plain JSON textarea for those in v1.
func FormProperties(schema map[string]any) []FormProperty {
	if schema == nil {
		return nil
	}
	rawProps, _ := schema["properties"].(map[string]any)
	if len(rawProps) == 0 {
		return nil
	}
	required := map[string]struct{}{}
	if reqArr, ok := schema["required"].([]any); ok {
		for _, r := range reqArr {
			if s, ok := r.(string); ok {
				required[s] = struct{}{}
			}
		}
	}
	names := make([]string, 0, len(rawProps))
	for k := range rawProps {
		names = append(names, k)
	}
	sort.Strings(names)
	out := make([]FormProperty, 0, len(names))
	for _, n := range names {
		propMap, _ := rawProps[n].(map[string]any)
		if propMap == nil {
			continue
		}
		fp := FormProperty{Name: n}
		if t, ok := propMap["type"].(string); ok {
			fp.Type = t
		}
		if title, ok := propMap["title"].(string); ok {
			fp.Title = title
		}
		if desc, ok := propMap["description"].(string); ok {
			fp.Description = desc
		}
		if format, ok := propMap["format"].(string); ok {
			fp.Format = format
		}
		if enumVals, ok := propMap["enum"].([]any); ok {
			fp.Enum = enumVals
		}
		if v, ok := propMap["minLength"].(float64); ok {
			n := int(v)
			fp.MinLength = &n
		}
		if v, ok := propMap["maxLength"].(float64); ok {
			n := int(v)
			fp.MaxLength = &n
		}
		if _, isRequired := required[n]; isRequired {
			fp.Required = true
		}
		out = append(out, fp)
	}
	return out
}
