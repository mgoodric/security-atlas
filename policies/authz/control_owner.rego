# security-atlas — control_owner role policy.
#
# Source attribution: community_draft (slice 035).
#
# control_owner is the role assigned to a specific control's accountable
# owner. They can read controls + their evidence and submit manual
# attestations (slice 011). They cannot approve policies, manage risks,
# or write to framework scopes.

package authz

# Read access to controls + evidence (their own work artifacts) +
# policies (so they can read the policies they're implementing).
allow if {
    has_role("control_owner")
    is_read
    control_owner_readable_resources[input.resource.type]
}

# Manual attestation submission (slice 011): writing evidence for
# controls they own. The control's owner_role match is enforced by the
# slice-011 handler — here we just check the role grants the action.
allow if {
    has_role("control_owner")
    input.action == "write"
    control_owner_writable_resources[input.resource.type]
}

control_owner_readable_resources := {
    "controls",
    "evidence",
    "policies",
    "artifacts",
    "framework-scopes",
}

control_owner_writable_resources := {
    "evidence",
    "artifacts",
}
