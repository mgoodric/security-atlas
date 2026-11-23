# security-atlas — grc_engineer role policy.
#
# Source attribution: community_draft (slice 035).
#
# grc_engineer is the operator role for a security-leader-of-one running
# the GRC program. They author controls, policies, risks; configure
# framework scopes; review and approve evidence; export OSCAL. They
# cannot manage user roles (admin-only) or impersonate other tenants.

package authz

# Read access to all tenant-scoped resources, except the tenant-admin-only
# surfaces (#2358, admin.rego).
allow if {
    has_role("grc_engineer")
    input.action == "read"
    not super_admin_only_resource
    not tenant_admin_only_resource
}

# Write + state transitions on the GRC operator surface.
allow if {
    has_role("grc_engineer")
    grc_writable_resources[input.resource.type]
    grc_actions[input.action]
    not risk_hard_delete
}

# #2107: hard-deleting a risk (DELETE /v1/risks/{id}) is admin-only.
# grc_engineer closes a risk instead (PATCH /v1/risks/{id}/close). Deletes
# under a risk (/children/{id}, /themes/{theme}) and of risk categories
# have more path segments and stay grc_engineer's.
risk_hard_delete if {
    input.request.method == "DELETE"
    parts := split(input.request.path, "/")
    count(parts) == 4
    parts[0] == ""
    parts[1] == "v1"
    parts[2] == "risks"
    parts[3] != ""
}

grc_writable_resources := {
    "controls",
    "evidence",
    "policies",
    "risks",
    "framework-scopes",
    "exceptions",
    "incidents",
    "samples",
    "populations",
    "vendors",
    "scopes",
    "org_units",
    "themes",
    "artifacts",
    # Slice 029: the GRC operator (auditee) can reply on shared audit-note
    # threads via POST /v1/audit-notes. Visibility is enforced at the
    # handler + query layer:
    #   - Auditees should only post 'shared' notes (handler-validated).
    #   - Auditees reading 'audit-notes' get only 'shared' rows; the
    #     auditor_only rows are filtered at the query layer
    #     (visibility = 'shared' OR author_user_id = caller).
    "audit-notes",
    # Slice 029: /v1/me/notifications mark-read (PATCH /v1/me/notifications/{id}/read).
    "notifications",
    # Slice 027: GRC engineer authors walkthroughs across the program.
    # Write surface includes create + attach + finalize; read surface
    # is implicit from the action == "read" allow rule above.
    "walkthroughs",
    # OE-663: personnel-security checklists (onboarding/offboarding).
    # The operator creates manual checklists and completes items —
    # completing an item writes the CC1 evidence record. Evidence +
    # task tracking only; no route provisions or deprovisions access
    # (the OE-630 boundary).
    "personnel-security",
    # #1797: tenant overlay edges (requirement -> SCF anchor) under
    # /v1/mapping-overlays. Tenant-owned mappings, RLS-scoped; the catalog
    # edges stay read-only.
    "mapping-overlays",
}

grc_actions := {
    "write",
    "submit",
    "approve",
    "activate",
    "publish",
    "deny",
    "aggregate",
}

# Slice 148: grc_engineer can mint their own ICS subscription URL via
# POST /v1/calendar/subscription. The wildcard-read rule above
# admits GET /v1/calendar but the action="write" path is gated
# separately. Same narrow path predicate the slice-148 admit uses
# on viewer / control_owner / auditor — keeps the write surface
# bound to the subscription mint, not the (non-existent) future
# POST /v1/calendar write path.
allow if {
    has_role("grc_engineer")
    input.action == "write"
    input.resource.type == "calendar"
    input.request.path == "/v1/calendar/subscription"
}

# #2113: grc_engineer revokes an active exception (PATCH
# /v1/exceptions/{id}/revoke). The path's terminal segment promotes the
# action to "revoke", which grc_actions does not hold because revoke on
# other resources (API keys, sessions, grants) stays admin's. This admit
# is bound to exceptions only. The handler gate is IsTenantApprover, which
# admits the live grc_engineer role (#2428), so it adds no refusal for a
# grc_engineer; it refuses roles OPA would not admit here anyway.
allow if {
    has_role("grc_engineer")
    input.action == "revoke"
    input.resource.type == "exceptions"
}

# #2105: grc_engineer revokes an approved risk acceptance (PATCH
# /v1/risks/{id}/acceptance/revoke), as it approves one. Bound to risks
# like the exceptions admit above; the handler gate is the same
# IsTenantApprover.
allow if {
    has_role("grc_engineer")
    input.action == "revoke"
    input.resource.type == "risks"
}

# #2572: grc_engineer sets a metric's target (PUT /v1/metrics/{id}/target),
# as it sets a framework posture target (#2136); the handler gate is the
# same IsTenantApprover. Bound to that one path: "metrics" is not in
# grc_writable_resources, so manual input (POST /v1/metrics/{id}/inputs)
# stays admin's.
allow if {
    has_role("grc_engineer")
    input.action == "write"
    input.resource.type == "metrics"
    input.request.method == "PUT"
    parts := split(input.request.path, "/")
    count(parts) == 5
    parts[0] == ""
    parts[1] == "v1"
    parts[2] == "metrics"
    parts[3] != ""
    parts[4] == "target"
}
