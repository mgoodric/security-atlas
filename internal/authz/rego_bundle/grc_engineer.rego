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
