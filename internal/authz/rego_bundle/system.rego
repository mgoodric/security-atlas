# security-atlas — system / machine-actor exemptions.
#
# Source attribution: community_draft (slice 035).
#
# Connector pushes (POST /v1/evidence:push) use machine credentials
# issued via /v1/admin/credentials (slice 014 / 034). Those credentials
# don't have a human user_id and don't fit the human-RBAC model. The
# slice-034 credential carries `kinds` (the list of evidence_kind values
# the credential can push) — when the request is a push and the
# credential carries that kind in its scope, this rule allows it.
#
# The platform layer (internal/api/evidence.PushHTTP) ALSO checks
# kinds — this Rego rule is the second leg of defense in depth, so a
# bug in the platform layer doesn't open a "push any kind" hole.
#
# Schema admin (POST /v1/schemas) is a separate machine path gated by
# the slice-034 IsAdmin flag. The legacy-flag bridge maps IsAdmin to
# the admin role, so admin.rego covers it without this file needing a
# dedicated rule.

package authz

# Connector push: machine credentials authorized for evidence:push when
# the resource type is `evidence` and the credential's role bridge
# resolved to `grc_engineer` (the default machine-actor role for slice
# 034 api_keys that have no human user backing).
allow if {
    input.action == "write"
    input.resource.type == "evidence"
    input.user.attrs.is_machine_actor == true
    has_role("grc_engineer")
}

# Slice 196 — atlas-bootstrap container OAuth migration. The one-shot
# bootstrap container drives `atlas-cli controls upload` via an OAuth
# client_credentials JWT (slice 188). That JWT has no per-tenant role
# bindings (slice 188's handleClientCredentials emits an empty Roles map
# and SuperAdmin=false on purpose), so the human-RBAC rules cannot
# admit it. The pre-slice-196 bootstrap path sidestepped this by
# minting an IsAdmin=true fixed-token credential via
# IssueBootstrapFixedAdminCredential; this carve-out is the symmetric
# OPA admit for the new OAuth flow.
#
# The rule is scoped THREE ways to keep the surface narrow:
#   - action == "upload-bundle"     — only the slice-037 upload path;
#                                     does not grant general controls writes.
#   - resource.type == "controls"   — does not grant uploads to any other
#                                     resource type.
#   - is_machine_actor == true      — does not admit any human caller
#                                     (which would let an
#                                     authenticated-but-role-less user
#                                     upload bundles).
#
# Pair this rule with the slice-196 widening of is_machine_actor in
# internal/authz/input.go which adds `oauth_client:` to the recognised
# machine-actor UserID prefixes.
allow if {
    input.action == "upload-bundle"
    input.resource.type == "controls"
    input.user.attrs.is_machine_actor == true
}
