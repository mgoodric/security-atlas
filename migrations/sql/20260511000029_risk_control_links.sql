-- security-atlas — risk-control linkage effectiveness weighting (slice 020).
--
-- Implements docs/issues/020-risk-control-linkage-residual.md migration `_029`.
--
-- ----------------------------------------------------------------------------
-- The `risk_control_links` table itself was created by slice 019 (migration
-- `_005`): a many-to-many join between `risks` and `controls` with a composite
-- PRIMARY KEY (tenant_id, risk_id, control_id), composite cross-tenant-safe
-- FKs, and the four-policy RLS split (tenant_read/write/update/delete) under
-- FORCE ROW LEVEL SECURITY. Slice 020 does NOT recreate it.
--
-- This migration ALTERs that table to add the per-link control-effectiveness
-- weighting columns the canvas §6.2 residual formula needs:
--
--   control_effectiveness = weight_design     * design_score
--                         + weight_operation  * operational_score
--                         + weight_coverage   * coverage_score
--
--   residual_score = inherent_score * (1 - weighted_control_effectiveness)
--
-- `design_score` is the human-set design-quality factor (0..1) — the only
-- component a person sets directly. `operational_score` is derived from slice
-- 012's rolling-30-day evidence pass rate; `coverage_score` is derived from
-- slice 017's applicability set intersected with the scope cells where the
-- control passes. Operational and coverage are computed at read time from the
-- evaluation ledger — they are NOT stored on the link row, because caching a
-- derived score beyond its staleness threshold is a P0 anti-criterion. Only
-- the human inputs (the design score and the three weights) are persisted.
--
-- Defaults: design_score 0.5 (a neutral "average" design until a human grades
-- it); weights 0.3 / 0.5 / 0.2, which sum to 1.0 — operational pass rate is
-- weighted heaviest because it is the component that "trends with reality"
-- (canvas §6.2). The weights are per-link, not global: a control whose design
-- is the dominant signal for a particular risk can be re-weighted on that link
-- without affecting the same control's contribution to other risks.
--
-- Constitutional invariants honored:
--
--   #2  Ingestion and evaluation are SEPARATED stages. This migration touches
--       only the linkage table; it adds no evidence-write path. The residual
--       derivation reads `control_evaluations` (the evaluation ledger) and
--       these weight columns — it never writes `evidence_records`.
--   #6  Tenant isolation at the database layer. The four-policy RLS split on
--       `risk_control_links` (slice 019, FORCE ROW LEVEL SECURITY) already
--       covers every row including the new columns — RLS is row-scoped, not
--       column-scoped, so no policy change is needed.
--
-- Idempotency / reversibility:
--
--   The four ADD COLUMN statements carry NOT NULL DEFAULT values so the ALTER
--   succeeds against a table that already holds slice-019 link rows (each
--   existing row is back-filled with the defaults). The CHECK constraints are
--   added in the same migration. Fully reversible via
--   20260511000029_risk_control_links.down.sql, which drops the four columns
--   (CASCADE-free — the CHECK constraints drop with their columns) for a
--   byte-clean up -> down -> up round-trip.
-- ----------------------------------------------------------------------------

-- ===== Per-link control-effectiveness weighting columns =====

ALTER TABLE risk_control_links
    ADD COLUMN design_score      NUMERIC(4,3) NOT NULL DEFAULT 0.5,
    ADD COLUMN weight_design     NUMERIC(4,3) NOT NULL DEFAULT 0.3,
    ADD COLUMN weight_operation  NUMERIC(4,3) NOT NULL DEFAULT 0.5,
    ADD COLUMN weight_coverage   NUMERIC(4,3) NOT NULL DEFAULT 0.2;

-- ===== CHECK constraints — every score and weight is a [0,1] factor =====
--
-- The residual math assumes each component score and each weight is in [0,1];
-- a value outside that range would produce a residual outside [0, inherent].
-- The application layer also validates these on write so the API returns a
-- 400 rather than a raw 23514 (check_violation); the DB constraints are the
-- defense-in-depth guard.

ALTER TABLE risk_control_links
    ADD CONSTRAINT risk_control_links_design_score_range
        CHECK (design_score >= 0 AND design_score <= 1),
    ADD CONSTRAINT risk_control_links_weight_design_range
        CHECK (weight_design >= 0 AND weight_design <= 1),
    ADD CONSTRAINT risk_control_links_weight_operation_range
        CHECK (weight_operation >= 0 AND weight_operation <= 1),
    ADD CONSTRAINT risk_control_links_weight_coverage_range
        CHECK (weight_coverage >= 0 AND weight_coverage <= 1);
