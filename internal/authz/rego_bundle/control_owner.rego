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
    # Slice 027: control owner can read walkthroughs for their controls
    # (AC-4 "the control's owner can read"). The application layer
    # enforces ownership by matching control.owner_role to the
    # credential's OwnerRoles; rego only gates the resource-type touch.
    "walkthroughs",
    # Slice 148: compliance calendar is cross-business by design
    # (slice 094 AC-9: "accessible to all signed-in users, no admin
    # gate"). Control owners specifically need to see when their own
    # periodic-review controls are due — the calendar surfaces that
    # cadence (slice 094 AC-2b "next_due_at = last_evaluated_at +
    # cadence"). RLS keeps the read tenant-scoped. The companion
    # `POST /v1/calendar/subscription` write is admitted by a
    # separate rule further down so a control owner can mint their
    # own ICS URL token.
    "calendar",
}

# Slice 148: control owner can mint their own ICS subscription URL via
# POST /v1/calendar/subscription. See viewer.rego for the design
# rationale; the same narrow path predicate keeps the write surface
# bound to the subscription mint and nothing else.
allow if {
    has_role("control_owner")
    input.action == "write"
    input.resource.type == "calendar"
    input.request.path == "/v1/calendar/subscription"
}

control_owner_writable_resources := {
    "evidence",
    "artifacts",
    # Slice 027: control owner can author walkthroughs for their
    # controls. The walkthrough.created_by stamp + the control's
    # owner_role intersection is the per-row gate; rego allows the
    # action at the role level.
    "walkthroughs",
}
