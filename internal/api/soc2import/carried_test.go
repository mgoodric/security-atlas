package soc2import

import (
	"strings"
	"testing"
)

// Pure-Go coverage for decideCarried (#3065); the integration tests in
// carried_integration_test.go run it against real remaps.

func bid(b byte) [16]byte { return [16]byte{b} }

var (
	ownedSource = "yaml:soc2.yaml"
	otherSource = "yaml:other.yaml"
	remapSource = "scf-remap:p"
	req1        = bid(0xAA)
)

func ownsYAML(s *string, attribution string) bool {
	return s != nil && *s == ownedSource && attribution == "community_draft"
}

func listedSet(codes ...string) func([16]byte, string) bool {
	return func(r [16]byte, c string) bool {
		if r != req1 {
			return false
		}
		for _, x := range codes {
			if x == c {
				return true
			}
		}
		return false
	}
}

// copyOf is an edge carried from a root edge with the given source and state.
func copyOf(edge, root byte, code, rootCode string, retired, rootRetired bool, rootSource *string) carriedEdge {
	return carriedEdge{
		id: bid(edge), requirementID: req1, scfID: code, source: &remapSource, retired: retired,
		sourceID: bid(root), sourceSCFID: rootCode, sourceAttribution: "community_draft",
		sourceCrosswalkSource: rootSource, sourceRetired: rootRetired,
	}
}

func TestDecideCarried(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name   string
		edges  []carriedEdge
		listed []string
		want   map[byte]carriedDecision
	}{
		{
			name:  "root dropped retires the copy",
			edges: []carriedEdge{copyOf(1, 9, "GOV-04", "GOV-04", false, true, &ownedSource)},
			want:  map[byte]carriedDecision{1: carriedRetire},
		},
		{
			name:  "root live leaves a live copy",
			edges: []carriedEdge{copyOf(1, 9, "GOV-04", "GOV-04", false, false, &ownedSource)},
			want:  map[byte]carriedDecision{1: carriedLeave},
		},
		{
			name:   "root retired but its code is still listed (moved release) keeps the copy",
			edges:  []carriedEdge{copyOf(1, 9, "GOV-04", "GOV-04", false, true, &ownedSource)},
			listed: []string{"GOV-04"},
			want:   map[byte]carriedDecision{1: carriedLeave},
		},
		{
			name:   "copy's own code listed keeps it",
			edges:  []carriedEdge{copyOf(1, 9, "IAC-21", "IAC-01", false, true, &ownedSource)},
			listed: []string{"IAC-21"},
			want:   map[byte]carriedDecision{1: carriedLeave},
		},
		{
			name:  "root another source wrote is not this import's to follow",
			edges: []carriedEdge{copyOf(1, 9, "GOV-04", "GOV-04", false, true, &otherSource)},
			want:  map[byte]carriedDecision{1: carriedLeave},
		},
		{
			name:  "retired copy of a live root is restored",
			edges: []carriedEdge{copyOf(1, 9, "GOV-04", "GOV-04", true, false, &ownedSource)},
			want:  map[byte]carriedDecision{1: carriedRestore},
		},
		{
			name:  "retired copy of a dropped root stays retired",
			edges: []carriedEdge{copyOf(1, 9, "GOV-04", "GOV-04", true, true, &ownedSource)},
			want:  map[byte]carriedDecision{1: carriedLeave},
		},
		{
			// 9 (root) -> 1 -> 2: 2's source is 1, a copy, whose root is 9.
			name: "chain of remaps follows to the root",
			edges: []carriedEdge{
				copyOf(1, 9, "GOV-04", "GOV-04", false, true, &ownedSource),
				{
					id: bid(2), requirementID: req1, scfID: "GOV-04", source: &remapSource,
					sourceID: bid(1), sourceSCFID: "GOV-04", sourceAttribution: "community_draft",
					sourceCrosswalkSource: &remapSource,
				},
			},
			want: map[byte]carriedDecision{1: carriedRetire, 2: carriedRetire},
		},
		{
			name:   "a chain code listed anywhere keeps every copy",
			listed: []string{"GOV-04"},
			edges: []carriedEdge{
				copyOf(1, 9, "GOV-04", "GOV-04", false, true, &ownedSource),
				{
					id: bid(2), requirementID: req1, scfID: "GOV-05", source: &remapSource,
					sourceID: bid(1), sourceSCFID: "GOV-04", sourceAttribution: "community_draft",
					sourceCrosswalkSource: &remapSource,
				},
			},
			want: map[byte]carriedDecision{1: carriedLeave, 2: carriedLeave},
		},
		{
			name: "a cycle has no root and is left alone",
			edges: []carriedEdge{
				{id: bid(1), requirementID: req1, scfID: "A", source: &remapSource, sourceID: bid(2), sourceSCFID: "B", sourceAttribution: "community_draft", sourceCrosswalkSource: &remapSource, sourceRetired: true},
				{id: bid(2), requirementID: req1, scfID: "B", source: &remapSource, sourceID: bid(1), sourceSCFID: "A", sourceAttribution: "community_draft", sourceCrosswalkSource: &remapSource, sourceRetired: true},
			},
			want: map[byte]carriedDecision{1: carriedLeave, 2: carriedLeave},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got := decideCarried(tc.edges, ownsYAML, listedSet(tc.listed...))
			for b, want := range tc.want {
				if got[bid(b)] != want {
					t.Errorf("edge %d: decision %d; want %d", b, got[bid(b)], want)
				}
			}
		})
	}
}

