// Unit tests for the pure filter-matching helpers in internal/decision/store.go.
//
// Load-bearing functions exercised:
//
//   - ValidLinkKind — whitelists the four link kinds; every link mutation
//     guards on it. A drift here would silently accept a kind not backed by
//     a sqlc query, surfacing only when the SQL fails later.
//   - matchesRicherFilters — slice-067 additive filter composition over
//     a List() result set. The function is pure and runs in-memory after
//     the sqlc query returns, so unit coverage of every branch is the
//     authoritative guard against regression of the filter-bar contract.
//   - constraintsIntersect — slice-067 (AC-5) faceted "OR-within-facet"
//     semantics for the ?constraints= filter. Failure modes include the
//     two empty-slice corners (no constraints stored, no constraints
//     requested) and the no-overlap case.
//
// Branches deliberately left to integration:
//
//   - Store.List itself (sqlc query routing) — covered by
//     integration_test.go's matrix.
//   - decisionFromRow — covered transitively by every integration test that
//     reads a row.
//
// Slice 279 — coverage lift target. Pre-lift merged %: 67.8. These tests
// target the in-memory branch surface to clear 70%.

package decision

import (
	"testing"
	"time"
)

func TestValidLinkKind_KnownKinds(t *testing.T) {
	t.Parallel()
	for _, k := range []string{"risks", "controls", "exceptions", "scope_predicates"} {
		k := k
		t.Run(k, func(t *testing.T) {
			t.Parallel()
			if !ValidLinkKind(k) {
				t.Fatalf("ValidLinkKind(%q) = false, want true", k)
			}
		})
	}
}

func TestValidLinkKind_UnknownKinds(t *testing.T) {
	t.Parallel()
	cases := []string{"", "RISKS", "vendors", "policies", "risk", "control"}
	for _, k := range cases {
		k := k
		t.Run(k, func(t *testing.T) {
			t.Parallel()
			if ValidLinkKind(k) {
				t.Fatalf("ValidLinkKind(%q) = true, want false", k)
			}
		})
	}
}

func TestConstraintsIntersect_EmptyHave_ReturnsFalse(t *testing.T) {
	t.Parallel()
	if constraintsIntersect(nil, []string{"cost"}) {
		t.Fatal("intersection with empty have should be false")
	}
}

func TestConstraintsIntersect_EmptyWant_ReturnsFalse(t *testing.T) {
	t.Parallel()
	if constraintsIntersect([]string{"cost"}, nil) {
		t.Fatal("intersection with empty want should be false")
	}
}

func TestConstraintsIntersect_BothEmpty_ReturnsFalse(t *testing.T) {
	t.Parallel()
	if constraintsIntersect(nil, nil) {
		t.Fatal("intersection of two empties should be false")
	}
}

func TestConstraintsIntersect_OneOverlap_ReturnsTrue(t *testing.T) {
	t.Parallel()
	if !constraintsIntersect([]string{"cost", "time"}, []string{"scope", "time"}) {
		t.Fatal("expected intersection on 'time'")
	}
}

func TestConstraintsIntersect_NoOverlap_ReturnsFalse(t *testing.T) {
	t.Parallel()
	if constraintsIntersect([]string{"cost", "time"}, []string{"scope", "people"}) {
		t.Fatal("expected no intersection")
	}
}

func TestConstraintsIntersect_FullOverlap_ReturnsTrue(t *testing.T) {
	t.Parallel()
	tags := []string{"cost", "time", "scope"}
	if !constraintsIntersect(tags, tags) {
		t.Fatal("expected intersection on identical sets")
	}
}

func TestConstraintsIntersect_HaveDuplicates_StillIntersects(t *testing.T) {
	t.Parallel()
	// The dedup is via the set internally; duplicates in have must not
	// suppress a real overlap.
	if !constraintsIntersect([]string{"cost", "cost", "cost"}, []string{"cost"}) {
		t.Fatal("expected intersection despite duplicates in have")
	}
}

// matchesRicherFilters branch matrix. Each branch is an independent
// criterion in the slice-067 contract — they MUST compose via AND, and
// each MUST short-circuit false on a no-match.

func TestMatchesRicherFilters_EmptyFilter_AlwaysTrue(t *testing.T) {
	t.Parallel()
	d := Decision{DecisionMaker: "anyone@example.com"}
	if !matchesRicherFilters(d, ListFilter{}) {
		t.Fatal("empty filter must match every decision")
	}
}

func TestMatchesRicherFilters_ConstraintsHit_ReturnsTrue(t *testing.T) {
	t.Parallel()
	d := Decision{Constraints: []string{"cost", "time"}}
	f := ListFilter{Constraints: []string{"time"}}
	if !matchesRicherFilters(d, f) {
		t.Fatal("expected constraint match")
	}
}

