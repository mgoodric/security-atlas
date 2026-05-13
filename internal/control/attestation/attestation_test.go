package attestation

import (
	"strings"
	"testing"
)

func TestValidateAttestationData_NoSchemaIsNoop(t *testing.T) {
	t.Parallel()
	if err := ValidateAttestationData(nil, map[string]any{"x": 1}); err != nil {
		t.Fatalf("nil schema must be a no-op; got %v", err)
	}
	if err := ValidateAttestationData(map[string]any{}, nil); err != nil {
		t.Fatalf("empty schema must be a no-op; got %v", err)
	}
}

func TestValidateAttestationData_AcceptsConforming(t *testing.T) {
	t.Parallel()
	schema := map[string]any{
		"type":     "object",
		"required": []any{"reviewer"},
		"properties": map[string]any{
			"reviewer": map[string]any{"type": "string", "minLength": 1},
			"count":    map[string]any{"type": "integer", "minimum": 0.0},
		},
	}
	data := map[string]any{"reviewer": "sample-user", "count": float64(7)}
	if err := ValidateAttestationData(schema, data); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestValidateAttestationData_RejectsMissingRequired(t *testing.T) {
	t.Parallel()
	schema := map[string]any{
		"type":     "object",
		"required": []any{"reviewer"},
		"properties": map[string]any{
			"reviewer": map[string]any{"type": "string"},
		},
	}
	err := ValidateAttestationData(schema, map[string]any{})
	if err == nil {
		t.Fatalf("expected validation error for missing required")
	}
	if !strings.Contains(err.Error(), "reviewer") {
		t.Fatalf("error should mention reviewer; got %v", err)
	}
}

func TestValidateAttestationData_RejectsWrongType(t *testing.T) {
	t.Parallel()
	schema := map[string]any{
		"type": "object",
		"properties": map[string]any{
			"count": map[string]any{"type": "integer"},
		},
	}
	err := ValidateAttestationData(schema, map[string]any{"count": "seven"})
	if err == nil {
		t.Fatalf("expected validation error for wrong type")
	}
}

func TestFormProperties_ReturnsNilForEmpty(t *testing.T) {
	t.Parallel()
	if got := FormProperties(nil); got != nil {
		t.Fatalf("nil schema must yield nil; got %v", got)
	}
	if got := FormProperties(map[string]any{}); got != nil {
		t.Fatalf("empty schema must yield nil; got %v", got)
	}
}

func TestFormProperties_DeterministicOrder(t *testing.T) {
	t.Parallel()
	schema := map[string]any{
		"type": "object",
		"properties": map[string]any{
			"zebra":  map[string]any{"type": "string"},
			"apple":  map[string]any{"type": "string"},
			"mango":  map[string]any{"type": "string"},
			"banana": map[string]any{"type": "string"},
		},
	}
	got := FormProperties(schema)
	if len(got) != 4 {
		t.Fatalf("expected 4 properties; got %d", len(got))
	}
	want := []string{"apple", "banana", "mango", "zebra"}
	for i, w := range want {
		if got[i].Name != w {
			t.Fatalf("property[%d] = %q; want %q", i, got[i].Name, w)
		}
	}
}

func TestFormProperties_PopulatesAttributes(t *testing.T) {
	t.Parallel()
	maxLen := 3.0
	schema := map[string]any{
		"type":     "object",
		"required": []any{"reviewer"},
		"properties": map[string]any{
			"reviewer": map[string]any{
				"type":        "string",
				"title":       "Reviewer",
				"description": "Who reviewed it",
				"minLength":   1.0,
				"maxLength":   maxLen,
			},
			"verdict": map[string]any{
				"type": "string",
				"enum": []any{"pass", "fail", "na"},
			},
		},
	}
	got := FormProperties(schema)
	if len(got) != 2 {
		t.Fatalf("expected 2 properties; got %d", len(got))
	}
	reviewer := got[0]
	if reviewer.Name != "reviewer" || reviewer.Type != "string" || !reviewer.Required {
		t.Fatalf("reviewer descriptor wrong: %+v", reviewer)
	}
	if reviewer.MinLength == nil || *reviewer.MinLength != 1 {
		t.Fatalf("reviewer min_length wrong: %+v", reviewer.MinLength)
	}
	if reviewer.MaxLength == nil || *reviewer.MaxLength != 3 {
		t.Fatalf("reviewer max_length wrong: %+v", reviewer.MaxLength)
	}
	verdict := got[1]
	if len(verdict.Enum) != 3 {
		t.Fatalf("verdict enum wrong: %+v", verdict.Enum)
	}
}