func TestCarriedNotes(t *testing.T) {
	t.Parallel()
	// The marker is a stable format: carriedRetiredBy matches on it, so a
	// change here strands copies already retired under the old one.
	if got, want := carriedRetireMarker("yaml:soc2.yaml"), `carried-retire:"yaml:soc2.yaml"`; got != want {
		t.Errorf("carriedRetireMarker = %q; want %q", got, want)
	}
	if got, want := carriedRetireMarker(""), `carried-retire:"`+LegacySource+`"`; got != want {
		t.Errorf("carriedRetireMarker(empty) = %q; want %q", got, want)
	}
	note := carriedRetireNote("yaml:soc2.yaml")
	if !strings.HasPrefix(note, carriedRetireMarker("yaml:soc2.yaml")+" ") {
		t.Errorf("carriedRetireNote = %q; want the marker, a space, then human text", note)
	}
	cases := []struct {
		note, source string
		want         bool
	}{
		{note, "yaml:soc2.yaml", true},
		{`carried-retire:"yaml:soc2.yaml" any wording at all`, "yaml:soc2.yaml", true},
		{note, "yaml:soc2", false},
		{carriedRetireNote("yaml:a"), "yaml:ab", false},
		{carriedRetireNote("yaml:ab"), "yaml:a", false},
		{carriedRestoreNote("yaml:soc2.yaml"), "yaml:soc2.yaml", false},
		{retireNote("yaml:soc2.yaml"), "yaml:soc2.yaml", false},
		{"", "yaml:soc2.yaml", false},
	}
	for _, tc := range cases {
		if got := carriedRetiredBy(tc.note, tc.source); got != tc.want {
			t.Errorf("carriedRetiredBy(%q, %q) = %v; want %v", tc.note, tc.source, got, tc.want)
		}
	}
}

func TestDecideCarriedSync(t *testing.T) {
	t.Parallel()
	stored := strmContent{relType: "intersects_with", strength: 0.7, attribution: "community_draft", rationale: "old"}
	changed := strmContent{relType: "subset_of", strength: 0.9, attribution: "community_draft", rationale: "new"}
	weaker := changed
	weaker.attribution = "org_internal"
	stronger := changed
	stronger.attribution = "scf_official"
	cases := []struct {
		name       string
		row        strmContent
		listed     bool
		humanMoved bool
		want       carriedSync
	}{
		{"not listed", changed, false, false, carriedSyncNone},
		{"same content", stored, true, false, carriedSyncNone},
		{"changed content", changed, true, false, carriedSyncApply},
		{"stronger attribution", stronger, true, false, carriedSyncApply},
		{"weaker attribution", weaker, true, false, carriedSyncHeldBack},
		{"moved by a person", changed, true, true, carriedSyncHeldBack},
		{"moved by a person, same content", stored, true, true, carriedSyncNone},
	}
	for _, tc := range cases {
		e := carriedEdge{content: stored, humanMoved: tc.humanMoved}
		if got := decideCarriedSync(e, tc.row, tc.listed); got != tc.want {
			t.Errorf("%s: decideCarriedSync = %v; want %v", tc.name, got, tc.want)
		}
	}
}

func TestCarriedSyncNote(t *testing.T) {
	t.Parallel()
	if got, want := carriedSyncMarker("yaml:soc2.yaml"), `carried-sync:"yaml:soc2.yaml"`; got != want {
		t.Errorf("carriedSyncMarker = %q; want %q", got, want)
	}
	note := carriedSyncNote("yaml:soc2.yaml", strmContent{relType: "intersects_with", strength: 0.7, attribution: "community_draft", rationale: "R-1->GOV-02"})
	if want := `carried-sync:"yaml:soc2.yaml" content set from crosswalk yaml:soc2.yaml; was intersects_with 0.7 community_draft "R-1->GOV-02"`; note != want {
		t.Errorf("carriedSyncNote = %q; want %q", note, want)
	}
	// A sync note never reads as this import's retire note.
	if carriedRetiredBy(note, "yaml:soc2.yaml") {
		t.Errorf("carriedRetiredBy(sync note) = true; want false")
	}
}