func TestMatchesRicherFilters_ConstraintsMiss_ReturnsFalse(t *testing.T) {
	t.Parallel()
	d := Decision{Constraints: []string{"cost"}}
	f := ListFilter{Constraints: []string{"people"}}
	if matchesRicherFilters(d, f) {
		t.Fatal("expected constraint mismatch")
	}
}

func TestMatchesRicherFilters_DecisionMakerHit_ReturnsTrue(t *testing.T) {
	t.Parallel()
	d := Decision{DecisionMaker: "alice@example.com"}
	f := ListFilter{DecisionMaker: "alice@example.com"}
	if !matchesRicherFilters(d, f) {
		t.Fatal("expected decision-maker match")
	}
}

func TestMatchesRicherFilters_DecisionMakerMiss_ReturnsFalse(t *testing.T) {
	t.Parallel()
	d := Decision{DecisionMaker: "alice@example.com"}
	f := ListFilter{DecisionMaker: "bob@example.com"}
	if matchesRicherFilters(d, f) {
		t.Fatal("expected decision-maker mismatch")
	}
}

func TestMatchesRicherFilters_RevisitWindowHit_ReturnsTrue(t *testing.T) {
	t.Parallel()
	rb := time.Date(2026, 6, 15, 0, 0, 0, 0, time.UTC)
	from := time.Date(2026, 6, 1, 0, 0, 0, 0, time.UTC)
	to := time.Date(2026, 6, 30, 0, 0, 0, 0, time.UTC)
	d := Decision{RevisitBy: &rb}
	f := ListFilter{RevisitByFrom: &from, RevisitByTo: &to}
	if !matchesRicherFilters(d, f) {
		t.Fatal("expected revisit-window match")
	}
}

func TestMatchesRicherFilters_RevisitBeforeWindow_ReturnsFalse(t *testing.T) {
	t.Parallel()
	rb := time.Date(2026, 5, 20, 0, 0, 0, 0, time.UTC)
	from := time.Date(2026, 6, 1, 0, 0, 0, 0, time.UTC)
	d := Decision{RevisitBy: &rb}
	f := ListFilter{RevisitByFrom: &from}
	if matchesRicherFilters(d, f) {
		t.Fatal("expected revisit before-window miss")
	}
}

func TestMatchesRicherFilters_RevisitAfterWindow_ReturnsFalse(t *testing.T) {
	t.Parallel()
	rb := time.Date(2026, 7, 15, 0, 0, 0, 0, time.UTC)
	to := time.Date(2026, 6, 30, 0, 0, 0, 0, time.UTC)
	d := Decision{RevisitBy: &rb}
	f := ListFilter{RevisitByTo: &to}
	if matchesRicherFilters(d, f) {
		t.Fatal("expected revisit after-window miss")
	}
}

func TestMatchesRicherFilters_RevisitWindowSetButNoRevisitBy_ReturnsFalse(t *testing.T) {
	t.Parallel()
	// Slice 067 (AC-5): a decision with no revisit_by is excluded when any
	// bound is set. The window semantics is "fall within"; a null date has
	// nowhere to fall.
	from := time.Date(2026, 6, 1, 0, 0, 0, 0, time.UTC)
	d := Decision{RevisitBy: nil}
	f := ListFilter{RevisitByFrom: &from}
	if matchesRicherFilters(d, f) {
		t.Fatal("expected null revisit_by to be excluded by window filter")
	}
}

func TestMatchesRicherFilters_ComposesAND_AllMatch(t *testing.T) {
	t.Parallel()
	rb := time.Date(2026, 6, 15, 0, 0, 0, 0, time.UTC)
	from := time.Date(2026, 6, 1, 0, 0, 0, 0, time.UTC)
	to := time.Date(2026, 6, 30, 0, 0, 0, 0, time.UTC)
	d := Decision{
		DecisionMaker: "alice@example.com",
		Constraints:   []string{"cost", "time"},
		RevisitBy:     &rb,
	}
	f := ListFilter{
		DecisionMaker: "alice@example.com",
		Constraints:   []string{"time"},
		RevisitByFrom: &from,
		RevisitByTo:   &to,
	}
	if !matchesRicherFilters(d, f) {
		t.Fatal("expected AND-composed filters to all match")
	}
}

func TestMatchesRicherFilters_ComposesAND_OneMisses(t *testing.T) {
	t.Parallel()
	// AND short-circuits — even a single failure means false.
	d := Decision{
		DecisionMaker: "alice@example.com",
		Constraints:   []string{"cost"},
	}
	f := ListFilter{
		DecisionMaker: "alice@example.com",
		Constraints:   []string{"people"}, // does not overlap with 'cost'
	}
	if matchesRicherFilters(d, f) {
		t.Fatal("expected AND-composition to fail when constraints miss")
	}
}
