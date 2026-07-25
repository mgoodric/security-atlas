-- security-atlas — slice 733: SCIM /Groups resource backing store.
--
-- The runtime-integration completion of the IdP-provisioning line:
--   - slice 508 provisions SCIM users (scim_credentials + users.scim_*).
--   - slice 509 maps IdP groups -> atlas roles (oidc_idp_group_mappings) and
--     ships the proven group-to-role resolver (grouprole.Resolver.Derive).
--   - slice 733 (THIS) wires 509's resolver into the live login path AND adds
--     the SCIM /scim/v2/Groups REST resource (RFC 7644) so an IdP can push
--     group membership over the same per-tenant SCIM credential as 508.
--
-- This migration owns TWO new tenant-scoped tables. It introduces NO new
-- mapping/derivation logic — group membership recorded here is fed (by the
-- handler) to the EXISTING slice-509 resolver, which is the ONLY path to a
-- role (P0-733-1 / P0-733-3).
--
--   - scim_groups         : one row per SCIM Group resource. Carries the IdP's
--                           stable externalId + a display name. The SCIM `value`
--                           a member ref points at is THIS row's id (the SCIM
--                           Group resource id). Soft-disable on Delete mirrors
--                           the slice-508 user pattern (a deleted group's
--                           membership is cleared but the row is retained so the
--                           audit chain survives — invariant #2).
--
--   - scim_group_members  : the (group, user) membership edge. A SCIM Group's
--                           `members` array is the set of edges for that group.
--                           user_id is TEXT to match user_roles.user_id (the
--                           value the resolver derives roles for; slice 018
--                           keyed user_roles.user_id as TEXT). group_ref records
--                           the value the resolver matches mappings against —
--                           the SCIM Group's externalId when present, else its
--                           display name — captured at write time so a later
--                           re-derivation reuses the exact group identifier the
--                           mapping table is keyed on.
--
-- Anti-criteria honored at the schema layer (P0-733-*):
--   - P0-733-3 no escalation: NO role column anywhere here. Membership is
--     identity data; the ONLY thing that turns a membership into a role is the
--     slice-509 resolver against oidc_idp_group_mappings (the allow-list). An
--     unmapped group's membership grants nothing (fail-closed, inherited from
--     509 P0-509-1).
--   - P0-733-4 tenant isolation: four-policy FORCE RLS on both tables using the
--     slice-002 current_tenant_matches helper. A SCIM credential for tenant A
--     can never read or mutate tenant B's groups or membership.
--
-- Columns default so existing fixtures stay valid (no backfill needed): both
-- tables are NEW, so there are no pre-existing rows to migrate.
--
-- Migration slot is `20260612040000` (after slice 509's `20260612030000`).
-- Plain SQL (Atlas community caveat — no HCL row_security blocks).
--
-- Issue: docs/issues/733-idp-group-role-live-wiring-scim-groups.md
-- Reversible via 20260612040000_scim_groups.down.sql.

-- ===== scim_groups =====

CREATE TABLE scim_groups (
    id            UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    tenant_id     UUID NOT NULL,
    -- display_name is the SCIM `displayName` (RFC 7643 §4.2). Required by the
    -- spec; non-empty enforced.
    display_name  TEXT NOT NULL,
    -- scim_external_id is the IdP's stable externalId for the group. NULLABLE:
    -- some IdPs omit it. When present it is the value the resolver matches
    -- mappings against (preferred over display_name).
    scim_external_id TEXT NULL,
    -- active soft-disables a group on Delete (invariant #2: retain the row).
    active        BOOLEAN NOT NULL DEFAULT TRUE,
    created_at    TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at    TIMESTAMPTZ NOT NULL DEFAULT now(),

    CONSTRAINT scim_groups_display_name_nonempty
        CHECK (length(display_name) > 0)
);

-- A SCIM Group's externalId is unique within a tenant (an IdP never pushes two
-- groups with the same externalId). NULLs are distinct in a plain UNIQUE index,
-- so two externalId-less groups are allowed (matched by display_name instead).
CREATE UNIQUE INDEX scim_groups_external_id_per_tenant_unique
    ON scim_groups (tenant_id, scim_external_id)
    WHERE scim_external_id IS NOT NULL;

CREATE INDEX scim_groups_tenant_created
    ON scim_groups (tenant_id, created_at DESC);

ALTER TABLE scim_groups ENABLE ROW LEVEL SECURITY;
ALTER TABLE scim_groups FORCE ROW LEVEL SECURITY;
CREATE POLICY tenant_read ON scim_groups
    FOR SELECT USING (current_tenant_matches(tenant_id));
CREATE POLICY tenant_write ON scim_groups
    FOR INSERT WITH CHECK (current_tenant_matches(tenant_id));
CREATE POLICY tenant_update ON scim_groups
    FOR UPDATE USING (current_tenant_matches(tenant_id))
               WITH CHECK (current_tenant_matches(tenant_id));
CREATE POLICY tenant_delete ON scim_groups
    FOR DELETE USING (current_tenant_matches(tenant_id));

-- ===== scim_group_members =====
--
-- The (group, user) membership edge. Keyed by (tenant, group, user) so a user
-- is in a group at most once. user_id is TEXT (matches user_roles.user_id, the
-- slice-018 shape the resolver derives against). group_ref is the resolver-
-- facing group identifier snapshotted at write time (externalId else
-- display_name) so a re-derivation reuses the exact group the mapping table is
-- keyed on.
CREATE TABLE scim_group_members (
    id          UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    tenant_id   UUID NOT NULL,
    group_id    UUID NOT NULL REFERENCES scim_groups (id) ON DELETE CASCADE,
    -- The atlas user the membership references (the SCIM member `value`). TEXT
    -- to match user_roles.user_id (slice 018) — the value the resolver derives
    -- roles for.
    user_id     TEXT NOT NULL,
    -- group_ref is the group identifier the resolver matches mappings against
    -- (externalId when the group has one, else display_name). Snapshotted so a
    -- later re-derivation does not have to re-resolve it.
    group_ref   TEXT NOT NULL,
    created_at  TIMESTAMPTZ NOT NULL DEFAULT now(),

    CONSTRAINT scim_group_members_user_id_nonempty
        CHECK (length(user_id) > 0),
    CONSTRAINT scim_group_members_group_ref_nonempty
        CHECK (length(group_ref) > 0)
);

CREATE UNIQUE INDEX scim_group_members_unique
    ON scim_group_members (tenant_id, group_id, user_id);

-- Hot path: list a user's groups (the resolver's input — every group a user is
-- in, to compute their full validated group set on a membership change).
CREATE INDEX scim_group_members_tenant_user
    ON scim_group_members (tenant_id, user_id);
