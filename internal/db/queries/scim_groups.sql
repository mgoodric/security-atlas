-- Slice 733 — SCIM /Groups resource queries.
--
-- Two clusters:
--   (1) scim_groups CRUD — the SCIM Group resource (Create/Get/List/Patch/
--       Delete per RFC 7644).
--   (2) scim_group_members edge CRUD + the resolver-feeding read
--       (ListGroupRefsForUser): a membership change feeds the user's FULL
--       current validated group set to the slice-509 grouprole.Resolver.Derive
--       (AC-3). This file holds NO mapping/derivation logic — that lives in
--       slice 509 and is reused, not re-authored (P0-733-1).
--
-- Every query is tenant-scoped in WHERE and runs under app.current_tenant RLS
-- (invariant #6 / P0-733-4).

-- ===== scim_groups CRUD =====

-- name: CreateSCIMGroup :one
-- Creates a SCIM Group. scim_external_id is NULLABLE (some IdPs omit it).
INSERT INTO scim_groups (id, tenant_id, display_name, scim_external_id)
VALUES ($1, $2, $3, $4)
RETURNING id, tenant_id, display_name, scim_external_id, active, created_at, updated_at;

-- name: GetSCIMGroupByID :one
-- Single group by id within the tenant. ErrNoRows when absent (a group in
-- another tenant reads identically to "not found" — RLS-confined, no oracle).
SELECT id, tenant_id, display_name, scim_external_id, active, created_at, updated_at
FROM scim_groups
WHERE tenant_id = $1 AND id = $2;

-- name: GetSCIMGroupByExternalID :one
-- Single group by externalId within the tenant (Create reconciliation).
SELECT id, tenant_id, display_name, scim_external_id, active, created_at, updated_at
FROM scim_groups
WHERE tenant_id = $1 AND scim_external_id = $2;

-- name: ListSCIMGroups :many
-- A page of tenant groups (List, no filter). RLS confines to the tenant.
SELECT id, tenant_id, display_name, scim_external_id, active, created_at, updated_at
FROM scim_groups
WHERE tenant_id = $1
ORDER BY created_at DESC
LIMIT $2 OFFSET $3;

-- name: CountSCIMGroups :one
-- Total group count in the tenant (List envelope totalResults).
SELECT COUNT(*) AS total
FROM scim_groups
WHERE tenant_id = $1;

-- name: ListSCIMGroupsByDisplayName :many
-- Groups matching `filter=displayName eq "x"` (the List filter minimum).
SELECT id, tenant_id, display_name, scim_external_id, active, created_at, updated_at
FROM scim_groups
WHERE tenant_id = $1 AND lower(display_name) = lower($2)
ORDER BY created_at DESC;

-- name: ReplaceSCIMGroupDisplayName :one
-- Overwrites the group's display name (Patch/Replace of displayName). Bumps
-- updated_at.
UPDATE scim_groups
SET display_name = $3, updated_at = now()
WHERE tenant_id = $1 AND id = $2
RETURNING id, tenant_id, display_name, scim_external_id, active, created_at, updated_at;

-- name: SetSCIMGroupActive :one
-- Soft-disables / re-enables a group (Delete = active=false). Bumps updated_at.
UPDATE scim_groups
SET active = $3, updated_at = now()
WHERE tenant_id = $1 AND id = $2
RETURNING id, tenant_id, display_name, scim_external_id, active, created_at, updated_at;

-- ===== scim_group_members edge CRUD =====

-- name: AddSCIMGroupMember :exec
-- Adds a (group, user) membership edge. Idempotent on the unique index: a
-- duplicate add is a no-op (the SCIM `add members` op is idempotent).
INSERT INTO scim_group_members (id, tenant_id, group_id, user_id, group_ref)
VALUES ($1, $2, $3, $4, $5)
ON CONFLICT (tenant_id, group_id, user_id) DO NOTHING;

-- name: RemoveSCIMGroupMember :execrows
-- Removes a single (group, user) membership edge. Returns affected-row count.
DELETE FROM scim_group_members
WHERE tenant_id = $1 AND group_id = $2 AND user_id = $3;

-- name: RemoveAllSCIMGroupMembers :execrows
-- Clears every member of a group (Replace `members`, or Delete soft-disable).
-- Returns affected-row count.
DELETE FROM scim_group_members
WHERE tenant_id = $1 AND group_id = $2;

-- name: ListSCIMGroupMembers :many
-- The member user ids of a group (the SCIM Group resource's `members` array).
SELECT user_id
FROM scim_group_members
WHERE tenant_id = $1 AND group_id = $2
ORDER BY user_id ASC;

-- name: ListGroupRefsForUser :many
-- The DISTINCT group_refs the user is currently a member of across ALL active
-- groups in the tenant. This is the resolver input on a membership change: the
-- user's FULL current validated group set (AC-3). Inactive (soft-disabled)
-- groups contribute nothing — a deleted group is no longer a membership source.
SELECT DISTINCT m.group_ref
FROM scim_group_members m
JOIN scim_groups g ON g.tenant_id = m.tenant_id AND g.id = m.group_id
WHERE m.tenant_id = $1 AND m.user_id = $2 AND g.active = TRUE
ORDER BY m.group_ref ASC;
