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
    # Slice 156: slice-066 dashboard read endpoints. The viewer is
    # the most restrictive signed-in role and the program dashboard
    # is the entry surface every signed-in user lands on (slice 040
    # + 066 contract). Without these admits, OPA returns
    # allow=false on GET /v1/activity and GET /v1/upcoming, the
    # React Query enters the isError branch, and the dashboard
    # Activity / Upcoming panels render "Failed to load." RLS keeps
    # the reads tenant-scoped (activity = admin_audit_log_v view,
    # tenant-scoped via slice 062; upcoming = rollup over already
    # tenant-scoped tables). No write surface exists on either
    # endpoint (constitutional invariant #2 — slice 066 P0-A3); the
    # is_read predicate on the rule above keeps these admits
    # read-only.
    #
    # /v1/frameworks/posture is admitted via
    # defaults.rego.catalog_resources["frameworks"] (slice 035);
    # the slice-156 unit test pins that the existing catalog admit
    # continues to cover this path so a future maintainer who
    # narrows catalog_resources is surfaced at the unit-test layer.
    "activity",
    "upcoming",
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
