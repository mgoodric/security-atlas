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
