package frameworkversion

import "sort"

// MatchKind classifies why a requirement is in the migration review queue.
// Values mirror the framework_version_migration_match_kind Postgres enum.
type MatchKind string

const (
	// MatchExactCode is the ONLY auto-suggested 1:1 carryover (ADR 0019 §3):
	// the requirement code is present in BOTH versions. The reviewer approves
	// the carryover or rejects it.
	MatchExactCode MatchKind = "exact_code"
	// MatchAdded flags a code present in the TO version but absent from the
	// FROM version — a new (or renamed/split) requirement for human review.
	MatchAdded MatchKind = "added"
	// MatchRemoved flags a code present in the FROM version but absent from the
	// TO version — a removed (or renamed/merged) requirement for human review.
	MatchRemoved MatchKind = "removed"
)

// RequirementRef is one (id, code) pair from a framework version's requirement
// set — the minimal input the suggest engine needs.
type RequirementRef struct {
	ID   string
	Code string
}

// Suggestion is one computed review-queue entry BEFORE it is persisted. The
// store turns each Suggestion into a framework_version_migrations row.
//
//	MatchExactCode -> FromID and ToID both set.
//	MatchAdded     -> ToID set, FromID empty.
//	MatchRemoved   -> FromID set, ToID empty.
type Suggestion struct {
	Code      string
	MatchKind MatchKind
	FromID    string
	ToID      string
}

// Suggest computes the migration suggestions between two adjacent versions of
// one framework using EXACT requirement-code matching only (ADR 0019 §3). It is
// pure: no DB, no I/O, deterministic (output sorted by match-kind then code).
//
//   - A code in BOTH sets -> MatchExactCode (the 1:1 carryover to approve).
//   - A code only in `to` -> MatchAdded (flag for review).
//   - A code only in `from` -> MatchRemoved (flag for review).
//
// There is intentionally NO title-similarity / fuzzy pass: a renamed/split/
// merged requirement surfaces as an added+removed pair the human reconciles
// (ADR 0019 §3 / P0-484-1 — precision over recall). The engine NEVER applies a
// suggestion; it only proposes (the store writes 'pending' queue rows).
func Suggest(from, to []RequirementRef) []Suggestion {
	fromByCode := make(map[string]string, len(from)) // code -> id
	for _, r := range from {
		fromByCode[r.Code] = r.ID
	}
	toByCode := make(map[string]string, len(to))
	for _, r := range to {
		toByCode[r.Code] = r.ID
	}

	out := make([]Suggestion, 0, len(from)+len(to))

	// Exact-code carryovers + removed (present in from).
	for _, r := range from {
		if toID, ok := toByCode[r.Code]; ok {
			out = append(out, Suggestion{
				Code:      r.Code,
				MatchKind: MatchExactCode,
				FromID:    r.ID,
				ToID:      toID,
			})
		} else {
			out = append(out, Suggestion{
				Code:      r.Code,
				MatchKind: MatchRemoved,
				FromID:    r.ID,
			})
		}
	}
	// Added (present in to only).
	for _, r := range to {
		if _, ok := fromByCode[r.Code]; !ok {
			out = append(out, Suggestion{
				Code:      r.Code,
				MatchKind: MatchAdded,
				ToID:      r.ID,
			})
		}
	}

	sort.Slice(out, func(i, j int) bool {
		if out[i].MatchKind != out[j].MatchKind {
			return out[i].MatchKind < out[j].MatchKind
		}
		return out[i].Code < out[j].Code
	})
	return out
}

// SuggestSummary tallies a suggestion set by kind. Convenience for the CLI/admin
// report ("12 exact carryovers, 3 added, 1 removed") and for assertions.
type SuggestSummary struct {
	ExactCode int
	Added     int
	Removed   int
}

// Summarize counts a suggestion slice by match kind.
func Summarize(suggestions []Suggestion) SuggestSummary {
	var s SuggestSummary
	for _, sug := range suggestions {
		switch sug.MatchKind {
		case MatchExactCode:
			s.ExactCode++
		case MatchAdded:
			s.Added++
		case MatchRemoved:
			s.Removed++
		}
	}
	return s
}
