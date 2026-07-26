package crosswalkconflict

// Pure-Go unit suite for the slice-536a conflict heuristics (slice 353 Q-2
// convention: no build tag, no Postgres, fast t.Parallel() table tests). Every
// branch of every heuristic is reachable from an in-memory fixture because
// Detect is a pure function over a catalog-shaped Input — which is also the
// threat-model-I mitigation (decisions log D1).

import (
	"fmt"
	"reflect"
	"sort"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/mgoodric/security-atlas/internal/db/dbx"
)

// id returns a deterministic fixture UUID. Fixed ids keep the assertions on
// ordering meaningful — a random uuid.New() would make output order untestable.
func id(n int) uuid.UUID {
	return uuid.MustParse(fmt.Sprintf("00000000-0000-0000-0000-%012d", n))
}

// reqA is the requirement under test in the single-requirement tables.
var reqA = Requirement{ID: id(1), Code: "A.5.15"}

// edge builds an Edge for reqA. n seeds both the edge id and the anchor id so
// each call is a distinct anchor unless the caller reuses n.
func edge(n int, family, scfID string, rel RelationshipType, strength float64) Edge {
	return Edge{
		ID:               id(1000 + n),
		RequirementID:    reqA.ID,
		RequirementCode:  reqA.Code,
		AnchorID:         id(2000 + n),
		AnchorSCFID:      scfID,
		AnchorFamily:     family,
		RelationshipType: rel,
		Strength:         strength,
	}
}

// reasons extracts the finding reasons in output order.
func reasons(cs []Conflict) []Reason {
	out := make([]Reason, 0, len(cs))
	for _, c := range cs {
		out = append(out, c.Reason)
	}
	return out
}

// sortedReasons is for assertions where the set matters but the order does not.
func sortedReasons(cs []Conflict) []Reason {
	out := reasons(cs)
	sort.Slice(out, func(i, j int) bool { return out[i] < out[j] })
	return out
}

func TestDetectCompetingAnchors(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name  string
		edges []Edge
		want  []Reason
	}{
		{
			// Canvas 3.1: anchors are semantic-equivalence classes, so a
			// requirement cannot be `equal` to two of them. Not family-scoped —
			// the argument does not weaken across families.
			name: "two equal claims in different families",
			edges: []Edge{
				edge(1, "IAC", "IAC-22", RelEqual, 1.0),
				edge(2, "CRY", "CRY-03", RelEqual, 1.0),
			},
			want: []Reason{ReasonDuplicateEqualClaim},
		},
		{
			// Rule 2 is suppressed when the family group is all-`equal`: rule 1
			// already reported that exact edge set at higher severity (D2 dedup).
			name: "two equal claims in one family reports once, not twice",
			edges: []Edge{
				edge(1, "IAC", "IAC-01", RelEqual, 1.0),
				edge(2, "IAC", "IAC-22", RelEqual, 1.0),
			},
			want: []Reason{ReasonDuplicateEqualClaim},
		},
		{
			name: "two subset_of claims in one family",
			edges: []Edge{
				edge(1, "IAC", "IAC-01", RelSubsetOf, 0.8),
				edge(2, "IAC", "IAC-22", RelSubsetOf, 0.8),
			},
			want: []Reason{ReasonDuplicateTotalClaimInFamily},
		},
		{
			name: "mixed equal and subset_of in one family",
			edges: []Edge{
				edge(1, "IAC", "IAC-01", RelEqual, 1.0),
				edge(2, "IAC", "IAC-22", RelSubsetOf, 1.0),
			},
			want: []Reason{ReasonDuplicateTotalClaimInFamily},
		},
		{
			// The load-bearing non-finding: a requirement spanning several SCF
			// domains is the normal case, not a conflict.
			name: "subset_of claims across different families is not a conflict",
			edges: []Edge{
				edge(1, "IAC", "IAC-22", RelSubsetOf, 0.8),
				edge(2, "CRY", "CRY-03", RelSubsetOf, 0.8),
			},
			want: []Reason{},
		},
		{
			name: "multiple intersects_with in one family is not a competing claim",
			edges: []Edge{
				edge(1, "IAC", "IAC-01", RelIntersectsWith, 0.5),
				edge(2, "IAC", "IAC-22", RelIntersectsWith, 0.5),
			},
			want: []Reason{},
		},
		{
			name:  "single equal edge",
			edges: []Edge{edge(1, "IAC", "IAC-22", RelEqual, 1.0)},
			want:  []Reason{},
		},
		{
			// The DB's UNIQUE (requirement, anchor) makes this unreachable from
			// a query, but a hand-built or merged Input must not inflate the
			// count with what is really one anchor.
			name: "duplicate rows for one anchor do not compete with themselves",
			edges: []Edge{
				{ID: id(11), RequirementID: reqA.ID, RequirementCode: reqA.Code,
					AnchorID: id(99), AnchorSCFID: "IAC-22", AnchorFamily: "IAC",
					RelationshipType: RelEqual, Strength: 1.0},
				{ID: id(12), RequirementID: reqA.ID, RequirementCode: reqA.Code,
					AnchorID: id(99), AnchorSCFID: "IAC-22", AnchorFamily: "IAC",
					RelationshipType: RelEqual, Strength: 1.0},
			},
			want: []Reason{},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got := filterKind(Detect(Input{Requirements: []Requirement{reqA}, Edges: tc.edges}), KindCompetingAnchors)
			if !reflect.DeepEqual(sortedReasons(got), tc.want) {
				t.Fatalf("reasons = %v, want %v (findings: %+v)", sortedReasons(got), tc.want, got)
			}
		})
	}
}

