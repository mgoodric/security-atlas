package control

import (
	"strings"
	"testing"
)

// Issue #1908: only the top-level `description` may differ for an upload
// to update the active control in place.
func TestManifestsEqualIgnoringDescription(t *testing.T) {
	t.Parallel()

	const base = `bundle_schema_version: "1"
bundle_id: c1
title: "Test control"
description: "Original prose."
scf_anchor_id: IAC-37
implementation_type: automated
evidence_queries:
  - id: q1
    language: rego
    expression: "allow"
    description: "query prose"
`
	cases := []struct {
		name string
		next string
		want bool
	}{
		{"identical", base, true},
		{"description reworded", replace(base, `description: "Original prose."`, `description: "Rewritten prose."`), true},
		{"description reflowed to block scalar", replace(base, `description: "Original prose."`, "description: >-\n  Original\n  prose."), true},
		{"description removed", replace(base, "description: \"Original prose.\"\n", ""), true},
		{"key order and comments", "# a comment\n" + replace(base, "title: \"Test control\"\n", "") + "title: \"Test control\"\n", true},
		{"title changed", replace(base, `title: "Test control"`, `title: "Renamed"`), false},
		{"implementation type changed", replace(base, "implementation_type: automated", "implementation_type: semi_automated"), false},
		{"nested query description changed", replace(base, `description: "query prose"`, `description: "other prose"`), false},
		{"field added", base + "owner_role: ciso\n", false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got, err := manifestsEqualIgnoringDescription([]byte(base), []byte(tc.next))
			if err != nil {
				t.Fatalf("compare: %v", err)
			}
			if got != tc.want {
				t.Fatalf("got %v, want %v", got, tc.want)
			}
		})
	}
}

func TestManifestsEqualIgnoringDescription_MalformedYAML(t *testing.T) {
	t.Parallel()
	if _, err := manifestsEqualIgnoringDescription([]byte("a: [1"), []byte("a: 1")); err == nil {
		t.Fatal("expected an error for malformed prior YAML")
	}
	if _, err := manifestsEqualIgnoringDescription([]byte("a: 1"), []byte("a: [1")); err == nil {
		t.Fatal("expected an error for malformed new YAML")
	}
}

func replace(s, old, new string) string {
	if !strings.Contains(s, old) {
		panic("replace: substring not found: " + old)
	}
	return strings.Replace(s, old, new, 1)
}
