-- Slice 144 — tenant identity queries.
--
-- The `tenants` table was added in slice 144 (migration
-- 20260521010000_tenants_rename.sql); see that header for the
-- design rationale. These queries are the read + rename surface.
--
-- All queries run under `tenancy.ApplyTenant` as `atlas_app`.
-- Postgres RLS on the table enforces "caller can only touch their
-- own row" — the slice-002 four-policy pattern. The handler's OPA
-- gate (admin or super_admin) is the second leg.

-- name: GetTenantByID :one
-- Read a single tenant row by id under the caller's tenant context.
-- Returns ErrNoRows when the caller's tenant_id GUC does not match
-- the requested id (RLS filters the row out).
SELECT
    id,
    name,
    is_bootstrap_tenant,
    bundle_gate_mode,
    created_at,
    updated_at
FROM tenants
WHERE id = $1;

-- name: GetTenantBundleGateMode :one
-- Slice 608: read the caller-tenant's control-bundle upload gate policy.
-- RLS scopes the row to the current tenant; the WHERE clause exists only so
-- the query returns at most one row. Returns ErrNoRows when no tenants row
-- exists for the caller (e.g. a bare-UUID tenant that predates slice 144) —
-- the resolver maps that absence to the 'strict' default.
SELECT bundle_gate_mode
FROM tenants
WHERE id = $1;

-- name: UpdateTenantName :one
-- Update a tenant's name. RLS gates this to the caller's own row.
-- Returns the post-update row so the handler can emit the new value
-- on the wire (and serialize before/after for the audit log).
--
-- The case-insensitive UNIQUE expression index on LOWER(name) raises
-- `unique_violation` on duplicate; the handler maps to 409.
UPDATE tenants
SET name = $2
WHERE id = $1
RETURNING
    id,
    name,
    is_bootstrap_tenant,
    bundle_gate_mode,
    created_at,
    updated_at;

-- name: UpdateTenantBundleGateMode :one
-- Slice 608: set the caller-tenant's control-bundle upload gate policy.
-- RLS gates this to the caller's own row. The CHECK constraint
-- `tenants_bundle_gate_mode_chk` rejects an out-of-enum value at the DB
-- layer (defense in depth atop the handler's allow-list).
UPDATE tenants
SET bundle_gate_mode = $2
WHERE id = $1
RETURNING
    id,
    name,
    is_bootstrap_tenant,
    bundle_gate_mode,
    created_at,
    updated_at;