func TestDetectContradictoryStrengthPerEdge(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		edge     Edge
		want     []Reason
		severity Severity
	}{
		{
			// Canvas 3.2 calls no_relationship "confirmed no overlap"; positive
			// strength on it is a straight contradiction that still feeds a
			// nonzero term into the slice-482 rollup.
			name:     "no_relationship carrying strength",
			edge:     edge(1, "IAC", "IAC-22", RelNoRelationship, 0.6),
			want:     []Reason{ReasonNoRelationshipWithStrength},
			severity: SeverityHigh,
		},
		{
			name:     "equal below the full-strength floor",
			edge:     edge(1, "IAC", "IAC-22", RelEqual, 0.6),
			want:     []Reason{ReasonEqualBelowFullStrength},
			severity: SeverityMedium,
		},
		{
			name:     "intersects_with at full strength",
			edge:     edge(1, "IAC", "IAC-22", RelIntersectsWith, 1.0),
			want:     []Reason{ReasonIntersectsAtFullStrength},
			severity: SeverityMedium,
		},
		{
			name:     "asserted relationship worth zero",
			edge:     edge(1, "IAC", "IAC-22", RelSubsetOf, 0.0),
			want:     []Reason{ReasonAssertedRelationshipZeroStrength},
			severity: SeverityMedium,
		},
		{
			// Mutually exclusive arms (D3a): an `equal` at zero is doubly wrong
			// but reports once, as the more actionable zero-strength finding.
			name:     "equal at zero strength reports the zero-strength reason only",
			edge:     edge(1, "IAC", "IAC-22", RelEqual, 0.0),
			want:     []Reason{ReasonAssertedRelationshipZeroStrength},
			severity: SeverityMedium,
		},
		{
			name: "equal exactly at the floor is coherent",
			edge: edge(1, "IAC", "IAC-22", RelEqual, equalStrengthFloor),
			want: []Reason{},
		},
		{name: "equal at full strength is coherent", edge: edge(1, "IAC", "IAC-22", RelEqual, 1.0), want: []Reason{}},
		{name: "intersects_with partial is coherent", edge: edge(1, "IAC", "IAC-22", RelIntersectsWith, 0.4), want: []Reason{}},
		{name: "superset_of partial is coherent", edge: edge(1, "IAC", "IAC-22", RelSupersetOf, 0.7), want: []Reason{}},
		{name: "no_relationship at zero is coherent", edge: edge(1, "IAC", "IAC-22", RelNoRelationship, 0.0), want: []Reason{}},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got := filterKind(Detect(Input{Requirements: []Requirement{reqA}, Edges: []Edge{tc.edge}}), KindContradictoryStrength)
			if !reflect.DeepEqual(sortedReasons(got), tc.want) {
				t.Fatalf("reasons = %v, want %v (findings: %+v)", sortedReasons(got), tc.want, got)
			}
			if len(tc.want) == 0 {
				return
			}
			if got[0].Severity != tc.severity {
				t.Errorf("severity = %q, want %q", got[0].Severity, tc.severity)
			}
			if len(got[0].EdgeIDs) != 1 || got[0].EdgeIDs[0] != tc.edge.ID {
				t.Errorf("EdgeIDs = %v, want [%s]", got[0].EdgeIDs, tc.edge.ID)
			}
		})
	}
}

