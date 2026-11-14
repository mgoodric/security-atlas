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
# not admitted here. Default-deny still applies to a roleless credential.
allow if {
    is_read
    input.resource.type == "me"
    me_self_read_paths[input.request.path]
    count(input.user.roles) > 0
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
}
