# security-atlas — auditor role policy.
#
# Source attribution: community_draft (slice 035).
#
# auditor is the external assessor role. Canvas §9.5 example:
# "auditor X can only see scope cells within audit_period Y for
# client Z." Read-only by default, with one write surface: sample
# annotations (slice 026) — the auditor's findings recorded against a
# pulled sample.
#
# ABAC: the audit_period scope check lives in audit_periods.rego so the
# attribute check can be reused for scope_cells. This file establishes
# the role-level allow; audit_periods.rego adds the attribute guard.

package authz

# Read access to audit-relevant non-period-scoped resources. The
# auditor does NOT see /v1/admin/* or /v1/risks (those are
# operator-internal).
#
# Period-scoped resources (samples, populations) are NOT in this set --
# they're gated by audit_periods.rego, which adds the ABAC predicate
# check on input.resource.attrs.audit_period_id.
allow if {
    has_role("auditor")
    is_read
    auditor_readable_resources[input.resource.type]
}

# One write action: annotating a sample with audit findings
# (slice 026: POST /v1/samples/{id}/annotations). Period-scoped --
# the audit_periods.rego ABAC predicate gates it.
allow if {
    has_role("auditor")
    input.action == "write"
    input.resource.type == "samples"
    auditor_period_matches
}

auditor_period_matches if {
    some assigned in input.user.attrs.audit_period_ids
    assigned == input.resource.attrs.audit_period_id
}

auditor_readable_resources := {
    "controls",
    "policies",
    "framework-scopes",
    "exceptions",
    "artifacts",
    "scopes",
}