func TestDetectSiblingStrengthDivergence(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name  string
		edges []Edge
		want  []Reason
	}{
		{
			name: "same family same type wide spread",
			edges: []Edge{
				edge(1, "IAC", "IAC-01", RelIntersectsWith, 0.9),
				edge(2, "IAC", "IAC-22", RelIntersectsWith, 0.2),
			},
			want: []Reason{ReasonSiblingStrengthDivergence},
		},
		{
			// The threshold is exclusive: a spread AT the boundary is tolerated.
			name: "spread at the threshold is tolerated",
			edges: []Edge{
				edge(1, "IAC", "IAC-01", RelIntersectsWith, 0.5),
				edge(2, "IAC", "IAC-22", RelIntersectsWith, 0.1),
			},
			want: []Reason{},
		},
		{
			name: "different families are not siblings",
			edges: []Edge{
				edge(1, "IAC", "IAC-01", RelIntersectsWith, 0.9),
				edge(2, "CRY", "CRY-03", RelIntersectsWith, 0.2),
			},
			want: []Reason{},
		},
		{
			name: "different relationship types are not siblings",
			edges: []Edge{
				edge(1, "IAC", "IAC-01", RelIntersectsWith, 0.9),
				edge(2, "IAC", "IAC-22", RelSupersetOf, 0.2),
			},
			want: []Reason{},
		},
		{
			// no_relationship groups are skipped — the per-edge arm owns any
			// positive strength there, so grouping them would double-report.
			name: "no_relationship siblings are skipped",
			edges: []Edge{
				edge(1, "IAC", "IAC-01", RelNoRelationship, 0.0),
				edge(2, "IAC", "IAC-22", RelNoRelationship, 0.9),
			},
			want: []Reason{ReasonNoRelationshipWithStrength},
		},
		{
			name: "three siblings flagged once with every edge attached",
			edges: []Edge{
				edge(1, "IAC", "IAC-01", RelSupersetOf, 0.95),
				edge(2, "IAC", "IAC-22", RelSupersetOf, 0.6),
				edge(3, "IAC", "IAC-33", RelSupersetOf, 0.1),
			},
			want: []Reason{ReasonSiblingStrengthDivergence},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got := filterKind(Detect(Input{Requirements: []Requirement{reqA}, Edges: tc.edges}), KindContradictoryStrength)
			if !reflect.DeepEqual(sortedReasons(got), tc.want) {
				t.Fatalf("reasons = %v, want %v (findings: %+v)", sortedReasons(got), tc.want, got)
			}
		})
	}

	t.Run("finding attaches every sibling edge", func(t *testing.T) {
		t.Parallel()
		edges := []Edge{
			edge(1, "IAC", "IAC-01", RelSupersetOf, 0.95),
			edge(2, "IAC", "IAC-22", RelSupersetOf, 0.6),
			edge(3, "IAC", "IAC-33", RelSupersetOf, 0.1),
		}
		got := filterKind(Detect(Input{Requirements: []Requirement{reqA}, Edges: edges}), KindContradictoryStrength)
		if len(got) != 1 {
			t.Fatalf("want 1 finding, got %d", len(got))
		}
		if len(got[0].EdgeIDs) != 3 {
			t.Errorf("EdgeIDs = %v, want all three sibling edges", got[0].EdgeIDs)
		}
		if !reflect.DeepEqual(got[0].AnchorSCFIDs, []string{"IAC-01", "IAC-22", "IAC-33"}) {
			t.Errorf("AnchorSCFIDs = %v, want the three anchors ascending", got[0].AnchorSCFIDs)
		}
	})
}

