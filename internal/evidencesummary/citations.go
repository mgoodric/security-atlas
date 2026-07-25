package evidencesummary

import (
	"context"
	"regexp"

	"github.com/google/uuid"
)

// uuidPattern matches a canonical RFC-4122 UUID anywhere in the model text. The
// prompt instructs the model to cite IDs verbatim as canonical UUIDs (see
// prompt.go), so a citation is any UUID-shaped token in the draft. The pattern
// is deliberately strict (8-4-4-4-12 hex) so prose that merely contains hex runs
// does not false-positive into a citation.
var uuidPattern = regexp.MustCompile(
	`[0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{12}`,
)

// parseCitedIDs extracts the distinct, well-formed UUIDs the model text cites,
// preserving first-seen order. Malformed UUID-ish tokens are skipped by the
// pattern; uuid.Parse is the final gate so only canonical UUIDs survive.
//
// Pure function — no IO. The DB-backed tenant-ownership check is
// CitationResolver.Resolve, called by validateCitations.
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

// CitationResolver resolves a candidate cited ID to a tenant-owned row under the
// caller's RLS context, classifying it as a control or an evidence record. It
// returns the resolved Citation and ok=true ONLY when the ID names a real row
// VISIBLE to the requesting tenant. A cross-tenant ID is invisible under RLS, so
// Resolve returns ok=false for it (the load-bearing AC-10 property).
//
// The production implementation is *Store; tests supply a fake to exercise the
// suppression branches deterministically without a live model.
type CitationResolver interface {
	Resolve(ctx context.Context, id uuid.UUID) (Citation, bool, error)
}

// validateCitations is the AC-4 gate. It parses the cited IDs from the draft,
// confirms EVERY one resolves to a real tenant-owned row, and returns the
// validated citation set. The contract is STRICT (the JUDGMENT call, decisions
// log D2): a SINGLE unresolvable citation fails the whole summary — the caller
// suppresses and falls back to the deterministic evidence list. A
// no-fabricated-coverage invariant cannot be "mostly" honored.
//
// allowed is the set of IDs the evidence set put in front of the model (the
// control id + the cited-excerpt evidence ids). It is a SECOND, cheaper gate in
// front of the DB resolution: a cited ID outside this set means the model
// invented or hallucinated an ID that was never in its context, which is a fail
// even if that ID happened to name some other tenant-owned row the operator may
// view. Grounding discipline (threat-model T) is "summarize ONLY what you were
// shown".
//
// Returns ok=false with a reason when:
//   - the draft cites no IDs at all (ReasonNoCitations) — a summary with no
//     grounding is not a cited summary, so it is suppressed.
//   - any cited ID is outside `allowed` or fails to resolve to a tenant-owned
//     row (ReasonUnresolvedCitation).
func validateCitations(
	ctx context.Context,
	res CitationResolver,
	text string,
	allowed map[uuid.UUID]bool,
) ([]Citation, bool, string, error) {
	cited := parseCitedIDs(text)
	if len(cited) == 0 {
		return nil, false, ReasonNoCitations, nil
	}
	out := make([]Citation, 0, len(cited))
	for _, id := range cited {
		// Grounding gate: the model may only cite what the evidence set showed it.
		if !allowed[id] {
			return nil, false, ReasonUnresolvedCitation, nil
		}
		// Tenant-ownership gate: the ID must resolve to a row visible under the
		// caller's RLS. Cross-tenant IDs are invisible here (AC-10).
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

// allowedIDs builds the grounding set from an evidence set: the control id plus
// every cited-excerpt evidence id. This is exactly the set of IDs the prompt put
// in front of the model, so it is exactly the set the model is permitted to
// cite.
func allowedIDs(set EvidenceSet) map[uuid.UUID]bool {
	allowed := make(map[uuid.UUID]bool, len(set.Records)+1)
	allowed[set.ControlID] = true
	for _, e := range set.Records {
		allowed[e.EvidenceID] = true
	}
	return allowed
}
