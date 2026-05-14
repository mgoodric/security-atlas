-- security-atlas — evidence freshness read-model + control drift snapshots
-- (slice 016).
--
-- Implements docs/issues/016-evidence-freshness-drift.md AC-1..AC-6.
--
-- ----------------------------------------------------------------------------
-- This migration creates the two derived read-model surfaces slice 016 needs:
--
--   `evidence_freshness`        — a materialized, current-state read model.
--                                 One row per (tenant_id, control_id),
--                                 UPSERTed on every refresh. Records the
--                                 freshest evidence observed_at, the derived
--                                 valid_until, and the is_stale flag.
--   `control_drift_snapshots`   — an append-only daily snapshot ledger. One
--                                 row per refresh; the LATEST row per
--                                 (tenant_id, snapshot_date) is the
--                                 authoritative snapshot for that day. Stores
--                                 the passing-control set so a flipped-to-fail
--                                 control is recoverable by set difference
--                                 (yesterday's passing set MINUS today's).
--
-- Both surfaces are DERIVED from the immutable ledgers:
--   - evidence_freshness is computed from `evidence_records` (slice 013) joined
--     to `controls.freshness_class` (slice 009).
--   - control_drift_snapshots is computed from `control_evaluations` (slice
--     012) — the (result, freshness_status) per (control, scope_cell).
--
-- Constitutional invariants honored:
--
--   #2  Ingestion and evaluation are SEPARATED stages. These two tables are
--       READ-MODEL OUTPUT — slice 016's refresh code writes here and ONLY
--       here. It never writes `evidence_records` or `control_evaluations`.
--       The read model is purely derived: drop every row, re-run the refresh,
--       and identical state is reproduced from the immutable ledgers. A bug
--       in the freshness/drift refresh can never corrupt the source of truth.
--   #6  Tenant isolation at the database layer. Both tables ENABLE + FORCE
--       ROW LEVEL SECURITY.
--         - `evidence_freshness` is an UPSERTed current-state table, so it
--           carries the full FOUR-policy split (tenant_read SELECT,
--           tenant_write INSERT, tenant_update UPDATE, tenant_delete DELETE) —
--           identical to slices 011/014/017/018/020/026's `aggregation_rules`.
--         - `control_drift_snapshots` is APPEND-ONLY by construction:
--           tenant_read (SELECT) + tenant_write (INSERT) policies ONLY. Under
--           FORCE ROW LEVEL SECURITY the absence of UPDATE/DELETE policies
--           means atlas_app cannot mutate snapshot rows — identical to slice
--           013's `evidence_audit_log`, slice 026's
--           `aggregation_rule_evaluations`, and slice 012's
--           `control_evaluations`.
--   D3  Cross-tenant FK leakage blocked. `evidence_freshness` carries the
--       composite FK `(tenant_id, control_id) -> controls(tenant_id, id)` so a
--       freshness row whose tenant_id does not align with the control's owning
--       tenant is refused. `control_drift_snapshots` stores a `passing_control_ids
--       UUID[]` rather than per-row FKs (a snapshot is a set, not a relation)
--       — the array is populated only from controls the refresh already
--       resolved inside the tenant's RLS context, so cross-tenant leakage is
--       blocked at the write path.
--
-- Why `control_drift_snapshots` is append-only, not UPSERTed:
--
--   Drift = `controls_passing_today - controls_passing_yesterday` (canvas
--   §7.1). Refresh happens BOTH on a daily 00:00 UTC tick AND on every
--   evidence ingest — so "today's snapshot" is refreshed many times per day.
--   An UPSERT-in-place table would need a `tenant_update` policy and would
--   destroy the intra-day history. Instead, every refresh APPENDS a new row
--   and the read path takes the LATEST row per (tenant_id, snapshot_date) via
--   `DISTINCT ON (snapshot_date) ... ORDER BY snapshot_date DESC, captured_at
--   DESC`. This is the SAME latest-row-wins pattern `control_evaluations`
--   uses, keeps the table genuinely append-only (two-policy RLS), and
--   preserves the intra-day refresh trail for debugging.
--
-- `is_stale` is materialized, not derived at read time:
--
--   AC-2 requires "records past valid_until are flagged stale=true in the read
--   API". The flag is computed at REFRESH time (is_stale = valid_until <
--   refreshed_at) and stored. The freshness read endpoint reads the stored
--   flag — it does NOT re-derive staleness against now() at query time,
--   because the read model's contract is "freshness as of the last refresh".
--   The daily scheduler tick guarantees the flag is never more than 24h
--   stale-about-staleness; the on-ingest refresh keeps the touched control
--   current. AC-6 is honored independently: NOTHING in this migration deletes
--   from `evidence_records` — stale rows stay queryable for audit replay.
--
-- No new enum types. `freshness_class` is copied as TEXT (it mirrors the
-- nullable `controls.freshness_class TEXT` from slice 009). `trigger` is
-- TEXT + CHECK — the same choice slices 011/012/026/052/054 made: the
-- vocabulary may grow and TEXT + CHECK evolves with a one-line ALTER instead
-- of ALTER TYPE ... ADD VALUE ceremony.
--
-- Migration is reversible via 20260511000028_evidence_freshness_drift.down.sql
-- which drops both tables for a byte-clean up -> down -> up round-trip. No
-- types or constraints on pre-existing tables are touched, so the down
-- migration is a simple two-statement DROP TABLE.
-- ----------------------------------------------------------------------------

