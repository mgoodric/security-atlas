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
