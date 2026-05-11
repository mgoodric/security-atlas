package schemaregistry

import (
	"strings"
	"testing"
)

// TestLoadPlatformSchemas_IncludesSlice044 asserts slice 044's two new
// schemas are picked up by the embedded FS without manual registration.
func TestLoadPlatformSchemas_IncludesSlice044(t *testing.T) {
	schemas, err := LoadPlatformSchemas(PlatformSchemasFS())
	if err != nil {
		t.Fatalf("LoadPlatformSchemas: %v", err)
	}
	want := map[string]bool{
		"github.audit_event.v1": false,
		"github.scim_user.v1":   false,
	}
	for _, s := range schemas {
		if _, ok := want[s.Kind]; ok {
			want[s.Kind] = true
			if s.Semver != "1.0.0" {
				t.Errorf("%s semver = %q; want 1.0.0", s.Kind, s.Semver)
			}
			if s.Owner != "platform" {
				t.Errorf("%s owner = %q; want platform", s.Kind, s.Owner)
			}
			// JSON Schema must declare required at top.
			if !strings.Contains(string(s.SchemaJSON), "\"required\"") {
				t.Errorf("%s schema_json missing required[] block", s.Kind)
			}
		}
	}
	for kind, found := range want {
		if !found {
			t.Errorf("kind %s not discovered by LoadPlatformSchemas", kind)
		}
	}
}