-- ===== 1. evidence_freshness =====
--
-- A materialized current-state read model. One row per (tenant_id,
-- control_id); the refresh UPSERTs onto the (tenant_id, control_id) unique
-- key.

CREATE TABLE evidence_freshness (
    id                  UUID PRIMARY KEY,
    tenant_id           UUID NOT NULL,
    -- The control whose evidence freshness this row records. Composite FK
    -- below blocks cross-tenant references (D3).
    control_id          UUID NOT NULL,
    -- The control's freshness_class at refresh time, copied onto the row so
    -- the read model is self-describing even if the control bundle is later
    -- superseded with a different class. NULL when the control declares no
    -- class (the refresh then applies the slice-012 `monthly` default when
    -- computing valid_until, but stores NULL here to record the absence).
    freshness_class     TEXT NULL,
    -- observed_at of the freshest evidence record for this control across the
    -- whole ledger (in-window or not). NULL when the control has no evidence.
    latest_observed_at  TIMESTAMPTZ NULL,
    -- The derived currency horizon: latest_observed_at + freshness_class
    -- max-age (canvas §2.3, the mapping owned by internal/eval). NULL when
    -- the control has no evidence (no observed_at to add to).
    valid_until         TIMESTAMPTZ NULL,
    -- TRUE when valid_until < refreshed_at — the freshest evidence has aged
    -- out of the control's freshness window. A control with NO evidence is
    -- is_stale = TRUE (it is, definitionally, not currently fresh).
    is_stale            BOOLEAN NOT NULL,
    -- How many evidence records exist for this control (whole ledger, not
    -- just in-window) — drives the "N records" count in the freshness panel.
    evidence_count      INT NOT NULL DEFAULT 0,
    -- Wall-clock stamp of the refresh that produced this row.
    refreshed_at        TIMESTAMPTZ NOT NULL DEFAULT now(),
    created_at          TIMESTAMPTZ NOT NULL DEFAULT now(),

    CONSTRAINT evidence_freshness_evidence_count_nonneg
        CHECK (evidence_count >= 0),
    -- A row with no evidence has no observed_at and no valid_until, is stale,
    -- and has a zero count. Keeps the read model internally consistent.
    CONSTRAINT evidence_freshness_no_evidence_coherent
        CHECK (
            latest_observed_at IS NOT NULL
            OR (valid_until IS NULL AND is_stale = TRUE AND evidence_count = 0)
        ),
    -- The UPSERT target: one freshness row per control per tenant.
    CONSTRAINT evidence_freshness_tenant_control_uniq
        UNIQUE (tenant_id, control_id),
    -- D3: cross-tenant FK leakage blocked. Deleting a control cascades its
    -- freshness row (the derived state is meaningless without the control).
    FOREIGN KEY (tenant_id, control_id)
        REFERENCES controls (tenant_id, id) ON DELETE CASCADE
);

-- The freshness-panel read: "freshness distribution by class for this
-- tenant" (AC-1) and "all stale controls for this tenant" (AC-2). A
-- tenant-prefixed B-tree over (freshness_class, is_stale) lets the
-- group-by-class + filter-by-stale aggregate land contiguously.
CREATE INDEX idx_evidence_freshness_class
    ON evidence_freshness (tenant_id, freshness_class, is_stale);

-- ===== 2. control_drift_snapshots =====
--
-- An append-only daily snapshot ledger. Every refresh APPENDS a row; the
-- latest row per (tenant_id, snapshot_date) is the authoritative snapshot
-- for that calendar day.

