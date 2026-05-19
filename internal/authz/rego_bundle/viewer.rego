# security-atlas — viewer role policy.
#
# Source attribution: community_draft (slice 035).
#
# viewer is the most restrictive role: read-only access to non-sensitive
# tenant resources. They see the GRC posture without seeing raw evidence
# payloads or making any state change.
#
# Distinction from auditor: viewer cannot annotate samples or read
# samples/populations (which are audit-period-scoped). They can read
# dashboards, control catalog, policy library text.

package authz

allow if {
    has_role("viewer")
    is_read
    viewer_readable_resources[input.resource.type]
}

viewer_readable_resources := {
    "controls",
    "policies",
    "framework-scopes",
    "vendors",
    "risks",
    "themes",
    "org_units",
    "scopes",
    # Slice 148: compliance calendar is cross-business by design
    # (slice 094 AC-9: "accessible to all signed-in users, no admin
    # gate"). The handler is read-only over four existing source
    # tables; RLS keeps the read tenant-scoped. Viewer is admitted
    # so any tenant user sees upcoming audits / exception
    # expirations / policy review cycles / control review
    # cadences. The companion `POST /v1/calendar/subscription`
    # write is admitted by a separate rule further down so a
    # viewer can mint their own ICS URL token.
    "calendar",
}

# Slice 148: viewer can mint their own ICS subscription URL via
# POST /v1/calendar/subscription. Per-user calendar subscriptions
# are part of the cross-business AC-9 admit ("every signed-in user
# can subscribe in their personal calendar"). The mint is tenant-
# scoped via credstore.Issue + the credential's TenantID; the
# minted token is scope-restricted to AllowedKinds=[calendar.read.v1]
# so a leaked URL cannot be used as a general bearer. The narrow
# path predicate (NOT a wildcard write on `calendar`) is the
# guard that keeps this from accidentally widening to a non-
# existent future PUT /v1/calendar write surface.
allow if {
    has_role("viewer")
    input.action == "write"
    input.resource.type == "calendar"
    input.request.path == "/v1/calendar/subscription"
}
