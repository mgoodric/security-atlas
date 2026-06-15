package boardnarrative

import (
	"context"
	"regexp"

	"github.com/google/uuid"
)

// Mandatory-citation enforcement — guardrail 4. Every factual claim in the
// section cites a real evidence/control id, validated to (a) be in the
// grounding set the prompt put in front of the model AND (b) resolve to a real
// tenant-owned row, BEFORE the operator sees the draft. A single unresolvable
// citation rejects the WHOLE draft. Mirrors internal/qaisuggest/citations.go.

// uuidPattern matches a canonical RFC-4122 UUID anywhere in the model text. The
// prompt instructs the model to cite candidate ids verbatim as canonical UUIDs,
// so a citation is any UUID-shaped token in the draft. The pattern is strict
// (8-4-4-4-12 hex) so prose that merely contains hex runs does not false-
// positive into a citation. Mirrors the qaisuggest / gapexplain pattern.
var uuidPattern = regexp.MustCompile(
	`[0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{12}`,
)

// parseCitedIDs extracts the distinct, well-formed UUIDs the model text cites,
// preserving first-seen order. Malformed UUID-ish tokens are skipped by the
// pattern; uuid.Parse is the final gate so only canonical UUIDs survive. Pure
// function — no IO.
func parseCitedIDs(text string) []uuid.UUID {
	matches := uuidPattern.FindAllString(text, -1)
	seen := make(map[uuid.UUID]bool, len(matches))
	out := make([]uuid.UUID, 0, len(matches))
	for _, m := range matches {
		id, err := uuid.Parse(m)
		if err != nil {
			continue
		}
		if seen[id] {
			continue
		}
		seen[id] = true
		out = append(out, id)
	}
	return out
}

// CitationResolver resolves a candidate cited id to a tenant-owned control or
// evidence row under the caller's RLS context. It returns ok=true ONLY when the
// id names a real row VISIBLE to the requesting tenant. A cross-tenant id is
// invisible under RLS, so Resolve returns ok=false for it (the load-bearing
// AC-18 / threat-model-I property).
//
// Production is *Store; tests supply a fake to exercise the suppression
// branches deterministically without a live model.
type CitationResolver interface {
	Resolve(ctx context.Context, id uuid.UUID) (Citation, bool, error)
}

// validateCitations is the guardrail-4 gate — the no-fabricated-coverage
// enforcement. It parses the cited ids from the draft, confirms EVERY one is in
// the grounding set AND resolves to a real tenant-owned row, and returns the
// validated citation set. STRICT (the decisions-log JUDGMENT call): a SINGLE
// unresolvable citation fails the WHOLE draft — the caller suppresses and
// persists nothing. A no-fabricated-coverage invariant cannot be "mostly"
// honored, and a board narrative is the highest-stakes place to half-honor it.
//
// allowed is the set of excerpt ids the prompt put in front of the model. It is
// a SECOND, cheaper gate in front of the DB resolution: a cited id outside this
// set means the model invented an id that was never in its context — a fail
// even if that id happened to name some other tenant-owned row. Grounding
// discipline (threat-model T) is "cite ONLY what you were shown".
//
// Returns ok=false with a reason when:
//   - the draft cites no ids at all (ReasonNoCitations) — a board claim with no
//     grounding is not a cited claim, so it is suppressed (every coverage
//     section MUST cite the controls/evidence behind its numbers).
//   - any cited id is outside `allowed` or fails to resolve to a tenant-owned
//     row (ReasonUnresolvedCitation).
func validateCitations(
	ctx context.Context,
	res CitationResolver,
	text string,
	allowed map[string]CitationKind,
) ([]Citation, bool, string, error) {
	cited := parseCitedIDs(text)
	if len(cited) == 0 {
		return nil, false, ReasonNoCitations, nil
	}
	out := make([]Citation, 0, len(cited))
	for _, id := range cited {
		// Grounding gate: the model may only cite a retrieved excerpt.
		if _, ok := allowed[id.String()]; !ok {
			return nil, false, ReasonUnresolvedCitation, nil
		}
		// Tenant-ownership gate: the id must resolve to a row visible under the
		// caller's RLS. Cross-tenant ids are invisible here (AC-18).
		c, ok, err := res.Resolve(ctx, id)
		if err != nil {
			return nil, false, ReasonUnresolvedCitation, err
		}
		if !ok {
			return nil, false, ReasonUnresolvedCitation, nil
		}
		out = append(out, c)
	}
	return out, true, "", nil
}
