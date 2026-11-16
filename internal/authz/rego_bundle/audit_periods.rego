# security-atlas — ABAC: auditor × audit_period attribute predicate.
#
# Source attribution: community_draft (slice 035).
#
# Canvas §9.5 example: "auditor X can only see scope cells within
# audit_period Y for client Z." This file implements the audit_period
# half of that example. The scope_cell half lives in scope_cells.rego.
#
# Predicate: when an auditor reads/annotates a sample or population,
# the resource's audit_period_id MUST match one of the audit_period_ids
# in the auditor's input.user.attrs.audit_period_ids (the assignment
# list set when the auditor was granted the role). The resource side is
# set by the authz middleware's hydrator (internal/api/audit.ResourceAttrs,
# #2157) from the population the route names; absent = deny.
#
# Period-scoped resources (samples, populations) are NOT in
# auditor.rego's auditor_readable_resources set; they only become
# allowed when this file's rule fires. So absence of an attribute
# match = deny by default.

package authz

# Allow auditor read of samples when the period matches.
allow if {
    has_role("auditor")
    is_read
    input.resource.type == "samples"
    period_assignment_matches
}

# Allow auditor read of populations when the period matches.
allow if {
    has_role("auditor")
    is_read
    input.resource.type == "populations"
    period_assignment_matches
}

# #2157: allow auditor read of an evidence record, or a control's evidence
# list, that belongs to an assigned period. The authz middleware's hydrator
# (internal/api/audit.ResourceAttrs) sets input.resource.attrs.
# audit_period_ids: for GET /v1/evidence/{id}, the periods whose attached
# populations contain the record (control, window, frozen horizon, scope
# predicate); for GET /v1/evidence?control_id=, the periods with a
# population covering every cell of the control. A list without
# control_id gets no periods, so the auditor has no tenant-wide evidence
# read. /v1/evidence/freshness is the separate exact-path admit in
# auditor.rego.
allow if {
    has_role("auditor")
    is_read
    input.resource.type == "evidence"
    evidence_period_matches
}

evidence_period_matches if {
    some assigned in input.user.attrs.audit_period_ids
    some period in input.resource.attrs.audit_period_ids
    assigned == period
}

period_assignment_matches if {
    some assigned in input.user.attrs.audit_period_ids
    assigned == input.resource.attrs.audit_period_id
}
