// Unit tests for the redaction package. Pure-Go; no DB, no NATS. The
// integration test for end-to-end redaction (push -> stream -> consumer ->
// ledger contains REDACTED not secret) lives in internal/evidence/streambuf.

package redact_test

import (
	"reflect"
	"strings"
	"testing"

	"google.golang.org/protobuf/types/known/structpb"

	"github.com/mgoodric/security-atlas/internal/evidence/redact"
)

// AC-6: dot-path redaction. `$.secret` replaces the top-level "secret"
// key value with REDACTED literal.
func TestRedactor_DotPath(t *testing.T) {
	payload := mustStruct(t, map[string]any{
		"tool":   "semgrep",
		"secret": "ghp_AAAABBBBCCCCDDDDEEEE",
	})
	rules := []string{"$.secret"}

	got, err := redact.Apply(payload, rules)
	if err != nil {
		t.Fatalf("Apply: %v", err)
	}
	if v := got.Fields["secret"].GetStringValue(); v != redact.Marker {
		t.Fatalf("secret = %q, want %q", v, redact.Marker)
	}
	if v := got.Fields["tool"].GetStringValue(); v != "semgrep" {
		t.Fatalf("tool clobbered: %q", v)
	}
}

// AC-6: nested dot-path. `$.user.api_key` walks into a sub-object.
func TestRedactor_NestedPath(t *testing.T) {
	payload := mustStruct(t, map[string]any{
		"user": map[string]any{
			"name":    "alice",
			"api_key": "sk-deadbeef",
		},
	})
	rules := []string{"$.user.api_key"}
	got, err := redact.Apply(payload, rules)
	if err != nil {
		t.Fatalf("Apply: %v", err)
	}
	user := got.Fields["user"].GetStructValue()
	if v := user.Fields["api_key"].GetStringValue(); v != redact.Marker {
		t.Fatalf("api_key = %q, want %q", v, redact.Marker)
	}
	if v := user.Fields["name"].GetStringValue(); v != "alice" {
		t.Fatalf("name clobbered: %q", v)
	}
}

// AC-6: array-wildcard. `$.findings[*].token` redacts the `token` field
// inside every element of the `findings` array.
func TestRedactor_ArrayWildcard(t *testing.T) {
	payload := mustStruct(t, map[string]any{
		"findings": []any{
			map[string]any{"rule": "a", "token": "secret-1"},
			map[string]any{"rule": "b", "token": "secret-2"},
		},
	})
	rules := []string{"$.findings[*].token"}
	got, err := redact.Apply(payload, rules)
	if err != nil {
		t.Fatalf("Apply: %v", err)
	}
	arr := got.Fields["findings"].GetListValue()
	if len(arr.Values) != 2 {
		t.Fatalf("findings len = %d", len(arr.Values))
	}
	for i, v := range arr.Values {
		obj := v.GetStructValue()
		if obj.Fields["token"].GetStringValue() != redact.Marker {
			t.Fatalf("findings[%d].token = %q, want %q", i, obj.Fields["token"].GetStringValue(), redact.Marker)
		}
		if obj.Fields["rule"].GetStringValue() == redact.Marker {
			t.Fatalf("findings[%d].rule was incorrectly redacted", i)
		}
	}
}

// Empty rules list returns the payload unchanged.
func TestRedactor_NoRules(t *testing.T) {
	payload := mustStruct(t, map[string]any{"secret": "x"})
	got, err := redact.Apply(payload, nil)
	if err != nil {
		t.Fatalf("Apply: %v", err)
	}
	if got.Fields["secret"].GetStringValue() != "x" {
		t.Fatalf("with no rules, secret was modified")
	}
}

// Missing path is a no-op (does not error). A schema may declare a path
// that an individual record happens not to populate; we don't fail the
// record over it.
func TestRedactor_MissingPath(t *testing.T) {
	payload := mustStruct(t, map[string]any{"tool": "semgrep"})
	rules := []string{"$.does_not_exist", "$.nested.also_missing"}
	got, err := redact.Apply(payload, rules)
	if err != nil {
		t.Fatalf("Apply: %v", err)
	}
	if !reflect.DeepEqual(got.AsMap(), payload.AsMap()) {
		t.Fatalf("payload changed: %v", got.AsMap())
	}
}

// Malformed rule is rejected — we want loud failures at schema registration
// time, not silent passthrough that lets secrets through.
func TestRedactor_MalformedRule(t *testing.T) {
	payload := mustStruct(t, map[string]any{"x": "y"})
	cases := []string{"", "secret", "$..", "$.[]"}
	for _, rule := range cases {
		if _, err := redact.Apply(payload, []string{rule}); err == nil {
			t.Errorf("rule %q: expected error, got nil", rule)
		}
	}
}

// ExtractRulesFromSchema parses the x-redaction-rules extension key out of
// a JSON Schema body. Slice 014's schema_json is JSONB; the registry holds
// the raw bytes. The extractor must:
//   - return an empty slice when the key is absent
//   - return the array of strings when present
//   - reject the key when it is not a JSON array of strings (a schema bug)
func TestExtractRulesFromSchema_Absent(t *testing.T) {
	schema := `{"type":"object","properties":{"x":{"type":"string"}}}`
	rules, err := redact.ExtractRulesFromSchema([]byte(schema))
	if err != nil {
		t.Fatalf("Extract: %v", err)
	}
	if len(rules) != 0 {
		t.Fatalf("rules = %v, want empty", rules)
	}
}

func TestExtractRulesFromSchema_Present(t *testing.T) {
	schema := `{
        "type":"object",
        "x-redaction-rules":["$.secret","$.findings[*].token"],
        "properties":{"secret":{"type":"string"}}
    }`
	rules, err := redact.ExtractRulesFromSchema([]byte(schema))
	if err != nil {
		t.Fatalf("Extract: %v", err)
	}
	want := []string{"$.secret", "$.findings[*].token"}
	if !reflect.DeepEqual(rules, want) {
		t.Fatalf("rules = %v, want %v", rules, want)
	}
}

func TestExtractRulesFromSchema_BadShape(t *testing.T) {
	schema := `{"x-redaction-rules":"not-an-array"}`
	_, err := redact.ExtractRulesFromSchema([]byte(schema))
	if err == nil {
		t.Fatal("expected error on bad shape")
	}
	if !strings.Contains(err.Error(), "redact") {
		t.Fatalf("error did not mention redact: %v", err)
	}
}

func mustStruct(t *testing.T, m map[string]any) *structpb.Struct {
	t.Helper()
	s, err := structpb.NewStruct(m)
	if err != nil {
		t.Fatalf("NewStruct: %v", err)
	}
	return s
}