func TestDetectOrphanedRequirements(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		edges    []Edge
		want     []Reason
		severity Severity
	}{
		{
			// A loader gap: nobody has looked at this requirement.
			name:     "no edges at all",
			edges:    nil,
			want:     []Reason{ReasonUnmapped},
			severity: SeverityHigh,
		},
		{
			// Mapped on paper only — hides behind a healthy-looking edge count,
			// which is why it needs its own reason rather than folding into
			// unmapped.
			name: "every edge carries zero strength",
			edges: []Edge{
				edge(1, "IAC", "IAC-22", RelSubsetOf, 0.0),
				edge(2, "CRY", "CRY-03", RelIntersectsWith, 0.0),
			},
			want:     []Reason{ReasonZeroStrengthOnly},
			severity: SeverityHigh,
		},
		{
			// A deliberate assertion a reviewer already made — surfaced low so a
			// whole category of intentional data does not read as defects.
			name: "every edge is no_relationship",
			edges: []Edge{
				edge(1, "IAC", "IAC-22", RelNoRelationship, 0.0),
				edge(2, "CRY", "CRY-03", RelNoRelationship, 0.0),
			},
			want:     []Reason{ReasonExplicitlyUnmapped},
			severity: SeverityLow,
		},
		{
			name:  "one real anchoring edge is enough",
			edges: []Edge{edge(1, "IAC", "IAC-22", RelSubsetOf, 0.8)},
			want:  []Reason{},
		},
		{
			name: "a single anchoring edge alongside a no_relationship edge",
			edges: []Edge{
				edge(1, "IAC", "IAC-22", RelSubsetOf, 0.8),
				edge(2, "CRY", "CRY-03", RelNoRelationship, 0.0),
			},
			want: []Reason{},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got := filterKind(Detect(Input{Requirements: []Requirement{reqA}, Edges: tc.edges}), KindOrphanedRequirement)
			if !reflect.DeepEqual(sortedReasons(got), tc.want) {
				t.Fatalf("reasons = %v, want %v (findings: %+v)", sortedReasons(got), tc.want, got)
			}
			if len(tc.want) == 0 {
				return
			}
			if got[0].Severity != tc.severity {
				t.Errorf("severity = %q, want %q", got[0].Severity, tc.severity)
			}
			if got[0].RequirementCode != reqA.Code {
				t.Errorf("RequirementCode = %q, want %q", got[0].RequirementCode, reqA.Code)
			}
		})
	}

	t.Run("unmapped cannot fire without the requirement inventory", func(t *testing.T) {
		t.Parallel()
		// The documented consequence of omitting Input.Requirements: a
		// requirement with zero edges produces zero rows in any edge query and
		// is therefore invisible.
		if got := Detect(Input{}); len(got) != 0 {
			t.Fatalf("want no findings from an empty input, got %+v", got)
		}
	})

	t.Run("requirement seen only through its edges is still analysed", func(t *testing.T) {
		t.Parallel()
		got := Detect(Input{Edges: []Edge{
			edge(1, "IAC", "IAC-01", RelEqual, 1.0),
			edge(2, "CRY", "CRY-03", RelEqual, 1.0),
		}})
		if !reflect.DeepEqual(sortedReasons(got), []Reason{ReasonDuplicateEqualClaim}) {
			t.Fatalf("reasons = %v, want the edge-scoped finding", sortedReasons(got))
		}
		if got[0].RequirementCode != reqA.Code {
			t.Errorf("RequirementCode = %q, want the code synthesized from the edge", got[0].RequirementCode)
		}
	})
}

// TestDetectAcrossRequirements pins that findings stay scoped to their own
// requirement — a conflict on one requirement never borrows another's edges.
func TestDetectAcrossRequirements(t *testing.T) {
	t.Parallel()

	reqB := Requirement{ID: id(2), Code: "A.8.02"}
	edges := []Edge{
		edge(1, "IAC", "IAC-01", RelEqual, 1.0),
		edge(2, "CRY", "CRY-03", RelEqual, 1.0),
		{ID: id(1010), RequirementID: reqB.ID, RequirementCode: reqB.Code,
			AnchorID: id(2010), AnchorSCFID: "IAC-01", AnchorFamily: "IAC",
			RelationshipType: RelEqual, Strength: 1.0},
	}

	got := Detect(Input{Requirements: []Requirement{reqA, reqB}, Edges: edges})
	if len(got) != 1 {
		t.Fatalf("want exactly the reqA finding, got %d: %+v", len(got), got)
	}
	if got[0].RequirementID != reqA.ID {
		t.Errorf("RequirementID = %s, want reqA %s", got[0].RequirementID, reqA.ID)
	}
	// reqB's single equal edge must not have been pulled into reqA's group.
	for _, eid := range got[0].EdgeIDs {
		if eid == id(1010) {
			t.Errorf("finding borrowed requirement %s's edge", reqB.Code)
		}
	}
}

