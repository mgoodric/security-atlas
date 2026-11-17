# security-atlas — authz default-deny + public-catalog allow.
#
# Source attribution: community_draft (slice 035). HITL gate pre-merge per
# docs/audit-log/authz-review.md.
#
# This file establishes the default-deny baseline that anti-criterion P0
# requires. Every decision starts as `allow := false`. Role-specific
# .rego files OR (logical OR) into `allow` by emitting their own
# `allow := true` when the input matches their predicate.
#
# Catalog reads (scf_anchors, frameworks, schemas list) are explicitly
# allowed here because they're platform-bundled, tenant-agnostic, and
# already RLS-public (no tenant_id column). Slice 008 (UCF graph
# traversal) and slice 006 (SCF importer) both rely on these reads
# working for any authenticated user.
package authz

# Default-deny baseline. Every other rule OR's into this.
default allow := false

# Catalog reads are public to any authenticated user within the tenant.
# These resources are RLS-public (no tenant_id) and have no write surface
# under the catalog read path. POST to schemas / control bundles is
# gated separately (admin / grc_engineer paths in their own .rego files).
allow if {
    input.action == "read"
    catalog_resources[input.resource.type]
}

catalog_resources := {
    "anchors",
    "frameworks",
    "schemas",
    "scf",
    "themes",
    "requirements",
    "ucf",
    "scopes",
}

# Slice 268 — unified cross-domain search (`/v1/search`).
#
# The endpoint-level admit is intentionally broad: any authenticated
# user inside a tenant may HIT `/v1/search`. The per-type narrowing
# happens INSIDE the handler, which re-invokes the OPA engine with
# `resource.type = controls|risks|evidence` for each requested type
# and drops the ones the caller cannot read (surfacing them in the
# response's `partial_types` field). See slice 268 decision D3 +
# `internal/api/search/search.go` for the design rationale.
#
# Default-deny still applies: callers with NO role (e.g. a fresh
# bearer-exempt path) fall through to the default-deny baseline.
allow if {
    input.action == "read"
    input.resource.type == "search"
    count(input.user.roles) > 0
}

# Slice 468 — per-user saved filter-views (`/v1/saved-views`).
#
# A self-service surface: ANY authenticated user with a role may manage
# THEIR OWN saved views (read + write + delete). Like the slice-029
# `/v1/me/notifications` and slice-016 user-preferences surfaces, the
# per-USER isolation is enforced at the query layer (the handler pins
# `user_id = caller` on every query, sourced from the verified
# credential, never the body) — so one user can never read or mutate
# another's view even within the same tenant (threat-model I / P0-448-5).
# RLS enforces the TENANT half. Default-deny still applies: a roleless
# credential falls through to the baseline.
allow if {
    input.resource.type == "saved-views"
    count(input.user.roles) > 0
}

# #1763 / #2015 — the caller's own `/v1/me/*` reads.
#
# Every signed-in role needs these to render the app shell and its own
# settings: the profile (`/v1/me`), tenant chrome and the switcher
# (`tenancy`, `tenants`; ADR 0023 decision 2), preferences, the
# notification bell, pending policy acknowledgments, the sessions list and
# the three channel opt-in toggles. viewer and control_owner have no other
# rule that admits `me` reads. Each handler is scoped to the caller: it
# reads the verified credential or JWT claims and pins the user to it, so
# no one reads another user's row. `tenancy` names no tenant, only a count.
#
# Exact paths, not a `/v1/me/` prefix, so an endpoint added under
# `/v1/me/` stays denied until someone lists it here or in a role file.
# `/v1/me/audit-period(s)` stay auditor-only (auditor.rego). Writes are
# not admitted here (see the #2022 rule below). Default-deny still
# applies to a roleless credential.
allow if {
    is_read
    input.resource.type == "me"
    me_self_read_paths[input.request.path]
    count(input.user.roles) > 0
}

