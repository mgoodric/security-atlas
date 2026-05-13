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
# list set when the auditor was granted the role).
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

period_assignment_matches if {
    some assigned in input.user.attrs.audit_period_ids
    assigned == input.resource.attrs.audit_period_id
}