CREATE TABLE control_drift_snapshots (
    id                   UUID PRIMARY KEY,
    tenant_id            UUID NOT NULL,
    -- The UTC calendar day this snapshot describes. Drift diffs snapshot_date
    -- D against snapshot_date D-1.
    snapshot_date        DATE NOT NULL,
    -- Count of controls "passing" on snapshot_date under the worst-cell
    -- rollup: a control passes iff EVERY applicable (control, scope_cell)
    -- tuple's latest evaluation that day has result='pass' AND
    -- freshness_status='fresh'. Stale evidence does NOT count as passing —
    -- canvas §2.3: stale evidence drives the drift signal.
    controls_passing     INT NOT NULL,
    -- The actual passing-control set. Stored so a flipped-to-fail control is
    -- recoverable: yesterday's passing_control_ids MINUS today's =
    -- the controls that drifted out of passing. Empty array = nothing passing.
    passing_control_ids  UUID[] NOT NULL DEFAULT '{}',
    -- Wall-clock stamp of the refresh that captured this snapshot. The read
    -- path takes the row with the MAX captured_at per (tenant_id,
    -- snapshot_date) — latest-row-wins, same as control_evaluations.
    captured_at          TIMESTAMPTZ NOT NULL DEFAULT now(),
    -- What caused this snapshot. `scheduled` = the daily 00:00 UTC tick;
    -- `ingest` = an evidence-ingest refresh touched the snapshot;
    -- `manual` = an explicit API/CLI trigger.
    trigger              TEXT NOT NULL,
    created_at           TIMESTAMPTZ NOT NULL DEFAULT now(),

    CONSTRAINT control_drift_snapshots_passing_nonneg
        CHECK (controls_passing >= 0),
    CONSTRAINT control_drift_snapshots_trigger_chk
        CHECK (trigger IN ('scheduled', 'ingest', 'manual')),
    -- The count and the set must agree — defense against a refresh bug that
    -- writes one without the other.
    CONSTRAINT control_drift_snapshots_count_matches_set
        CHECK (controls_passing = cardinality(passing_control_ids))
);

-- The drift read: "latest snapshot per day for this tenant over the last N
-- days" (AC-3). A tenant-prefixed B-tree over (snapshot_date DESC,
-- captured_at DESC) lets the DISTINCT ON (snapshot_date) latest-row-wins
-- window query land contiguously.
CREATE INDEX idx_control_drift_snapshots_latest
    ON control_drift_snapshots (tenant_id, snapshot_date DESC, captured_at DESC);

-- ===== 3. Row-Level Security =====

-- evidence_freshness: four-policy split — it is an UPSERTed current-state
-- table. tenant_read SELECT, tenant_write INSERT WITH CHECK, tenant_update
-- UPDATE USING + WITH CHECK, tenant_delete DELETE. Identical shape to slice
-- 026's aggregation_rules.
ALTER TABLE evidence_freshness ENABLE ROW LEVEL SECURITY;
ALTER TABLE evidence_freshness FORCE ROW LEVEL SECURITY;
CREATE POLICY tenant_read ON evidence_freshness
    FOR SELECT USING (current_tenant_matches(tenant_id));
CREATE POLICY tenant_write ON evidence_freshness
    FOR INSERT WITH CHECK (current_tenant_matches(tenant_id));
CREATE POLICY tenant_update ON evidence_freshness
    FOR UPDATE USING (current_tenant_matches(tenant_id))
    WITH CHECK (current_tenant_matches(tenant_id));
CREATE POLICY tenant_delete ON evidence_freshness
    FOR DELETE USING (current_tenant_matches(tenant_id));

-- control_drift_snapshots: append-only by construction — tenant_read
-- (SELECT) + tenant_write (INSERT) policies ONLY. Under FORCE ROW LEVEL
-- SECURITY the absence of UPDATE/DELETE policies means atlas_app cannot
-- mutate snapshot rows. Mirrors slice 013's evidence_audit_log, slice 026's
-- aggregation_rule_evaluations, slice 012's control_evaluations.
ALTER TABLE control_drift_snapshots ENABLE ROW LEVEL SECURITY;
ALTER TABLE control_drift_snapshots FORCE ROW LEVEL SECURITY;
CREATE POLICY tenant_read ON control_drift_snapshots
    FOR SELECT USING (current_tenant_matches(tenant_id));
CREATE POLICY tenant_write ON control_drift_snapshots
    FOR INSERT WITH CHECK (current_tenant_matches(tenant_id));

-- The GRANTs keep the privileges so a future BYPASSRLS migration can still
-- run DDL; RLS is what blocks the mutation at the application role. The
-- absence of an UPDATE/DELETE POLICY on control_drift_snapshots — not the
-- GRANT — is what makes it append-only for atlas_app.
GRANT SELECT, INSERT, UPDATE, DELETE ON evidence_freshness TO atlas_app;
GRANT SELECT, INSERT, UPDATE, DELETE ON control_drift_snapshots TO atlas_app;
