# security-atlas — ABAC: auditor × scope_cell attribute predicate.
#
# Source attribution: community_draft (slice 035).
#
# Canvas §9.5 example: "auditor X can only see scope cells within
# audit_period Y for client Z." This file implements the scope_cell
# half — when an auditor reads a control's evidence or a framework
# scope, the resource's scope_cell_id must intersect the auditor's
# assigned scope_cell_ids attribute (when present).

package authz

allow if {
    has_role("auditor")
    is_read
    input.resource.type == "evidence"
    scope_assignment_matches
}

scope_assignment_matches if {
    some assigned in input.user.attrs.scope_cell_ids
    assigned == input.resource.attrs.scope_cell_id
}

# Nothing sets input.user.attrs.scope_cell_ids yet: an auditor assignment
# is period-only (#2118), so this rule does not fire today and an
# auditor's evidence read goes through audit_periods.rego (#2157). The
# hydrator does set input.resource.attrs.scope_cell_id on an evidence
# record read. is_read (#2157): a scope match never admits a write.
# When the auditor has no scope_cell_ids attribute, fall through to the
# auditor.rego allow rule. Period-scoped resources are gated separately
# in audit_periods.rego.
