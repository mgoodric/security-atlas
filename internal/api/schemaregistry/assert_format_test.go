package schemaregistry

import (
	"encoding/json"
	"io/fs"
	"strings"
	"testing"
)

// #2930: `format` is enforced only for a schema version that sets
// x-assert-format: true; everywhere else it stays an annotation.
func TestCompileSchema_AssertFormatOptIn(t *testing.T) {
	t.Parallel()
	schema := func(flag string) []byte {
		return []byte(`{
  "$schema": "https://json-schema.org/draft/2020-12/schema",
  "type": "object",` + flag + `
  "properties": { "last_seen_at": { "type": "string", "format": "date-time" } }
}`)
	}
	cases := []struct {
		name    string
		flag    string
		payload string
		ok      bool
	}{
		{"opted_in_rejects_malformed", `"x-assert-format": true,`, `{"last_seen_at":"not-a-date"}`, false},
		{"opted_in_rejects_date_only", `"x-assert-format": true,`, `{"last_seen_at":"2026-11-22"}`, false},
		{"opted_in_accepts_rfc3339", `"x-assert-format": true,`, `{"last_seen_at":"2026-11-22T10:00:00Z"}`, true},
		{"absent_accepts_malformed", ``, `{"last_seen_at":"not-a-date"}`, true},
		{"false_accepts_malformed", `"x-assert-format": false,`, `{"last_seen_at":"not-a-date"}`, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			compiled, err := compileSchema(schema(tc.flag))
			if err != nil {
				t.Fatalf("compileSchema: %v", err)
			}
			var v any
			if err := json.Unmarshal([]byte(tc.payload), &v); err != nil {
				t.Fatal(err)
			}
			err = compiled.Validate(v)
			if tc.ok && err != nil {
				t.Fatalf("want accepted, got %v", err)
			}
			if !tc.ok && err == nil {
				t.Fatal("want rejected, got accepted")
			}
		})
	}
}

// A non-boolean flag is a misconfiguration, not a silent "off".
func TestCompileSchema_AssertFormatNonBoolRefused(t *testing.T) {
	t.Parallel()
	_, err := compileSchema([]byte(`{"type":"object","x-assert-format":"true"}`))
	if err == nil || !strings.Contains(err.Error(), "x-assert-format must be a boolean") {
		t.Fatalf("want non-boolean refusal, got %v", err)
	}
}

// Every version embedded before #2930 keeps its acceptance: none of them
// opts in. A new version that opts in is added to this list deliberately.
func TestEmbeddedSchemas_AssertFormatOptIns(t *testing.T) {
	t.Parallel()
	optedIn := map[string]bool{}
	visited := 0
	err := fs.WalkDir(PlatformSchemasFS(), ".", func(path string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() || !strings.HasSuffix(path, ".json") {
			return err
		}
		b, err := fs.ReadFile(PlatformSchemasFS(), path)
		if err != nil {
			return err
		}
		var doc map[string]any
		if err := json.Unmarshal(b, &doc); err != nil {
			return err
		}
		visited++
		if doc[assertFormatKey] == true {
			optedIn[path] = true
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if visited == 0 {
		t.Fatal("walked no embedded schemas")
	}
	want := map[string]bool{}
	for p := range optedIn {
		if !want[p] {
			t.Errorf("%s sets %s but is not in the expected opt-in list", p, assertFormatKey)
		}
	}
}
