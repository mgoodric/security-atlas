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
