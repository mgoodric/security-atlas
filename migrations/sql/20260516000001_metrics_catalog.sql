-- security-atlas — metrics catalog + cascade + observation store (slice 076).
--
-- Implements docs/issues/076-metrics-catalog-cascade.md AC-1.
--
-- ----------------------------------------------------------------------------
-- WHAT THIS IS
-- ----------------------------------------------------------------------------
-- A 5-table backbone for the platform's measurement surface:
--
--   metrics_catalog          — singleton, tenant-agnostic catalog of
--                              definitions ("Audit readiness", "Open risk
--                              financial exposure", etc.). Platform-seeded
--                              from catalogs/metrics/*.yaml at boot.
--   metric_cascade_edges     — singleton, tenant-agnostic graph of
--                              parent → child relationships between catalog
--                              metrics. Encodes how a board metric composes
--                              from program + team metrics.
--   metric_observations      — tenant-scoped, append-only series of
--                              measurements. One row per (tenant, metric,
--                              observed_at, dimensions). Written by either
--                              the computed evaluator cron or the
--                              metric_inputs replicating trigger (manual).
--   metric_targets           — tenant-scoped, upsert-in-place per
--                              (tenant, metric) — the per-tenant target +
--                              warning + critical thresholds + direction.
--   metric_inputs            — tenant-scoped, append-only audit trail of
--                              manual entries. Each insert triggers a
--                              matching metric_observations row so the
--                              read API serves a unified series.
--
-- ----------------------------------------------------------------------------
-- CONSTITUTIONAL INVARIANTS HONORED
-- ----------------------------------------------------------------------------
--   #1 (One control, N framework satisfactions)
--      Metrics that aggregate across frameworks (per-framework coverage,
--      audit readiness) read the existing SCF-anchor + framework_scope
--      graph, never duplicated per-framework stores. The catalog defines
--      ONE metric "Audit readiness"; cascade edges and computed evaluators
--      handle the per-framework breakdown.
--
--   #6 (Tenant isolation at the DB layer)
--      Every tenant-scoped table (`metric_observations`, `metric_targets`,
--      `metric_inputs`) carries `tenant_id` and either the four-policy RLS
--      pattern (metric_targets — UPSERT semantics) or the two-policy
--      append-only pattern (metric_observations + metric_inputs).
--      `metrics_catalog` + `metric_cascade_edges` are intentionally
--      tenant-agnostic singletons (slice 068 pattern adapted from
--      schema_registry) — the catalog itself is platform-shared, and
--      tenant-customised metric definitions are a future slice.
--
--   #9 (Manual evidence is first-class)
--      The metric_inputs insert trigger replicates each manual entry to
--      metric_observations so consumers (board pack, dashboard, OSCAL
--      export) read ONE unified series — manual and computed observations
--      use the same read shape. Trigger fires AFTER INSERT and runs as the
--      table owner so it bypasses the append-only RLS on metric_observations
--      (which would otherwise block the cross-table replication from the
--      user-bound atlas_app session).
--
-- ----------------------------------------------------------------------------
-- WHY THE CASCADE IS A GRAPH, NOT A TREE
-- ----------------------------------------------------------------------------
-- A program metric like "Evidence freshness %" is a child of BOTH the
-- "Audit readiness" board metric AND the "Program effectiveness" board
-- metric. A strict tree would force duplication; an edge list with PK
-- (parent_id, child_id) lets the graph hold the same child under multiple
-- parents without duplication. Recursive-CTE traversal in
-- `internal/db/queries/metrics.sql` walks the DAG; cycle prevention
-- happens at YAML seed time in `internal/catalog/metrics/seed.go` (an
-- app-layer topological-sort check, louder than a DB trigger error at
-- runtime). A CHECK constraint here forbids the trivial self-loop case so
-- malformed seed data still fails closed at the DB.
--
-- ----------------------------------------------------------------------------
-- WHY metric_inputs AND metric_observations ARE DISTINCT TABLES
-- ----------------------------------------------------------------------------
-- metric_inputs is the AUDIT TRAIL: who entered what value, when, with what
-- justification. metric_observations is the READ-OPTIMISED SERIES. The
-- trigger keeps them coherent so a query of either table reports the same
-- series, but the audit trail's "who and why" fields stay in metric_inputs
-- where the read API for audit log lookups can pull them without polluting
-- the observation read path.
--
-- The split also keeps the schema honest under freezing semantics
-- (canvas §8.4): an AuditPeriod's evidence horizon is about *evidence*, not
-- about platform-internal posture telemetry. Metric observations are
-- platform-internal telemetry; they are NOT subject to evidence-window
-- freezing (the freezing applies to the underlying evidence_records read
-- by the audit_readiness_score evaluator's source data, not to the
-- observation rows the evaluator writes).
--
-- ----------------------------------------------------------------------------
-- AUDIT-PERIOD FREEZING (canvas §8.4) AND METRICS
-- ----------------------------------------------------------------------------
-- Metric observations describe platform posture telemetry; they are not
-- evidence records and they are not subject to the AuditPeriod
-- frozen-horizon read. The audit_readiness_score evaluator (the only
-- evaluator that touches freezing semantics) READS audit_periods to
-- judge currency but WRITES an observation row tagged with `observed_at`
-- equal to the evaluator-run wall clock. A frozen AuditPeriod does not
-- retroactively edit observations; live and frozen reads remain distinct
-- surfaces. Documented in `docs/audit-log/076-metrics-catalog-cascade-decisions.md`.
-- ----------------------------------------------------------------------------

-- ===== 1. metrics_catalog =====
--
-- Singleton, tenant-agnostic. id is a text slug (e.g. "audit_readiness")
-- because YAML authors and operators read these by name. Slice 068's
-- schema_registry uses the same pattern.

CREATE TABLE metrics_catalog (
    id                  TEXT PRIMARY KEY,
    tenant_id           UUID NULL,
    level               TEXT NOT NULL,
    category            TEXT NOT NULL,
    name                TEXT NOT NULL,
    description         TEXT NOT NULL,
    unit                TEXT NOT NULL,
    cadence             TEXT NOT NULL,
    compute_strategy    TEXT NOT NULL,
    compute_evaluator   TEXT NULL,
    source_slices       TEXT[] NOT NULL DEFAULT ARRAY[]::TEXT[],
    notes               TEXT NOT NULL DEFAULT '',
    created_at          TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at          TIMESTAMPTZ NOT NULL DEFAULT now(),

    CONSTRAINT metrics_catalog_level_chk
        CHECK (level IN ('board', 'program', 'team')),
    CONSTRAINT metrics_catalog_cadence_chk
        CHECK (cadence IN ('realtime', 'daily', 'weekly', 'monthly', 'quarterly')),
    CONSTRAINT metrics_catalog_compute_strategy_chk
        CHECK (compute_strategy IN ('computed', 'manual_input', 'external_integration')),
    -- A computed metric MUST name its Go evaluator. A non-computed metric
    -- MUST NOT name one (the absence of a registered evaluator is the
    -- contract that the cron skips it).
    CONSTRAINT metrics_catalog_evaluator_iff_computed
        CHECK (
            (compute_strategy = 'computed'    AND compute_evaluator IS NOT NULL AND length(compute_evaluator) > 0)
         OR (compute_strategy <> 'computed'   AND compute_evaluator IS NULL)
        )
);

-- Lookup index for the hot path: list catalog by level for the cascade
-- entry-point reads (GET /v1/metrics?level=board).
CREATE INDEX idx_metrics_catalog_level
    ON metrics_catalog (level, category);

ALTER TABLE metrics_catalog ENABLE ROW LEVEL SECURITY;
ALTER TABLE metrics_catalog FORCE ROW LEVEL SECURITY;
-- Reads: global rows (tenant_id NULL) visible to every tenant; tenant
-- rows visible only when GUC matches. Slice 068 pattern.
CREATE POLICY tenant_or_catalog_read ON metrics_catalog
    FOR SELECT
    USING (tenant_id IS NULL OR current_tenant_matches(tenant_id));
-- Writes: tenant-only. The platform-seeded NULL rows are inserted by the
-- atlas_migrate BYPASSRLS role at boot. atlas_app may neither insert nor
-- mutate global rows.
CREATE POLICY tenant_write ON metrics_catalog
    FOR INSERT
    WITH CHECK (tenant_id IS NOT NULL AND current_tenant_matches(tenant_id));
CREATE POLICY tenant_update ON metrics_catalog
    FOR UPDATE
    USING (tenant_id IS NOT NULL AND current_tenant_matches(tenant_id))
    WITH CHECK (tenant_id IS NOT NULL AND current_tenant_matches(tenant_id));
CREATE POLICY tenant_delete ON metrics_catalog
    FOR DELETE
    USING (tenant_id IS NOT NULL AND current_tenant_matches(tenant_id));

GRANT SELECT, INSERT, UPDATE, DELETE ON metrics_catalog TO atlas_app;

-- ===== 2. metric_cascade_edges =====
--
-- Singleton, tenant-agnostic. PRIMARY KEY (parent_id, child_id) prevents
-- duplicate edges. CHECK forbids the trivial self-loop case. Multi-hop
-- cycle detection lives in the YAML seeder (app-layer topological sort,
-- D2 in the decisions log).

CREATE TABLE metric_cascade_edges (
    parent_id    TEXT NOT NULL REFERENCES metrics_catalog(id) ON DELETE CASCADE,
    child_id     TEXT NOT NULL REFERENCES metrics_catalog(id) ON DELETE CASCADE,
    weight       NUMERIC(5,4) NOT NULL DEFAULT 1.0,
    notes        TEXT NOT NULL DEFAULT '',
    created_at   TIMESTAMPTZ NOT NULL DEFAULT now(),

    CONSTRAINT metric_cascade_edges_pk
        PRIMARY KEY (parent_id, child_id),
    CONSTRAINT metric_cascade_edges_no_self_loop
        CHECK (parent_id <> child_id),
    -- v1 hardcodes 1.0; the column accepts (0,1] so a future weighted
    -- rollup slice can tune without a schema change.
    CONSTRAINT metric_cascade_edges_weight_range
        CHECK (weight > 0 AND weight <= 1)
);

-- Reverse-direction lookup for "list all parents of a child" (the
-- GET /v1/metrics/{id} response's parents field).
CREATE INDEX idx_metric_cascade_edges_child ON metric_cascade_edges (child_id);

ALTER TABLE metric_cascade_edges ENABLE ROW LEVEL SECURITY;
ALTER TABLE metric_cascade_edges FORCE ROW LEVEL SECURITY;
-- Singleton catalog edges are platform-shared; reads are public to every
-- authenticated context. There is no tenant_id on the table so the
-- USING (true) policy is exhaustive.
CREATE POLICY public_read ON metric_cascade_edges
    FOR SELECT
    USING (true);
-- No write policies for atlas_app; the platform seeder runs as
-- atlas_migrate (BYPASSRLS).

GRANT SELECT ON metric_cascade_edges TO atlas_app;

-- ===== 3. metric_observations =====
--
-- Tenant-scoped, append-only. The unified read surface for both computed
-- and manual values. Written by the cron evaluator path AND by the
-- metric_inputs replicate trigger (see ===== 6 below).

CREATE TABLE metric_observations (
    id              UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    tenant_id       UUID NOT NULL,
    metric_id       TEXT NOT NULL REFERENCES metrics_catalog(id) ON DELETE CASCADE,
    observed_at     TIMESTAMPTZ NOT NULL,
    numeric_value   NUMERIC(20,6) NOT NULL,
    dimensions      JSONB NOT NULL DEFAULT '{}'::jsonb,
    source          TEXT NOT NULL,
    created_at      TIMESTAMPTZ NOT NULL DEFAULT now(),

    CONSTRAINT metric_observations_source_nonempty
        CHECK (length(source) > 0)
);

-- The series read: "observations for this tenant + metric, ordered by
-- observed_at" with optional window bounds. tenant-prefixed B-tree over
-- (metric_id, observed_at DESC) lets the keyset-paginated ListObservations
-- query land contiguously.
CREATE INDEX idx_metric_observations_series
    ON metric_observations (tenant_id, metric_id, observed_at DESC);

ALTER TABLE metric_observations ENABLE ROW LEVEL SECURITY;
ALTER TABLE metric_observations FORCE ROW LEVEL SECURITY;
-- Append-only by construction: tenant_read SELECT + tenant_write INSERT
-- policies ONLY. Under FORCE ROW LEVEL SECURITY the absence of
-- UPDATE/DELETE policies means atlas_app cannot mutate observation rows.
-- Mirrors slice 013's evidence_audit_log, slice 026's
-- aggregation_rule_evaluations, slice 012's control_evaluations.
CREATE POLICY tenant_read ON metric_observations
    FOR SELECT
    USING (current_tenant_matches(tenant_id));
CREATE POLICY tenant_write ON metric_observations
    FOR INSERT
    WITH CHECK (current_tenant_matches(tenant_id));

GRANT SELECT, INSERT ON metric_observations TO atlas_app;

-- ===== 4. metric_targets =====
--
-- Tenant-scoped, upsert-in-place. One row per (tenant, metric). Carries
-- the target + warning + critical thresholds + direction so the read
-- API can render a metric value with its goal context.

CREATE TABLE metric_targets (
    id                    UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    tenant_id             UUID NOT NULL,
    metric_id             TEXT NOT NULL REFERENCES metrics_catalog(id) ON DELETE CASCADE,
    target_value          NUMERIC(20,6) NULL,
    warning_threshold     NUMERIC(20,6) NULL,
    critical_threshold    NUMERIC(20,6) NULL,
    direction             TEXT NOT NULL,
    owner_user_id         UUID NULL,
    notes                 TEXT NOT NULL DEFAULT '',
    created_at            TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at            TIMESTAMPTZ NOT NULL DEFAULT now(),

    CONSTRAINT metric_targets_direction_chk
        CHECK (direction IN ('higher_is_better', 'lower_is_better', 'target_is_better')),
    -- UPSERT target: one target row per (tenant, metric). PUT
    -- /v1/metrics/{id}/target uses this unique key.
    CONSTRAINT metric_targets_tenant_metric_uniq
        UNIQUE (tenant_id, metric_id)
);

CREATE INDEX idx_metric_targets_metric
    ON metric_targets (tenant_id, metric_id);

ALTER TABLE metric_targets ENABLE ROW LEVEL SECURITY;
ALTER TABLE metric_targets FORCE ROW LEVEL SECURITY;
-- Four-policy split (UPSERT semantics). Same shape as
-- evidence_freshness, aggregation_rules.
CREATE POLICY tenant_read ON metric_targets
    FOR SELECT
    USING (current_tenant_matches(tenant_id));
CREATE POLICY tenant_write ON metric_targets
    FOR INSERT
    WITH CHECK (current_tenant_matches(tenant_id));
CREATE POLICY tenant_update ON metric_targets
    FOR UPDATE
    USING (current_tenant_matches(tenant_id))
    WITH CHECK (current_tenant_matches(tenant_id));
CREATE POLICY tenant_delete ON metric_targets
    FOR DELETE
    USING (current_tenant_matches(tenant_id));

GRANT SELECT, INSERT, UPDATE, DELETE ON metric_targets TO atlas_app;

-- ===== 5. metric_inputs =====
--
-- Tenant-scoped, append-only. The audit trail for manual entries. Each
-- INSERT triggers a matching metric_observations row (see ===== 6 below)
-- so the read API serves a unified series.

CREATE TABLE metric_inputs (
    id                    UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    tenant_id             UUID NOT NULL,
    metric_id             TEXT NOT NULL REFERENCES metrics_catalog(id) ON DELETE CASCADE,
    input_at              TIMESTAMPTZ NOT NULL,
    numeric_value         NUMERIC(20,6) NOT NULL,
    dimensions            JSONB NOT NULL DEFAULT '{}'::jsonb,
    entered_by_user_id    UUID NOT NULL,
    notes                 TEXT NOT NULL DEFAULT '',
    created_at            TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE INDEX idx_metric_inputs_metric
    ON metric_inputs (tenant_id, metric_id, input_at DESC);

ALTER TABLE metric_inputs ENABLE ROW LEVEL SECURITY;
ALTER TABLE metric_inputs FORCE ROW LEVEL SECURITY;
-- Append-only by construction: tenant_read SELECT + tenant_write INSERT
-- policies ONLY. Audit-trail semantics — no UPDATE or DELETE path even
-- for the row's creator.
CREATE POLICY tenant_read ON metric_inputs
    FOR SELECT
    USING (current_tenant_matches(tenant_id));
CREATE POLICY tenant_write ON metric_inputs
    FOR INSERT
    WITH CHECK (current_tenant_matches(tenant_id));

GRANT SELECT, INSERT ON metric_inputs TO atlas_app;

-- ===== 6. Insert trigger: metric_inputs → metric_observations =====
--
-- The trigger runs as the function's SECURITY DEFINER context (the owner
-- of the function — the migration role, BYPASSRLS) so the INSERT into
-- metric_observations is not blocked by atlas_app's RLS context. The
-- inserted observation row carries the SAME tenant_id as the input row
-- so a cross-tenant injection is impossible: the input row's RLS already
-- enforced tenant_id matches the caller's GUC.
--
-- The source column on the observation distinguishes manual entries
-- ("manual:user-uuid") from computed entries ("evaluator:name") so the
-- read API can render provenance.
--
-- Idempotency: each metric_inputs INSERT produces exactly one
-- metric_observations row. The trigger inserts unconditionally; the
-- caller's input INSERT is the idempotency boundary upstream (the
-- HTTP handler is the only path).

CREATE OR REPLACE FUNCTION fn_metric_inputs_replicate()
RETURNS TRIGGER
LANGUAGE plpgsql
SECURITY DEFINER
SET search_path = public, pg_temp
AS $$
BEGIN
    INSERT INTO metric_observations (
        tenant_id,
        metric_id,
        observed_at,
        numeric_value,
        dimensions,
        source
    ) VALUES (
        NEW.tenant_id,
        NEW.metric_id,
        NEW.input_at,
        NEW.numeric_value,
        NEW.dimensions,
        'manual:' || NEW.entered_by_user_id::text
    );
    RETURN NEW;
END;
$$;

CREATE TRIGGER trg_metric_inputs_replicate
    AFTER INSERT ON metric_inputs
    FOR EACH ROW
    EXECUTE FUNCTION fn_metric_inputs_replicate();
