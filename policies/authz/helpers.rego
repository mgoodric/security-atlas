# security-atlas — authz helpers + input schema documentation.
#
# Source attribution: community_draft (slice 035).
#
# Input schema (built by internal/authz.BuildInput from request context):
#
#   {
#     "user": {
#       "id":    "key_..." | "<uuid>",      # credential id OR user id
#       "roles": ["admin" | "grc_engineer" | "control_owner" |
#                  "auditor" | "viewer"]
#     },
#     "tenant_id": "<uuid>",
#     "action":    "read" | "write" | "submit" | "approve" |
#                  "activate" | "publish" | "rotate" | "revoke" |
#                  "deny" | "upload-bundle" | "aggregate",
#     "resource":  {
#       "type": "controls" | "evidence" | "policies" | "risks" |
#               "framework-scopes" | "exceptions" | "samples" |
#               "populations" | "vendors" | "scopes" | "org_units" |
#               "anchors" | ...,
#       "id":   "<uuid>" | "",              # path id, empty for collections
#       "attrs": { "audit_period_id": "...", ... }   # ABAC attributes
#     },
#     "request": {
#       "method": "GET" | "POST" | ...,
#       "path":   "/v1/..."
#     }
#   }

package authz

# has_role(role) reports whether the calling user holds role.
has_role(role) if {
    some r in input.user.roles
    r == role
}

# is_read is true on read-only actions. Used by viewer / auditor rules
# that allow read-anywhere within their tenant but block writes.
is_read if input.action == "read"

# is_state_transition collects the audit-relevant non-read actions that
# represent governance steps (submit / approve / publish / etc.). Some
# roles allow read+write but NOT state transitions; this helper lets
# those rules be precise.
is_state_transition if {
    transition_actions[input.action]
}

transition_actions := {
    "submit",
    "approve",
    "activate",
    "publish",
    "deny",
}
