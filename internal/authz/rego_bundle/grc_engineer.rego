# security-atlas — grc_engineer role policy.
#
# Source attribution: community_draft (slice 035).
#
# grc_engineer is the operator role for a security-leader-of-one running
# the GRC program. They author controls, policies, risks; configure
# framework scopes; review and approve evidence; export OSCAL. They
# cannot manage user roles (admin-only) or impersonate other tenants.

package authz

# Read access to all tenant-scoped resources.
allow if {
    has_role("grc_engineer")
    input.action == "read"
}

# Write + state transitions on the GRC operator surface.
allow if {
    has_role("grc_engineer")
    grc_writable_resources[input.resource.type]
    grc_actions[input.action]
}

grc_writable_resources := {
    "controls",
    "evidence",
    "policies",
    "risks",
    "framework-scopes",
    "exceptions",
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
