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
}
