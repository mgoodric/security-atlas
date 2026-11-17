# security-atlas — NIST CSF 2.0 Tier / Profile assessment (/v1/csf/*).
#
# #2540: the bundle had no rule for the csf resource, so every role but
# admin and grc_engineer's reads fell through to default-deny. The slice
# 515 intent, stated in internal/api/csfassessment/handler.go:
#
#   - reads (GET tier, gap, profiles/{kind}): any member of the tenant
#     with a live role.
#   - writes (PUT tier, PUT profiles/{kind}, PUT and DELETE selections):
#     grc_engineer. admin is admitted by admin.rego. The handler checks
#     authz.HasOwnerRole(cred, "grc_engineer") again (#2530).
#
# Each rule names its exact routes; any other method or sub-path under
# /v1/csf stays denied. Default-deny still applies to a roleless
# credential.

package authz

allow if {
    is_read
    input.resource.type == "csf"
    csf_read_route
    count(input.user.roles) > 0
}

allow if {
    has_role("grc_engineer")
    input.action == "write"
    input.resource.type == "csf"
    csf_write_route
}

csf_read_route if {
    csf_path_parts == ["", "v1", "csf", "tier"]
}

csf_read_route if {
    csf_path_parts == ["", "v1", "csf", "gap"]
}

csf_read_route if {
    parts := csf_path_parts
    count(parts) == 5
    parts[3] == "profiles"
    csf_profile_kinds[parts[4]]
}

# PUT /v1/csf/tier
csf_write_route if {
    input.request.method == "PUT"
    csf_path_parts == ["", "v1", "csf", "tier"]
}

# PUT /v1/csf/profiles/{kind}
csf_write_route if {
    input.request.method == "PUT"
    parts := csf_path_parts
    count(parts) == 5
    parts[3] == "profiles"
    csf_profile_kinds[parts[4]]
}

# PUT /v1/csf/profiles/{kind}/selections
csf_write_route if {
    input.request.method == "PUT"
    parts := csf_path_parts
    count(parts) == 6
    parts[3] == "profiles"
    csf_profile_kinds[parts[4]]
    parts[5] == "selections"
}

# DELETE /v1/csf/profiles/{kind}/selections/{requirement_id}
csf_write_route if {
    input.request.method == "DELETE"
    parts := csf_path_parts
    count(parts) == 7
    parts[3] == "profiles"
    csf_profile_kinds[parts[4]]
    parts[5] == "selections"
    parts[6] != ""
}

csf_path_parts := parts if {
    parts := split(input.request.path, "/")
    parts[0] == ""
    parts[1] == "v1"
    parts[2] == "csf"
}

csf_profile_kinds := {
    "current",
    "target",
}
