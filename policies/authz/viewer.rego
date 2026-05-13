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
}