# #2085 — GET /v1/people, the tenant's users and API keys as names.
#
# Every signed-in role reads it so pages show a person in place of an ID.
# The handler returns id, display name, status and key owner only (no
# email, token or last4) and reads under the caller's RLS tenant, so a
# super-admin sees only the tenant it is signed in to. Exact path: any
# sub-path stays denied. Default-deny still applies to a roleless
# credential.
allow if {
    is_read
    input.resource.type == "people"
    input.request.path == "/v1/people"
    count(input.user.roles) > 0
}

# #2119 — GET /v1/approvals, what the caller can approve right now.
#
# Every signed-in role reads it so the sidebar can ask without knowing the
# caller's roles first. The handler answers per caller: it lists only the
# rows each type's approve handler would accept from this credential
# (authz.IsTenantApprover, then that type's own approver check), so a
# viewer, an auditor or a machine credential gets an empty list. Exact
# path: any sub-path stays denied. Default-deny still applies to a
# roleless credential.
allow if {
    is_read
    input.resource.type == "approvals"
    input.request.path == "/v1/approvals"
    count(input.user.roles) > 0
}

# #2494 — a signed-in user with no role in the current tenant reads its
# own profile and its tenant list, so the web shows "You don't have a role
# in <tenant>" with a switcher instead of a dashboard of 403s. GET /v1/me
# carries has_tenant_role; GET /v1/me/tenants lists the tenants in the
# caller's own token. Both answer from the verified credential alone.
# Exact paths, reads only, and only for a credential that names a user in
# a tenant; every other path still needs a role.
allow if {
    is_read
    input.resource.type == "me"
    roleless_self_read_paths[input.request.path]
    input.tenant_id != ""
    input.user.id != ""
}

roleless_self_read_paths := {
    "/v1/me",
    "/v1/me/tenants",
}

me_self_read_paths := {
    "/v1/me",
    "/v1/me/tenancy",
    "/v1/me/tenants",
    "/v1/me/preferences",
    "/v1/me/notifications",
    "/v1/me/acknowledgments",
    "/v1/me/sessions",
    "/v1/me/email-channel",
    "/v1/me/slack-channel",
    "/v1/me/webhook-channel",
    "/v1/me/modules",
}

# #2022 — the caller's own `/v1/me/*` settings writes.
#
# Every signed-in role manages its own account: the profile (display name
# and time zone), notification preferences, the three channel opt-ins,
# revoking its own sessions, and marking its own notifications read.
# Before this, viewer and control_owner could read these settings
# (#2015) but not change them. Each handler takes the user from the
# verified credential only, never from the body or the path: a session or
# notification id is matched against that user's rows, so another user's
# id is a 404, not a write.
#
# Method and path are both pinned. The two id-bearing routes match one
# non-empty path segment, nothing deeper. Default-deny still applies to a
# roleless credential.
allow if {
    input.action == "write"
    input.resource.type == "me"
    me_self_writes[[input.request.method, input.request.path]]
    count(input.user.roles) > 0
}

allow if {
    input.action == "write"
    input.resource.type == "me"
    me_self_id_write
    count(input.user.roles) > 0
}

me_self_writes := {
    ["PATCH", "/v1/me"],
    ["PATCH", "/v1/me/preferences"],
    ["PUT", "/v1/me/email-channel"],
    ["PUT", "/v1/me/slack-channel"],
    ["PUT", "/v1/me/webhook-channel"],
    ["DELETE", "/v1/me/sessions"],
}

# DELETE /v1/me/sessions/{id}
me_self_id_write if {
    input.request.method == "DELETE"
    parts := split(input.request.path, "/")
    count(parts) == 5
    parts[0] == ""
    parts[1] == "v1"
    parts[2] == "me"
    parts[3] == "sessions"
    parts[4] != ""
}

# PATCH /v1/me/notifications/{id}/read
me_self_id_write if {
    input.request.method == "PATCH"
    parts := split(input.request.path, "/")
    count(parts) == 6
    parts[0] == ""
    parts[1] == "v1"
    parts[2] == "me"
    parts[3] == "notifications"
    parts[4] != ""
    parts[5] == "read"
}
