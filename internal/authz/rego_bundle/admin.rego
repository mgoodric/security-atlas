# security-atlas — admin role policy.
#
# Source attribution: community_draft (slice 035).
#
# Admin is the most permissive role: it allows every action on every
# resource WITHIN the user's tenant. Cross-tenant access is impossible
# at the database layer (constitutional invariant 6 / RLS) so this Rego
# doesn't need to enforce it.
#
# Admin is NOT an emergency-bypass — anti-criterion P0 forbids that. It
# is a normal role, granted by another admin via POST /v1/admin/users
# (slice 037+), and audited like every other role. The point is to have
# one role that can manage tenants without writing per-action carve-outs.
#
# #2503: the /v1/admin/* section (resource.type="admin") is split. A
# tenant admin by role reaches only the tenant-scoped segments in
# tenant_admin_segments. Every other segment is platform: only an admin
# credential reaches it (cred.IsAdmin, which jwtmw sets from the verified
# atlas:super_admin claim, or an admin-flagged API key), as the handlers'
# cred.IsAdmin gates already required. The route inventory is in
# docs/audit-log/2503-tenant-admin-pages-decisions.md.

package authz

allow if {
    has_role("admin")
    input.resource.type != "admin"
}

allow if {
    has_role("admin")
    input.resource.type == "admin"
    tenant_admin_segments[input.resource.id]
}

allow if {
    has_role("admin")
    is_admin_credential
    input.resource.type == "admin"
    not super_admin_only_resource
}

# is_admin_credential reports whether the caller is an admin credential
# rather than a tenant admin by role. internal/authz/input.go sets it from
# cred.IsAdmin.
is_admin_credential if {
    input.user.attrs.is_admin_credential == true
}

# #2503: the tenant-scoped /v1/admin/<segment>/... surfaces a tenant admin
# by role manages for its own tenant. The handler checks
# authz.IsTenantAdmin (or a narrower gate) as the second leg, and RLS keeps
# every read and write in the caller's tenant.
#
#   users         list, read, set roles, set module access, assign and
#                 revoke within the caller's tenant. Cross-tenant assign
#                 and revoke stay super_admin in the handler.
#   controls      GET /v1/admin/controls/held-back (#2358).
#   llm-routing   the tenant's LLM routing (#2428).
#   evidence, policies, exceptions, samples
#                 the per-entity exports, gated on authz.IsTenantApprover
#                 (#2507). POST /v1/admin/policies/import stays on
#                 cred.IsAdmin in the handler.
tenant_admin_segments := {
    "users",
    "controls",
    "llm-routing",
    "evidence",
    "policies",
    "exceptions",
    "samples",
}

# #2358: tenant-admin-only surfaces under /v1/admin/<resource>/..., which
# BuildInput resolves to resource.type="admin", resource.id=<resource>.
# The rules above admit a tenant admin; grc_engineer.rego's blanket read
# excludes these (`not tenant_admin_only_resource`), so no other role
# reads them. The handler checks authz.IsTenantAdmin as the second leg.
#
#   controls   GET /v1/admin/controls/held-back, the stock controls whose
#              shipped update the boot seed holds back behind a reviewed
#              SCF re-anchor.
tenant_admin_only_resource if {
    input.resource.type == "admin"
    tenant_admin_only_segments[input.resource.id]
}

tenant_admin_only_segments := {
    "controls",
}
