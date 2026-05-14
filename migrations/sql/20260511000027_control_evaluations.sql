-- security-atlas — control state evaluation ledger (slice 012).
--
-- Implements docs/issues/012-control-state-evaluation.md AC-1..AC-7.
--
-- ----------------------------------------------------------------------------
-- This migration creates `control_evaluations`: the output table of the
-- EVALUATION STAGE (canvas §4.3). The evaluation engine (internal/eval) is a
-- read-only consumer of the append-only evidence ledger (`evidence_records`,
-- slice 013). It computes `(control × scope_cell × time) → {pass, fail, na,
-- inconclusive}` plus a freshness status, and appends the derived state here.
--
-- Constitutional invariants honored:
--
--   #2  Ingestion and evaluation are SEPARATED stages with an append-only
--       evidence ledger between them. `control_evaluations` is the evaluation
--       stage's OWN output table — the engine writes here and ONLY here. It
--       never writes `evidence_records` or any ingestion-side table. Two
--       structural guarantees back this:
--         (a) `evidence_records` itself has no UPDATE/DELETE RLS policy under
--             FORCE ROW LEVEL SECURITY (slice 013) — even a buggy evaluation
--             path cannot mutate the ledger.
--         (b) The engine's only writer is a sqlc Queries handle whose sole
--             INSERT target is this table.
--       Because the engine derives state purely from the immutable ledger,
--       point-in-time replay is always possible: delete every
--       `control_evaluations` row, re-run the engine, and identical state is
--       reproduced (AC-7).
--   #6  Tenant isolation at the database layer. ENABLE + FORCE ROW LEVEL
--       SECURITY. Append-only RLS shape: `tenant_read` (SELECT) +
--       `tenant_write` (INSERT) policies ONLY. The explicit ABSENCE of
--       UPDATE/DELETE policies under FORCE ROW LEVEL SECURITY means atlas_app
--       cannot execute those commands on the table — append-only by
--       construction. Identical to slice 013's `evidence_audit_log` and slice
--       054's `aggregation_rule_evaluations`.
--   D3  Cross-tenant FK leakage blocked. The composite FK
--       `(tenant_id, control_id) -> controls(tenant_id, id)` refuses an
--       evaluation row whose tenant_id does not align with the control's
--       owning tenant. `scope_cells` did not previously carry a
--       `(tenant_id, id)` composite key, so this migration adds
--       `scope_cells_tenant_id_unique` and the evaluation table's
--       `(tenant_id, scope_cell_id)` composite FK rides on it.
--
-- Append-only ledger, NOT a mutable current-state table:
--
--   The issue spec's narrative literally names the output `control_state`.
--   We ship `control_evaluations` — an append-only EVALUATION LEDGER, one row
--   per evaluation run per (control_id, scope_cell_id), latest-row-by-
--   `evaluated_at` wins. Rationale: an upsert "current state" table destroys
--   the prior computed state on every run, which makes AC-7's point-in-time
--   replay test meaningless (nothing to compare) and breaks historical
--   `as-of` queries. An append-only ledger is also the established
--   precedent in this codebase (`evidence_audit_log`,
--   `aggregation_rule_evaluations`). The naming drift is resolved in favor of
--   `control_evaluations`; the issue spec's `control_state` is superseded.
--
-- `evaluated_at` vs `eval_run_id`:
--   `eval_run_id` groups every row produced by a single engine invocation
--   (one EvaluateAll / Replay / per-ingest evaluation). `evaluated_at` is the
--   per-row wall-clock stamp. The computed columns (`result`,
--   `freshness_status`, `evidence_count_in_window`, `last_observed_at`) are
--   deterministic functions of the ledger slice — running the engine twice
--   over the same evidence yields identical computed columns even though
--   `id` / `eval_run_id` / `evaluated_at` differ (AC-3).
--
-- No new enum type for `result`: reuses the slice-002 `evidence_result` enum
-- (pass | fail | na | inconclusive). `freshness_status` and `trigger` are
-- TEXT + CHECK rather than enums — the same choice slices 011/052/054 made:
-- the vocabularies may grow and TEXT + CHECK evolves with a one-line ALTER
-- instead of the ALTER TYPE ... ADD VALUE ceremony.
--
-- Migration is reversible via 20260511000027_control_evaluations.down.sql
-- which drops the table and the scope_cells constraint for a byte-clean
-- up -> down -> up round-trip.
-- ----------------------------------------------------------------------------

-- ===== 1. scope_cells composite key for the cross-tenant-safe FK =====
--
-- `scope_cells` (slice 003) only declared UNIQUE (tenant_id, dimensions_hash).
-- A composite FK target needs a UNIQUE/PK over exactly (tenant_id, id). Add it
-- here so `control_evaluations.(tenant_id, scope_cell_id)` can reference it
-- the same way evidence_records.(tenant_id, control_id) references controls.

ALTER TABLE scope_cells
    ADD CONSTRAINT scope_cells_tenant_id_unique UNIQUE (tenant_id, id);

-- ===== 2. control_evaluations =====

