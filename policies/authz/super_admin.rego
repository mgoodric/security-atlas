# security-atlas — super_admin role policy.
#
# Source attribution: community_draft (slice 142).
#
# super_admin is the platform-global escalation role. Its membership is
# storage-backed in the slice-198 `super_admins` table and signalled to
# OPA via `input.user.attrs.is_super_admin` (set by
# internal/authz/input.go from the verified JWT's `atlas:super_admin`
# claim).
#
# This file ships TWO things:
#
#   1. `is_super_admin` — a single-line predicate handlers + sibling
#      rego files can reference to inspect the bit without re-reading
#      the input shape.
#
#   2. An `allow` rule that grants super_admin authority for the
#      slice-142 super-admin management surface: POST + DELETE on the
#      `super-admins` resource. The rule is NARROW by resource type so
#      this file does NOT silently elevate a super_admin to do
#      arbitrary writes across every other resource (that would muddle
#      the canvas §9.5 RBAC story — super_admin is a PLATFORM role for
#      identity management, not a tenant-write override). Tenant-rename
#      (slice 144) and create-tenant (slice 143) keep their own
#      authority gates inside their handler code; OPA's role here is
#      narrow.
#
# What this policy does NOT do:
#
#   - It does NOT grant super_admin write access to controls, evidence,
#     risks, policies, vendors, etc. Those need per-tenant role
#     grants. The super_admin claim only opens identity-management
#     surfaces (super-admins, future create-tenant via slice 143).
#
#   - It does NOT short-circuit ABAC predicates. A super_admin who
#     also holds 'auditor' for tenant X is still bound by the auditor
#     read-only ABAC predicates for tenant X's resources.
#
# Composes with: admin.rego (a super_admin who ALSO holds 'admin' on
# the current tenant gets admin authority via admin.rego's role check
# in the standard way).

package authz

# is_super_admin reports whether the calling identity carries the
# platform-global super_admin bit. Set by internal/authz/input.go from
# the verified JWT's `atlas:super_admin` claim (slice 187).
is_super_admin if {
    input.user.attrs.is_super_admin == true
}

# Super_admin authority on the slice-142 management surface.
#
# Action set: read (list) + write (POST grant) + revoke (DELETE demote).
# DELETE on /v1/admin/super-admins/{user_id} maps to action='revoke'
# via internal/authz/input.go's transitionActions promotion when the
# terminal path segment matches a known transition verb. We list the
# verbs explicitly here so the rule is grep-discoverable from the
# handler code.
allow if {
    is_super_admin
    input.resource.type == "admin"
    super_admin_resource_segments[input.resource.id]
    super_admin_actions[input.action]
}

# The slice-142 management surface lives under /v1/admin/super-admins/...
# which BuildInput resolves to resource.type="admin", resource.id="super-admins"
# (or "super-admins" + a path segment downstream). We allow both the
# collection ("super-admins") and the targeted form (user-id path param)
# by matching only on the constant first downstream segment.
super_admin_resource_segments := {
    "super-admins",
}

super_admin_actions := {
    "read",
    "write",
    "revoke",
}