// TestNoRequirementToRequirementRelationEmitted is the constitutional
// invariant-7 guard (decisions log D7). A finding names exactly one requirement
// and only that requirement's edges — the package has no way to express a
// requirement-to-requirement relation, and this pins that it stays that way.
func TestNoRequirementToRequirementRelationEmitted(t *testing.T) {
	t.Parallel()

	reqB := Requirement{ID: id(2), Code: "A.8.02"}
	edgeOwner := map[uuid.UUID]uuid.UUID{}
	edges := []Edge{
		edge(1, "IAC", "IAC-01", RelEqual, 1.0),
		edge(2, "IAC", "IAC-22", RelEqual, 0.5),
		edge(3, "CRY", "CRY-03", RelNoRelationship, 0.7),
		{ID: id(1010), RequirementID: reqB.ID, RequirementCode: reqB.Code,
			AnchorID: id(2010), AnchorSCFID: "IAC-01", AnchorFamily: "IAC",
			RelationshipType: RelSubsetOf, Strength: 0.0},
	}
	for _, e := range edges {
		edgeOwner[e.ID] = e.RequirementID
	}

	got := Detect(Input{Requirements: []Requirement{reqA, reqB}, Edges: edges})
	if len(got) == 0 {
		t.Fatal("fixture should produce findings")
	}
	for _, c := range got {
		if c.RequirementID == uuid.Nil {
			t.Errorf("finding %s/%s has no requirement", c.Kind, c.Reason)
		}
		for _, eid := range c.EdgeIDs {
			if owner := edgeOwner[eid]; owner != c.RequirementID {
				t.Errorf("finding for requirement %s cites edge %s owned by requirement %s — a requirement-to-requirement relation",
					c.RequirementID, eid, owner)
			}
		}
	}
}

// TestDetectIsDeterministic pins D6: the same catalog must produce the same
// review queue on every call regardless of input order, or the 536b UI
// reshuffles between refreshes.
func TestDetectIsDeterministic(t *testing.T) {
	t.Parallel()

	reqB := Requirement{ID: id(2), Code: "A.8.02"}
	reqC := Requirement{ID: id(3), Code: "A.5.01"}
	edges := []Edge{
		edge(1, "IAC", "IAC-01", RelEqual, 1.0),
		edge(2, "CRY", "CRY-03", RelEqual, 1.0),
		edge(3, "IAC", "IAC-22", RelIntersectsWith, 1.0),
		{ID: id(1010), RequirementID: reqB.ID, RequirementCode: reqB.Code,
			AnchorID: id(2010), AnchorSCFID: "IAC-01", AnchorFamily: "IAC",
			RelationshipType: RelNoRelationship, Strength: 0.4},
	}

	forward := Detect(Input{Requirements: []Requirement{reqA, reqB, reqC}, Edges: edges})

	reversedEdges := make([]Edge, len(edges))
	for i, e := range edges {
		reversedEdges[len(edges)-1-i] = e
	}
	reversed := Detect(Input{Requirements: []Requirement{reqC, reqB, reqA}, Edges: reversedEdges})

	if !reflect.DeepEqual(forward, reversed) {
		t.Fatalf("output depends on input order:\nforward:  %+v\nreversed: %+v", forward, reversed)
	}

	// Output is ordered by requirement code: A.5.01 (reqC, unmapped) precedes
	// A.5.15 (reqA) precedes A.8.02 (reqB).
	var codes []string
	for _, c := range forward {
		codes = append(codes, c.RequirementCode)
	}
	if !sort.StringsAreSorted(codes) {
		t.Errorf("findings are not ordered by requirement code: %v", codes)
	}
}

// TestDetectDoesNotMutateInput pins the purity claim in D1 — Detect sorts
// internally and must not reorder or edit the caller's slices.
func TestDetectDoesNotMutateInput(t *testing.T) {
	t.Parallel()

	edges := []Edge{
		edge(3, "IAC", "IAC-33", RelEqual, 1.0),
		edge(1, "IAC", "IAC-01", RelEqual, 1.0),
	}
	before := make([]Edge, len(edges))
	copy(before, edges)

	reqs := []Requirement{{ID: id(2), Code: "Z.99"}, reqA}
	beforeReqs := make([]Requirement, len(reqs))
	copy(beforeReqs, reqs)

	Detect(Input{Requirements: reqs, Edges: edges})

	if !reflect.DeepEqual(edges, before) {
		t.Errorf("Detect mutated the caller's edge slice:\n got %+v\nwant %+v", edges, before)
	}
	if !reflect.DeepEqual(reqs, beforeReqs) {
		t.Errorf("Detect mutated the caller's requirement slice:\n got %+v\nwant %+v", reqs, beforeReqs)
	}
}

