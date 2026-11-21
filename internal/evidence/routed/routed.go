// Package routed names the evidence kinds that are routed to controls by
// evidence type (#2924).
//
// A connector observes a system, not a control: one github.repo_protection.v1
// record answers both cc8_1 bundles, one osquery.host_posture.v1 record
// answers cc6_6 and cc4_1. A record of a routed kind is therefore stored bound
// to no control (control_id NULL); any wire control_id is kept only as
// provenance in control_ref. It is evidence for every control in the tenant
// whose evidence queries declare its kind.
//
// The three categories (ADR 0028):
//
//   - bound: every kind not listed here or in internal/evidence/unbound. A
//     record reaches the control its control_id names, and no other.
//   - routed by type: this list. Pushed by connectors under the ordinary
//     credential and scope checks.
//   - owned: internal/evidence/unbound. Also control-free, but written only by
//     its platform handler; a push of one is refused (ingest.OwnedKind).
//
// Every ledger reader that resolves a control's evidence applies one rule in
// SQL, with ControlFreeKinds() bound as its unbound_kinds parameter:
//
//	control_id IS NULL
//	AND evidence_kind = ANY(unbound_kinds)
//	AND <the control's evidence_queries> @> [{"evidence_kind": evidence_kind}]
//
// The list is closed: TestStockBundleKindsAreCategorized fails when a stock
// bundle reads a kind that is in no category.
package routed

import (
	"slices"

	"github.com/mgoodric/security-atlas/internal/evidence/unbound"
)

// kinds is the closed list of routed connector kinds.
var kinds = []string{
	"1password.org_policy.v1",
	"aws.s3.bucket_encryption_state.v1",
	"github.audit_event.v1",
	"github.repo_protection.v1",
	"github.repo_protection.v2",
	"jira.ticket_evidence.v1",
	"k8s.networkpolicy_coverage.v1",
	"okta.app_assignment.v1",
	"okta.mfa_policy.v2",
	"okta.user_lifecycle.v1",
	"osquery.host_posture.v1",
	"slack.retention_settings.v1",
}

// Kinds returns a copy of the routed kinds, never nil.
func Kinds() []string {
	return append([]string{}, kinds...)
}

// Kind reports whether kind is routed by type.
func Kind(kind string) bool {
	return slices.Contains(kinds, kind)
}

// ControlFreeKinds returns every kind whose control-free records a control
// reads by kind: the owned unbound kinds plus the routed kinds. Ledger
// readers bind it as unbound_kinds.
func ControlFreeKinds() []string {
	return append(unbound.Kinds(), kinds...)
}
