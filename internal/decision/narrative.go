package decision

import (
	"fmt"
	"sort"
	"strings"

	"github.com/google/uuid"
)

// AuditNarrativeRemark is the structured shape of a single Decision Log
// entry's contribution to an OSCAL SSP narrative. Slice 030's OSCAL export
// turns each remark into an OSCAL `<remarks>` block on the relevant
// control's implemented-requirement. Decisions are audit *context*, not
// compliance artifacts (canvas Â§6.7, constitutional invariant 8) -- the
// remark explains *why* a gap or tradeoff exists, it does not assert
// coverage.
type AuditNarrativeRemark struct {
	// DecisionID is the human-readable "DL-YYYY-MM-DD-NNNN" identifier.
	DecisionID string
	// ControlIDs are the in-scope control UUIDs this decision is linked
	// to -- the controls whose SSP narrative this remark attaches to.
	ControlIDs []uuid.UUID
	// Text is the rendered `<remarks>` body (see EmitRemarkText format).
	Text string
}

// EmitRemarkText renders a single decision into the AC-7 narrative format:
//
//	[DL-id] {title} ({decision_maker}, {decided_at}) — Linked risks: {ids}. Revisit: {revisit_by or "n/a"}.
//
// `decided_at` is rendered as a UTC calendar date (YYYY-MM-DD). `revisit_by`
// renders as its date or the literal "n/a" when unset. linkedRiskIDs are the
// human-or-uuid risk identifiers the caller has resolved; they render
// comma-separated, or "none" when empty. The function is pure and
// deterministic -- the risk-id list is sorted so the output is stable for
// snapshot tests and for byte-identical OSCAL re-exports.
func EmitRemarkText(d Decision, linkedRiskIDs []string) string {
	risks := "none"
	if len(linkedRiskIDs) > 0 {
		sorted := append([]string(nil), linkedRiskIDs...)
		sort.Strings(sorted)
		risks = strings.Join(sorted, ", ")
	}
	revisit := "n/a"
	if d.RevisitBy != nil {
		revisit = d.RevisitBy.UTC().Format("2006-01-02")
	}
	return fmt.Sprintf(
		"[%s] %s (%s, %s) — Linked risks: %s. Revisit: %s.",
		d.DecisionID,
		d.Title,
		d.DecisionMaker,
		d.DecidedAt.UTC().Format("2006-01-02"),
		risks,
		revisit,
	)
}

// EmitRemark builds the full AuditNarrativeRemark for a decision, given its
// linked control UUIDs and the resolved risk identifiers. It returns
// (remark, true) when the decision should appear in the OSCAL narrative,
// and (_, false) when the decision has opted out via
// audit_narrative_opt_out (P0 anti-criterion) or has no linked controls
// (a decision linked to no in-scope control has nowhere to attach).
func EmitRemark(d Decision, linkedControlIDs []uuid.UUID, linkedRiskIDs []string) (AuditNarrativeRemark, bool) {
	if d.AuditNarrativeOptOut {
		return AuditNarrativeRemark{}, false
	}
	if len(linkedControlIDs) == 0 {
		return AuditNarrativeRemark{}, false
	}
	return AuditNarrativeRemark{
		DecisionID: d.DecisionID,
		ControlIDs: append([]uuid.UUID(nil), linkedControlIDs...),
		Text:       EmitRemarkText(d, linkedRiskIDs),
	}, true
}

// EmitRemarks is the slice-030 entry point: given a set of decisions, each
// already paired with its linked control UUIDs and resolved risk
// identifiers, it returns the remarks that should appear in the OSCAL SSP
// narrative. Opted-out decisions (audit_narrative_opt_out=true) and
// decisions with no linked controls are dropped -- the returned slice
// contains only emittable remarks. Order follows the input order.
//
// NarrativeInput is the per-decision bundle the caller assembles by
// joining a decision against its decision_controls / decision_risks link
// rows; slice 055 ships the function and unit-tests it, slice 030 calls it
// from the OSCAL export pipeline.
type NarrativeInput struct {
	Decision         Decision
	LinkedControlIDs []uuid.UUID
	LinkedRiskIDs    []string
}

// EmitRemarks renders every emittable decision in inputs.
func EmitRemarks(inputs []NarrativeInput) []AuditNarrativeRemark {
	out := make([]AuditNarrativeRemark, 0, len(inputs))
	for _, in := range inputs {
		if remark, ok := EmitRemark(in.Decision, in.LinkedControlIDs, in.LinkedRiskIDs); ok {
			out = append(out, remark)
		}
	}
	return out
}
