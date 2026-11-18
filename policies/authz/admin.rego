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

package authz

allow if {
    has_role("admin")
    not super_admin_only_resource
}

# #2358: tenant-admin-only surfaces under /v1/admin/<resource>/..., which
# BuildInput resolves to resource.type="admin", resource.id=<resource>.
# The rule above admits a tenant admin; grc_engineer.rego's blanket read
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
