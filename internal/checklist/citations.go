package checklist

import (
	"context"
	"regexp"
	"strings"

	"github.com/google/uuid"
)

// uuidPattern matches a canonical RFC-4122 UUID anywhere in the model text. The
// prompt instructs the model to cite control/policy ids verbatim as canonical
// UUIDs, so a UUID-shaped token in a task line is a citation. Strict 8-4-4-4-12
// hex so prose containing hex runs does not false-positive. Mirrors the
// slice-441 qaisuggest pattern.
var uuidPattern = regexp.MustCompile(
	`[0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{12}`,
)

// scfPattern matches an SCF anchor id token (e.g. IAC-06, GOV-01, AST-04) — a
// 2-4 letter family, a dash, then digits. Used to extract an scf_anchor
// citation from a task line. Anchored to a word boundary so it does not match
// inside a longer token.
var scfPattern = regexp.MustCompile(`\b[A-Z]{2,4}-[0-9]{1,3}\b`)

// parseCitedUUIDs extracts the distinct, well-formed UUIDs a task line cites,
// first-seen order. Pure — no IO.
func parseCitedUUIDs(text string) []uuid.UUID {
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

// parseCitedSCF extracts the distinct SCF-anchor id tokens a task line cites,
// first-seen order. Pure — no IO.
func parseCitedSCF(text string) []string {
	matches := scfPattern.FindAllString(text, -1)
	seen := make(map[string]bool, len(matches))
	out := make([]string, 0, len(matches))
	for _, m := range matches {
		if seen[m] {
			continue
		}
		seen[m] = true
		out = append(out, m)
	}
	return out
}

// ControlResolver confirms a cited control/policy id resolves to a tenant-owned
// row under the caller's RLS context. ResolveControl/ResolvePolicy return
// ok=true ONLY when the id names a real row VISIBLE to the requesting tenant; a
// cross-tenant id is RLS-invisible, so they return ok=false for it (the
// load-bearing AC-8 property). The production implementation is *Store; tests
// supply a fake to drive the suppression branches without a live model.
type ControlResolver interface {
	ResolveControl(ctx context.Context, id uuid.UUID) (bool, error)
	ResolvePolicy(ctx context.Context, id uuid.UUID) (bool, error)
}

// allowedRefs is the grounding set the prompt put in front of the model for ONE
// control: the control's own id, its SCF id, and its linked policy ids. The
// model may cite ONLY these for that control's tasks — a cited id outside this
// set is a fabrication even if it happens to name another tenant-owned row.
type allowedRefs struct {
	controlID uuid.UUID
	scfID     string
	policyIDs map[uuid.UUID]bool
}

// buildAllowed assembles the grounding set for one control.
func buildAllowed(c ControlInput) (allowedRefs, error) {
	cid, err := uuid.Parse(c.ID)
	if err != nil {
		return allowedRefs{}, err
	}
	pol := make(map[uuid.UUID]bool, len(c.PolicyIDs))
	for _, p := range c.PolicyIDs {
		if pid, perr := uuid.Parse(p); perr == nil {
			pol[pid] = true
		}
	}
	return allowedRefs{controlID: cid, scfID: c.SCFID, policyIDs: pol}, nil
}

// validateItemCitations is the AC-5 gate — the no-fabricated-coverage
// enforcement for ONE task line of ONE control. It parses the cited ids,
// confirms EVERY one is in the control's grounding set AND (for control/policy
// ids) resolves to a tenant-owned row, and returns the validated citation set.
//
// The contract is STRICT (the JUDGMENT call, decisions log): a SINGLE
// unresolvable or out-of-grounding citation fails the WHOLE item, and the
// caller fails the WHOLE section (suppressing it and persisting nothing). A
// no-fabricated-coverage invariant cannot be "mostly" honored.
//
// Every item MUST cite at least its own control id; a task that cites nothing is
// not a grounded task (ReasonNoCitations). The control citation is always
// present in the grounding set, so a well-formed model output always satisfies
// this.
func validateItemCitations(
	ctx context.Context,
	res ControlResolver,
	taskText string,
	allowed allowedRefs,
) ([]Citation, bool, string, error) {
	out := make([]Citation, 0, 4)
	citedControl := false

	for _, id := range parseCitedUUIDs(taskText) {
		switch {
		case id == allowed.controlID:
			// Grounding: the control id. Confirm it resolves in-tenant.
			ok, err := res.ResolveControl(ctx, id)
			if err != nil {
				return nil, false, ReasonUnresolvedCitation, err
			}
			if !ok {
				return nil, false, ReasonUnresolvedCitation, nil
			}
			out = append(out, Citation{Kind: KindControl, ID: id.String(), Ref: id.String()})
			citedControl = true
		case allowed.policyIDs[id]:
			// Grounding: a linked policy id. Confirm it resolves in-tenant.
			ok, err := res.ResolvePolicy(ctx, id)
			if err != nil {
				return nil, false, ReasonUnresolvedCitation, err
			}
			if !ok {
				return nil, false, ReasonUnresolvedCitation, nil
			}
			out = append(out, Citation{Kind: KindPolicy, ID: id.String(), Ref: allowed.controlID.String()})
		default:
			// A UUID outside this control's grounding set — fabrication or
			// cross-tenant leak. Fail the item (and thus the section).
			return nil, false, ReasonUnresolvedCitation, nil
		}
	}

	// SCF-anchor citation: allowed ONLY if it matches the control's SCF id. The
	// anchor is catalog-global; its tenant grounding is the control that carries
	// it (Ref). A cited SCF id that is not THIS control's is out-of-grounding.
	for _, scf := range parseCitedSCF(taskText) {
		if allowed.scfID == "" || !strings.EqualFold(scf, allowed.scfID) {
			return nil, false, ReasonUnresolvedCitation, nil
		}
		out = append(out, Citation{Kind: KindSCFAnchor, ID: allowed.scfID, Ref: allowed.controlID.String()})
	}

	if !citedControl {
		// Every item must cite its control id (the minimum grounding).
		return nil, false, ReasonNoCitations, nil
	}
	return out, true, "", nil
}
