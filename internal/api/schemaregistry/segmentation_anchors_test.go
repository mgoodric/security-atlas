package schemaregistry

import (
	"slices"
	"testing"
)

// TestSegmentationKindsDefaultAnchors pins the network evidence kinds'
// x-default-scf-anchors to Matt's 2026-11-18 call on #2396: NetworkPolicy
// coverage is segmentation evidence (NET-06), an NSG is both boundary and
// segmentation evidence, and Azure Firewall stays boundary-only.
func TestSegmentationKindsDefaultAnchors(t *testing.T) {
	t.Parallel()
	want := map[string][]string{
		"k8s.networkpolicy_coverage.v1": {"NET-06", "NET-02"},
		"azure.nsg_rules.v1":            {"NET-03", "NET-06", "NET-02"},
		"azure.firewall_rules.v1":       {"NET-03", "NET-02"},
	}
	schemas, err := LoadPlatformSchemas(PlatformSchemasFS())
	if err != nil {
		t.Fatalf("LoadPlatformSchemas: %v", err)
	}
	for _, s := range schemas {
		w, ok := want[s.Kind]
		if !ok || s.Semver != "1.0.0" {
			continue
		}
		delete(want, s.Kind)
		if !slices.Equal(s.DefaultSCFAnchors, w) {
			t.Errorf("%s x-default-scf-anchors = %v, want %v", s.Kind, s.DefaultSCFAnchors, w)
		}
	}
	for kind := range want {
		t.Errorf("schema %s 1.0.0 not loaded", kind)
	}
}