CREATE TABLE control_evaluations (
    id                       UUID PRIMARY KEY,
    tenant_id                UUID NOT NULL,
    -- The control whose state this row records. Composite FK below.
    control_id               UUID NOT NULL,
    -- The applicable scope cell this evaluation is for. NULL is the
    -- degenerate "whole-tenant" evaluation: a control with no
    -- applicability_expr cells resolved, or a control evaluated before any
    -- scope cells exist. When set, the composite FK guarantees the cell
    -- belongs to the same tenant.
    scope_cell_id            UUID NULL,
    -- Groups every row produced by ONE engine invocation. An EvaluateAll /
    -- Replay run stamps the same eval_run_id across all its rows so an
    -- auditor can see "this is the state as of run X".
    eval_run_id              UUID NOT NULL,
    -- Per-row wall-clock stamp. The latest row by evaluated_at per
    -- (control_id, scope_cell_id) is the current state. NOT part of the
    -- deterministic computed state — see header.
    evaluated_at             TIMESTAMPTZ NOT NULL DEFAULT now(),
    -- The computed control state. Reuses the slice-002 evidence_result enum.
    result                   evidence_result NOT NULL,
    -- Freshness of the evidence the result was computed over, relative to the
    -- control's freshness_class window (canvas §2.3). Orthogonal to `result`:
    -- a control can be `pass` + `stale` (the freshest passing evidence is old)
    -- or `pass` + `fresh`. `no_evidence` accompanies a `result` of
    -- `inconclusive`.
    freshness_status         TEXT NOT NULL,
    -- How many evidence records fell inside the freshness window and drove
    -- the result. Zero when freshness_status = 'no_evidence'.
    evidence_count_in_window INT NOT NULL DEFAULT 0,
    -- observed_at of the freshest evidence record for this (control, cell),
    -- regardless of whether it was in-window. NULL when the control has no
    -- evidence at all.
    last_observed_at         TIMESTAMPTZ NULL,
    -- The control's freshness_class at evaluation time, copied onto the row
    -- so the evaluation is self-describing even if the control bundle is
    -- later superseded with a different class.
    freshness_class          TEXT NULL,
    -- What caused this evaluation run. `ingest` = NATS consumer fired on a
    -- new evidence record; `scheduled` = the time-based recompute ticker;
    -- `manual` = an explicit API/CLI trigger; `replay` = a full
    -- recompute-from-ledger (AC-7).
    trigger                  TEXT NOT NULL,
    created_at               TIMESTAMPTZ NOT NULL DEFAULT now(),

    CONSTRAINT control_evaluations_freshness_status_chk
        CHECK (freshness_status IN ('fresh', 'stale', 'no_evidence')),
    CONSTRAINT control_evaluations_trigger_chk
        CHECK (trigger IN ('ingest', 'scheduled', 'manual', 'replay')),
    CONSTRAINT control_evaluations_evidence_count_nonneg
        CHECK (evidence_count_in_window >= 0),
    -- A no_evidence row must have zero in-window records and an inconclusive
    -- result; keeps the ledger internally consistent.
    CONSTRAINT control_evaluations_no_evidence_coherent
        CHECK (
            freshness_status <> 'no_evidence'
            OR (evidence_count_in_window = 0 AND result = 'inconclusive')
        ),

    -- D3: cross-tenant FK leakage blocked. Deleting a control cascades its
    -- evaluation history (the derived state is meaningless without the
    -- control).
    FOREIGN KEY (tenant_id, control_id)
        REFERENCES controls (tenant_id, id) ON DELETE CASCADE,
    -- D3: same for the scope cell. Nullable column — a NULL scope_cell_id
    -- skips the FK check entirely (SQL FK semantics), which is the intended
    -- "whole-tenant" degenerate case.
    FOREIGN KEY (tenant_id, scope_cell_id)
        REFERENCES scope_cells (tenant_id, id) ON DELETE CASCADE
);

-- The hot path: "latest state for this control across its scope cells".
-- (tenant_id, control_id, scope_cell_id, evaluated_at DESC) lets the latest-
-- row-per-cell read land contiguously with a DISTINCT ON / window query.
CREATE INDEX idx_control_evaluations_latest
    ON control_evaluations (tenant_id, control_id, scope_cell_id, evaluated_at DESC);

-- The effectiveness rolling-window read: "every evaluation for this control
-- in the last 30 days". A tenant + control + time-ordered B-tree range scan.
CREATE INDEX idx_control_evaluations_effectiveness
    ON control_evaluations (tenant_id, control_id, evaluated_at DESC);

-- Run-grouped audit: "show me every row from engine run X".
CREATE INDEX idx_control_evaluations_run
    ON control_evaluations (tenant_id, eval_run_id);

-- ===== 3. Row-Level Security — append-only =====
--
-- tenant_read (SELECT) + tenant_write (INSERT) policies ONLY. Under FORCE ROW
-- LEVEL SECURITY, a command with no matching policy is denied for atlas_app —
-- so UPDATE and DELETE are role-denied. The table is append-only by
-- construction. Identical shape to slice 013's evidence_audit_log and slice
-- 054's aggregation_rule_evaluations.

ALTER TABLE control_evaluations ENABLE ROW LEVEL SECURITY;
ALTER TABLE control_evaluations FORCE ROW LEVEL SECURITY;

CREATE POLICY tenant_read ON control_evaluations
    FOR SELECT
    USING (current_tenant_matches(tenant_id));

CREATE POLICY tenant_write ON control_evaluations
    FOR INSERT
    WITH CHECK (current_tenant_matches(tenant_id));

-- Intentionally NO POLICY for UPDATE or DELETE. The GRANT below keeps the
-- privilege so a future BYPASSRLS migration can still run DDL; RLS is what
-- blocks the mutation at the application role.
GRANT SELECT, INSERT, UPDATE, DELETE ON control_evaluations TO atlas_app;