-- Hot path: list a group's members (the SCIM Group resource's `members` array).
CREATE INDEX scim_group_members_tenant_group
    ON scim_group_members (tenant_id, group_id);

ALTER TABLE scim_group_members ENABLE ROW LEVEL SECURITY;
ALTER TABLE scim_group_members FORCE ROW LEVEL SECURITY;
CREATE POLICY tenant_read ON scim_group_members
    FOR SELECT USING (current_tenant_matches(tenant_id));
CREATE POLICY tenant_write ON scim_group_members
    FOR INSERT WITH CHECK (current_tenant_matches(tenant_id));
CREATE POLICY tenant_update ON scim_group_members
    FOR UPDATE USING (current_tenant_matches(tenant_id))
               WITH CHECK (current_tenant_matches(tenant_id));
CREATE POLICY tenant_delete ON scim_group_members
    FOR DELETE USING (current_tenant_matches(tenant_id));

-- ===== grants =====

GRANT SELECT, INSERT, UPDATE, DELETE ON scim_groups TO atlas_app;
GRANT SELECT, INSERT, UPDATE, DELETE ON scim_groups TO atlas_migrate;
GRANT SELECT, INSERT, UPDATE, DELETE ON scim_group_members TO atlas_app;
GRANT SELECT, INSERT, UPDATE, DELETE ON scim_group_members TO atlas_migrate;
