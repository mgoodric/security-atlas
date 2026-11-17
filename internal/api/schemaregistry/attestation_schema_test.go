package schemaregistry

import (
	"encoding/json"
	"io/fs"
	"testing"
)

// #2111: manual.attestation 1.2.0 adds result + fail_reason. A fail needs a
// reason; a payload without result (every 1.1.0 attestation) still
// validates, and 1.2.0 stays additive over 1.1.0.
func TestManualAttestation120_ResultAndFailReason(t *testing.T) {
	t.Parallel()
	read := func(v string) []byte {
		t.Helper()
		b, err := fs.ReadFile(PlatformSchemasFS(), "manual.attestation/"+v+".json")
		if err != nil {
			t.Fatalf("read %s: %v", v, err)
		}
		return b
	}
	v110, v120 := read("1.1.0"), read("1.2.0")
	if err := CheckAdditiveOver(v110, v120); err != nil {
		t.Fatalf("1.2.0 is not additive over 1.1.0: %v", err)
	}
	compiled, err := compileSchema(v120)
	if err != nil {
		t.Fatalf("compile 1.2.0: %v", err)
	}
	cases := []struct {
		name    string
		payload string
		ok      bool
	}{
		{"1.1.0 shape, no result", `{"attestor":"u","statement":"s"}`, true},
		{"pass", `{"attestor":"u","statement":"s","result":"pass"}`, true},
		{"fail with reason", `{"attestor":"u","statement":"s","result":"fail","fail_reason":"r"}`, true},
		{"fail without reason", `{"attestor":"u","statement":"s","result":"fail"}`, false},
		{"fail with empty reason", `{"attestor":"u","statement":"s","result":"fail","fail_reason":""}`, false},
		{"unknown result", `{"attestor":"u","statement":"s","result":"na"}`, false},
	}
	for _, tc := range cases {
		var doc any
		if err := json.Unmarshal([]byte(tc.payload), &doc); err != nil {
			t.Fatalf("%s: %v", tc.name, err)
		}
		err := compiled.Validate(doc)
		if tc.ok && err != nil {
			t.Errorf("%s: rejected: %v", tc.name, err)
		}
		if !tc.ok && err == nil {
			t.Errorf("%s: accepted, want rejected", tc.name)
		}
	}
}
