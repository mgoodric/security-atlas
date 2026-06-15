package frameworkversion

import "testing"

// Pure-Go unit tests for the exact-code-match suggestion engine (slice 353 Q-2
// pure-Go-pre-DB convention). No Postgres, no build tag. These pin the
// load-bearing JUDGMENT call: exact requirement-code match ONLY (ADR 0019 §3) —
// a shared code is a 1:1 carryover; everything else is flagged, never
// auto-matched by title fuzz.

func ref(id, code string) RequirementRef { return RequirementRef{ID: id, Code: code} }

func TestSuggest_ExactCodeCarryover(t *testing.T) {
	t.Parallel()
	from := []RequirementRef{ref("f1", "CC6.1"), ref("f2", "CC6.2")}
	to := []RequirementRef{ref("t1", "CC6.1"), ref("t2", "CC6.2")}

	got := Suggest(from, to)
	s := Summarize(got)
	if s.ExactCode != 2 || s.Added != 0 || s.Removed != 0 {
		t.Fatalf("want 2 exact / 0 added / 0 removed, got %+v", s)
	}
	for _, sug := range got {
		if sug.MatchKind != MatchExactCode {
			t.Errorf("expected exact_code, got %s for %s", sug.MatchKind, sug.Code)
		}
		if sug.FromID == "" || sug.ToID == "" {
			t.Errorf("exact_code %s must carry both ids, got from=%q to=%q", sug.Code, sug.FromID, sug.ToID)
		}
	}
}

func TestSuggest_FlagsAddedAndRemoved(t *testing.T) {
	t.Parallel()
	// CC6.1 carries over; CC6.2 removed (only in from); CC6.3 added (only in to).
	from := []RequirementRef{ref("f1", "CC6.1"), ref("f2", "CC6.2")}
	to := []RequirementRef{ref("t1", "CC6.1"), ref("t3", "CC6.3")}

	s := Summarize(Suggest(from, to))
	if s.ExactCode != 1 {
		t.Errorf("want 1 exact carryover, got %d", s.ExactCode)
	}
	if s.Removed != 1 {
		t.Errorf("want 1 removed (CC6.2), got %d", s.Removed)
	}
	if s.Added != 1 {
		t.Errorf("want 1 added (CC6.3), got %d", s.Added)
	}
}

func TestSuggest_RenameSurfacesAsAddedPlusRemoved(t *testing.T) {
	t.Parallel()
	// A renamed requirement (CC6.1 -> CC6.1a) is NOT fuzzily matched: it
	// surfaces as one removed + one added for the human to reconcile (ADR 0019
	// §3 — precision over recall, no title-similarity pass).
	from := []RequirementRef{ref("f1", "CC6.1")}
	to := []RequirementRef{ref("t1", "CC6.1a")}

	got := Suggest(from, to)
	s := Summarize(got)
	if s.ExactCode != 0 {
		t.Errorf("a rename must NOT auto-match: want 0 exact, got %d", s.ExactCode)
	}
	if s.Removed != 1 || s.Added != 1 {
		t.Errorf("rename must surface as 1 added + 1 removed, got added=%d removed=%d", s.Added, s.Removed)
	}
}

func TestSuggest_Deterministic(t *testing.T) {
	t.Parallel()
	from := []RequirementRef{ref("f3", "CC6.3"), ref("f1", "CC6.1"), ref("f2", "CC6.2")}
	to := []RequirementRef{ref("t1", "CC6.1"), ref("t9", "CC9.9")}

	first := Suggest(from, to)
	second := Suggest(from, to)
	if len(first) != len(second) {
		t.Fatalf("non-deterministic length: %d vs %d", len(first), len(second))
	}
	for i := range first {
		if first[i] != second[i] {
			t.Errorf("non-deterministic at %d: %+v vs %+v", i, first[i], second[i])
		}
	}
	// Output is sorted by (match_kind, code): added < exact_code < removed.
	for i := 1; i < len(first); i++ {
		a, b := first[i-1], first[i]
		if a.MatchKind > b.MatchKind || (a.MatchKind == b.MatchKind && a.Code > b.Code) {
			t.Errorf("not sorted at %d: %+v before %+v", i, a, b)
		}
	}
}

func TestSuggest_EmptyInputs(t *testing.T) {
	t.Parallel()
	if got := Suggest(nil, nil); len(got) != 0 {
		t.Errorf("empty inputs must yield no suggestions, got %d", len(got))
	}
	// All-new (empty from): every to-row flagged added.
	s := Summarize(Suggest(nil, []RequirementRef{ref("t1", "A"), ref("t2", "B")}))
	if s.Added != 2 || s.ExactCode != 0 || s.Removed != 0 {
		t.Errorf("all-new must be 2 added, got %+v", s)
	}
}
