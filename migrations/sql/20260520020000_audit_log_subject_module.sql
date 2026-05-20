-- security-atlas — slice 180: privacy-module foundation.
--
-- Add `subject_module TEXT NOT NULL DEFAULT 'core'` column to each of the
-- nine platform audit-log tables. Pre-commitment for the deferred privacy
-- sibling module (canvas OQ #7, resolved 2026-05-20).
--
-- WHY THIS LANDS NOW (without privacy primitives):
--
-- The privacy module (DataSubject / ProcessingActivity / DPIA / DSR primitives)
-- is v2+ work, gated on a real prospect surfacing demand. But the audit-log
-- column is cheap to add today and expensive to retrofit later — once privacy
-- primitives exist, every privacy-side audit-log write would need to backfill
-- a `subject_module` value and a migration would need to thread NOT NULL onto
-- already-populated rows. Adding the column NOW with `DEFAULT 'core'` means
-- every existing audit-log row carries `'core'` from day one and future
-- writes (whether from `core` or `privacy` or a third future module) tag
-- themselves explicitly at insert time.
--
-- THE NINE TABLES (verbatim from slice 124's UNION ALL):
--
--   1. decision_audit_log         (slice 035)  — authz allow/deny decisions
--   2. evidence_audit_log         (slice 013)  — IngestEvidence accept/reject
--   3. exception_audit_log        (slice 021)  — exception lifecycle
--   4. sample_audit_log           (slice 026)  — audit sample draws
--   5. audit_period_audit_log     (slice 028)  — period freeze / unfreeze
--   6. aggregation_rule_audit_log (slice 053)  — rule create/edit/disable
--   7. feature_flag_audit_log     (slice 059)  — flag toggles
--   8. me_audit_log               (slice 108)  — per-user profile / read events
--   9. walkthrough_audit_log      (slice 027)  — walkthrough lifecycle
--
-- Constitutional invariants honored:
--
--   #6 Tenant isolation via PostgreSQL RLS. The new column is data only;
--      it does not alter any RLS policy. AC-7 (integration test) asserts
--      the visibility-set under tenant context is unchanged.
--
--   #2 / canvas §4.3 Append-only evidence ledger. The new column does NOT
--      change the immutability invariant. Slice 036's four-policy RLS pattern
--      (SELECT-tenant_read + INSERT-tenant_write, no UPDATE policy, no
--      DELETE policy) is preserved unchanged across all nine tables.
--
-- ANTI-CRITERIA HONORED (slice 180):
--
--   P0-180-1: does NOT create a `privacy` Postgres schema namespace. Empty
--             schemas are confusing; the namespace lands with privacy v0.
--   P0-180-2: does NOT add an index on `subject_module`. Current query
--             patterns filter by `tenant_id + occurred_at`; if privacy v0
--             needs a module-filtered query, the index ships in THAT slice
--             with real workload data.
--   P0-180-6: idempotent (`ADD COLUMN IF NOT EXISTS`) and reversible
--             (companion `.down.sql` drops the column from all nine).
--   P0-180-7: touches ONLY the nine audit-log tables; no other table is
--             modified.
--   P0-180-9: does NOT relax slice 036's four-policy RLS pattern.
--
-- DEFAULT 'core' rationale: existing rows backfill via the DEFAULT (no
-- separate UPDATE statement needed). The 'core' value tags every primitive
-- shipping today (Control / Risk / Evidence / Scope / Framework / Policy +
-- their dependents). The eventual privacy v0 module will write 'privacy';
-- a third future module would pick its own short identifier.

ALTER TABLE decision_audit_log
    ADD COLUMN IF NOT EXISTS subject_module TEXT NOT NULL DEFAULT 'core';

ALTER TABLE evidence_audit_log
    ADD COLUMN IF NOT EXISTS subject_module TEXT NOT NULL DEFAULT 'core';

ALTER TABLE exception_audit_log
    ADD COLUMN IF NOT EXISTS subject_module TEXT NOT NULL DEFAULT 'core';

ALTER TABLE sample_audit_log
    ADD COLUMN IF NOT EXISTS subject_module TEXT NOT NULL DEFAULT 'core';

ALTER TABLE audit_period_audit_log
    ADD COLUMN IF NOT EXISTS subject_module TEXT NOT NULL DEFAULT 'core';

ALTER TABLE aggregation_rule_audit_log
    ADD COLUMN IF NOT EXISTS subject_module TEXT NOT NULL DEFAULT 'core';

ALTER TABLE feature_flag_audit_log
    ADD COLUMN IF NOT EXISTS subject_module TEXT NOT NULL DEFAULT 'core';

ALTER TABLE me_audit_log
    ADD COLUMN IF NOT EXISTS subject_module TEXT NOT NULL DEFAULT 'core';

ALTER TABLE walkthrough_audit_log
    ADD COLUMN IF NOT EXISTS subject_module TEXT NOT NULL DEFAULT 'core';
