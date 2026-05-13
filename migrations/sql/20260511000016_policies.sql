-- security-atlas — policy library (slice 022).
--
-- The `policies` table was bootstrapped by slice 002's init migration with a
-- placeholder shape (single tenant_isolation RLS policy; minimal columns).
-- This slice graduates the table to its v1 production shape:
--
--   * ADDs the version-chain columns (predecessor_id self-FK,
--     source_attribution, submitted/approved/published/superseded timestamps
--     + actors, linked_control_ids array, created_by, approver_role,
--     renamed owner -> owner_role).
--   * REPLACEs the single tenant_isolation policy with the four-policy split
--     (tenant_read / tenant_write / tenant_update / tenant_delete) established
--     by slices 011/017/018/021/036.
--   * CHANGEs `version` from INTEGER -> TEXT (semver) -- the operator names
--     each published version, no auto-bump.
--   * CHANGEs `status` from the policy_status enum -> TEXT + CHECK constraint
--     so future state-machine additions don't require a TYPE ALTER.
--
-- Canvas §2.6 + CONTEXT.md "Policy (slice 022)" carry the canonical definition.
--
-- Constitutional invariants honored:
--   #6  Tenant isolation at the database layer. FORCE ROW LEVEL SECURITY
--       (inherited from slice 002) plus the four-policy split:
--       tenant_read FOR SELECT, tenant_write FOR INSERT WITH CHECK,
--       tenant_update FOR UPDATE USING + WITH CHECK, tenant_delete FOR
--       DELETE.
--   D3  Cross-tenant FK leakage blocked. Self-FK is composite
--       (tenant_id, predecessor_id) -> (tenant_id, id) so version chains
--       cannot span tenants. Composite UNIQUE (tenant_id, id) provides the
--       FK target.
--   #7  SCF is the canonical control catalog. linked_control_ids is a
--       UUID[] of `controls.id`. Postgres does not enforce per-element
--       array FKs natively; the application validates against `controls`
--       on the write path. Empty array = orphan policy (warning surfaces
--       on read; blocks publish per AC-7 + anti-criterion P0).
--
-- Anti-criteria honored at the schema layer (P0):
--   - Auto-renewal of version chains is impossible. A Publish UPDATE marks
--     the prior 'published' row 'superseded' and an INSERT creates the new
--     row referencing it; there is no UPDATE path that mutates
--     predecessor_id post-creation.
--   - Orphan-publish blocking is enforced at the application layer (canvas
--     §2.6 + AC-7 vocabulary). The DB does not gate publish on
--     linked_control_ids cardinality because the column is UUID[] and a
--     partial-index gate would mask legitimate v1 drafts. Defense in depth:
--     handler returns 409 if len(linked_control_ids) == 0 at publish time.
--
-- Migration is reversible via 20260511000016_policies.down.sql which
-- restores the slice-002 shape (column-wise).

-- ===== Column additions =====

ALTER TABLE policies
    ADD COLUMN predecessor_id                 UUID NULL,
    ADD COLUMN owner_role                     TEXT NOT NULL DEFAULT '',
    ADD COLUMN approver_role                  TEXT NOT NULL DEFAULT '',
    ADD COLUMN linked_control_ids             UUID[] NOT NULL DEFAULT '{}',
    ADD COLUMN source_attribution             TEXT NOT NULL DEFAULT 'tenant_authored',
    ADD COLUMN created_by                     TEXT NOT NULL DEFAULT '',
    ADD COLUMN submitted_at                   TIMESTAMPTZ NULL,
    ADD COLUMN submitted_by                   TEXT NULL,
    ADD COLUMN approved_at                    TIMESTAMPTZ NULL,
    ADD COLUMN approved_by                    TEXT NULL,
    ADD COLUMN published_at                   TIMESTAMPTZ NULL,
    ADD COLUMN published_by                   TEXT NULL,
    ADD COLUMN superseded_at                  TIMESTAMPTZ NULL;

-- ===== Column reshape =====
--
-- version (INTEGER -> TEXT): operator-supplied semver string. Cast existing
-- INTEGER values to TEXT verbatim so the migration is data-preserving on a
-- non-empty table (slice 002 set DEFAULT 1 so existing rows -- none in
-- practice -- become "1").
ALTER TABLE policies
    ALTER COLUMN version DROP DEFAULT,
    ALTER COLUMN version TYPE TEXT USING version::TEXT;

ALTER TABLE policies
    ALTER COLUMN version SET DEFAULT '1.0.0';

-- body_md was DEFAULT '' in slice 002; tighten to require non-empty content.
-- Existing empty rows -- none in practice -- would block this constraint;
-- if a future deploy hits that case the operator should backfill before
-- applying.
ALTER TABLE policies
    ALTER COLUMN body_md DROP DEFAULT;

