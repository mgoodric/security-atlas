package schemaregistry

import (
	"io/fs"
	"testing"
)

// #2912: osquery.host_posture 1.1.0 adds last_seen_at + reason and must stay
// additive over 1.0.0, so 1.0.0 payloads keep validating under kind .v1.
func TestOsqueryHostPosture110_AdditiveOver100(t *testing.T) {
	t.Parallel()
	read := func(v string) []byte {
		t.Helper()
		b, err := fs.ReadFile(PlatformSchemasFS(), "osquery.host_posture/"+v+".json")
		if err != nil {
			t.Fatalf("read %s: %v", v, err)
		}
		return b
	}
	if err := CheckAdditiveOver(read("1.0.0"), read("1.1.0")); err != nil {
		t.Fatalf("1.1.0 is not additive over 1.0.0: %v", err)
	}
}