// TestDetectEveryFindingIsDescribed pins that the 536b review queue always has
// something to render: kind, severity and a non-empty human-readable detail.
func TestDetectEveryFindingIsDescribed(t *testing.T) {
	t.Parallel()

	got := Detect(Input{
		Requirements: []Requirement{reqA, {ID: id(2), Code: "A.8.02"}},
		Edges: []Edge{
			edge(1, "IAC", "IAC-01", RelEqual, 1.0),
			edge(2, "IAC", "IAC-22", RelEqual, 1.0),
			edge(3, "CRY", "CRY-03", RelNoRelationship, 0.7),
			edge(4, "CRY", "CRY-04", RelIntersectsWith, 1.0),
		},
	})
	if len(got) == 0 {
		t.Fatal("fixture should produce findings")
	}
	for _, c := range got {
		if c.Kind == "" || c.Reason == "" || c.Severity == "" || c.Detail == "" {
			t.Errorf("under-described finding: %+v", c)
		}
		switch c.Severity {
		case SeverityLow, SeverityMedium, SeverityHigh:
		default:
			t.Errorf("finding %s carries an unknown severity %q", c.Reason, c.Severity)
		}
	}
}

func TestEdgesFromDBRows(t *testing.T) {
	t.Parallel()

	rows := []dbx.ListFwToScfEdgesForRequirementRow{{
		ID:                     pgUUID(id(1000)),
		FrameworkRequirementID: pgUUID(id(1)),
		ScfAnchorID:            pgUUID(id(2000)),
		RelationshipType:       dbx.StrmRelationshipTypeIntersectsWith,
		Strength:               0.45,
		SourceAttribution:      dbx.CrosswalkSourceAttributionCommunityDraft,
		MappingTier:            dbx.CrosswalkMappingTierDraft,
		ScfID:                  "IAC-22",
		Family:                 "IAC",
	}}

	got := EdgesFromDBRows("A.5.15", rows)
	want := []Edge{{
		ID:               id(1000),
		RequirementID:    id(1),
		RequirementCode:  "A.5.15",
		AnchorID:         id(2000),
		AnchorSCFID:      "IAC-22",
		AnchorFamily:     "IAC",
		RelationshipType: RelIntersectsWith,
		Strength:         0.45,
	}}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("EdgesFromDBRows = %+v, want %+v", got, want)
	}
}

func TestRelationshipTypeFromDBCoversEveryEnumValue(t *testing.T) {
	t.Parallel()

	// An unrecognised value must pass through verbatim rather than being
	// coerced to a default — silently rewriting an unknown STRM type would make
	// the heuristics reason over data that is not in the catalog.
	cases := map[dbx.StrmRelationshipType]RelationshipType{
		dbx.StrmRelationshipTypeEqual:           RelEqual,
		dbx.StrmRelationshipTypeSubsetOf:        RelSubsetOf,
		dbx.StrmRelationshipTypeSupersetOf:      RelSupersetOf,
		dbx.StrmRelationshipTypeIntersectsWith:  RelIntersectsWith,
		dbx.StrmRelationshipTypeNoRelationship:  RelNoRelationship,
		dbx.StrmRelationshipType("future_type"): RelationshipType("future_type"),
	}
	for in, want := range cases {
		if got := RelationshipTypeFromDB(in); got != want {
			t.Errorf("RelationshipTypeFromDB(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestRequirementsFromDB(t *testing.T) {
	t.Parallel()

	got := RequirementsFromDB([]dbx.FrameworkRequirement{
		{ID: pgUUID(id(1)), Code: "A.5.15"},
		{ID: pgUUID(id(2)), Code: "A.8.02"},
	})
	want := []Requirement{{ID: id(1), Code: "A.5.15"}, {ID: id(2), Code: "A.8.02"}}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("RequirementsFromDB = %+v, want %+v", got, want)
	}
	if got := RequirementsFromDB(nil); len(got) != 0 {
		t.Errorf("RequirementsFromDB(nil) = %+v, want empty", got)
	}
}

func filterKind(cs []Conflict, k Kind) []Conflict {
	out := make([]Conflict, 0, len(cs))
	for _, c := range cs {
		if c.Kind == k {
			out = append(out, c)
		}
	}
	return out
}

func pgUUID(u uuid.UUID) pgtype.UUID { return pgtype.UUID{Bytes: u, Valid: true} }
