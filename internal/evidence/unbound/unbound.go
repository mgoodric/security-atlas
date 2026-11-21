// Package unbound names the evidence kinds whose records are pushed bound to
// no control and are evidence for every control whose evidence queries
// declare the kind (#2817).
//
// A policy acknowledgement is the one such kind. It is pushed under
// control_id "policy:<id>:v<version>" (internal/api/policyacks), which ingest
// cannot resolve to a control, and the stock controls that read it (cc1_5,
// cc5_3, cc7_4) ask about acknowledgement across the tenant, not of one
// policy. The list is closed on purpose: any other kind's unbound record is a
// free-form control_ref and reaches only the control that names it.
//
// Every ledger reader that resolves a control's evidence applies the same
// rule in SQL, with Kinds() bound as its unbound_kinds parameter:
//
//	control_id IS NULL
//	AND evidence_kind = ANY(unbound_kinds)
//	AND <the control's evidence_queries> @> [{"evidence_kind": evidence_kind}]
//
// A record of such a kind that IS bound to a control reaches only that
// control.
package unbound

// kinds is the closed list. Kinds returns a copy so no caller can widen it.
var kinds = []string{
	"policy.acknowledgment.v1",
}

// Kinds returns the closed list of unbound evidence kinds, never nil, so a
// query always binds a text[] parameter.
func Kinds() []string {
	return append([]string{}, kinds...)
}