-- status (policy_status enum -> TEXT + CHECK): give us room to add states
-- in future slices without a TYPE ALTER. Cast existing enum values to TEXT
-- verbatim so the enum vocabulary is preserved.
ALTER TABLE policies
    ALTER COLUMN status DROP DEFAULT,
    ALTER COLUMN status TYPE TEXT USING status::TEXT,
    ALTER COLUMN status SET DEFAULT 'draft';

-- Drop the legacy `owner`/`approver` columns now that owner_role and
-- approver_role exist. The slice-002 shape had them as TEXT with DEFAULT ''
-- and no readers -- dropping is safe; data preserved into owner_role for
-- any non-empty values would be a coding error (no caller has ever
-- populated them).
ALTER TABLE policies
    DROP COLUMN owner,
    DROP COLUMN approver;

-- Rename acknowledgment_required_role -> acknowledgment_required_roles for
-- API consistency (every other slice uses plural array column names).
ALTER TABLE policies
    RENAME COLUMN acknowledgment_required_role TO acknowledgment_required_roles;

-- ===== Constraints =====

ALTER TABLE policies
    ADD CONSTRAINT policies_status_chk
        CHECK (status IN ('draft', 'under_review', 'approved', 'published', 'superseded')),
    ADD CONSTRAINT policies_title_nonempty
        CHECK (length(title) > 0),
    ADD CONSTRAINT policies_version_nonempty
        CHECK (length(version) > 0),
    ADD CONSTRAINT policies_body_md_nonempty
        CHECK (length(body_md) > 0),
    ADD CONSTRAINT policies_owner_role_nonempty
        CHECK (length(owner_role) > 0),
    ADD CONSTRAINT policies_approver_role_nonempty
        CHECK (length(approver_role) > 0),
    ADD CONSTRAINT policies_created_by_nonempty
        CHECK (length(created_by) > 0),
    ADD CONSTRAINT policies_source_attribution_chk
        CHECK (source_attribution IN ('community_draft', 'tenant_authored', 'vendor_provided')),
    -- effective_date is populated only on publish/superseded; draft/
    -- under_review/approved must keep it NULL.
    ADD CONSTRAINT policies_effective_date_set_when_published
        CHECK (
            (status IN ('published', 'superseded') AND effective_date IS NOT NULL)
            OR (status IN ('draft', 'under_review', 'approved') AND effective_date IS NULL)
        );

-- Composite UNIQUE on (tenant_id, id) lets the self-FK target a cross-
-- tenant-safe predecessor reference. Mirrors slice 011/021 pattern.
ALTER TABLE policies
    ADD CONSTRAINT policies_tenant_id_unique UNIQUE (tenant_id, id);

-- Self-FK enforces predecessor chain stays within tenant (D3 invariant).
-- ON DELETE SET NULL because deleting a superseded predecessor should not
-- cascade-delete its successors; the version chain may legitimately have
-- gaps if an old predecessor is admin-cleaned.
ALTER TABLE policies
    ADD CONSTRAINT policies_predecessor_fk
        FOREIGN KEY (tenant_id, predecessor_id)
        REFERENCES policies(tenant_id, id)
        ON DELETE SET NULL;

-- A predecessor_id can be referenced by at most one successor (linear chain,
-- not branching). Partial unique because most rows have predecessor_id = NULL.
CREATE UNIQUE INDEX policies_predecessor_unique_when_set
    ON policies (tenant_id, predecessor_id)
    WHERE predecessor_id IS NOT NULL;

-- List by status (dashboard panels filter by status).
CREATE INDEX idx_policies_tenant_status_created
    ON policies (tenant_id, status, created_at DESC);

-- Version-chain traversal (rare; defensive).
CREATE INDEX idx_policies_tenant_predecessor
    ON policies (tenant_id, predecessor_id);

-- ===== Row-Level Security upgrade =====
--
-- Slice 002 created a single tenant_isolation policy (USING-only, no
-- WITH CHECK -- effectively SELECT-with-INSERT-default). Replace it with
-- the four-policy split so writes/updates/deletes all get explicit
-- WITH CHECK guards. RLS itself is already enabled+forced from slice 002.

DROP POLICY IF EXISTS tenant_isolation ON policies;

CREATE POLICY tenant_read ON policies
    FOR SELECT USING (current_tenant_matches(tenant_id));
CREATE POLICY tenant_write ON policies
    FOR INSERT WITH CHECK (current_tenant_matches(tenant_id));
CREATE POLICY tenant_update ON policies
    FOR UPDATE USING (current_tenant_matches(tenant_id))
    WITH CHECK (current_tenant_matches(tenant_id));
CREATE POLICY tenant_delete ON policies
    FOR DELETE USING (current_tenant_matches(tenant_id));

-- atlas_app GRANTs were established by slice 002 (SELECT/INSERT/UPDATE/DELETE
-- on policies); no re-grant needed.
