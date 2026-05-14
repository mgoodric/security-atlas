-- security-atlas — declarative aggregation rules engine (slice 054).
--
-- Three tables in one migration. Builds directly on slice 052's risk
-- hierarchy schema (`risks.level`, `risks.org_unit_id`, `risks.themes`,
-- `risk_aggregations`) and slice 053's severity functions. Implements
-- canvas Plans/canvas/06-risk.md §6.6 "Aggregation rules and roll-up math":
-- a declarative rule defines when child-level risks should auto-generate a
-- parent-level meta-risk, and the engine re-evaluates active rules on every
-- risk write.
--
--   aggregation_rules            - the rule definitions. HYBRID storage:
--                                  the queryable threshold fields are typed
--                                  columns (the engine's hot-path
--                                  "list active rules" + per-rule threshold
--                                  read never has to parse JSON), while the
--                                  full canonical rule body — title
--                                  template, the custom_rego policy bytes,
--                                  and any future fields — rides along in a
--                                  single `rule_body` JSONB column so the
--                                  rule shape can evolve without a
--                                  migration. Same hybrid pattern as
--                                  slice 052/053 (typed risk columns +
--                                  inherent_score JSONB).
--   aggregation_rule_evaluations - append-only evaluation ledger. Canvas
--                                  §6.6 + the issue's AC-8: EVERY rule
--                                  evaluation cycle writes exactly one row,
--                                  even when the outcome is `no_match`.
--                                  This is the audit trail that lets an
--                                  auditor trust the engine is not silently
--                                  missing patterns.
--   aggregation_rule_audit_log   - append-only activation-event log. Every
--                                  status transition (staged -> active,
--                                  active -> inactive, reactivate) and
--                                  every threshold edit writes one row. The
--                                  HITL gate (a human flips staged ->
--                                  active) is the security-sensitive
--                                  surface; this log is its evidence trail.
--
-- Constitutional invariants honored:
--   #6  Tenant isolation at the database layer. All three tables get
--       ENABLE + FORCE ROW LEVEL SECURITY. `aggregation_rules` uses the
--       four-policy split established by slices 011/014/017/018/020
--       (tenant_read FOR SELECT, tenant_write FOR INSERT WITH CHECK,
--       tenant_update FOR UPDATE USING + WITH CHECK, tenant_delete FOR
--       DELETE). The two log tables use SELECT + INSERT policies ONLY; the
--       explicit absence of UPDATE/DELETE policies under FORCE makes them
--       append-only by construction — identical to slice 011's
--       exception_audit_log, slice 013's evidence_audit_log, slice 020's
--       audit_period_audit_log.
--   D3  Cross-tenant FK leakage blocked. Both log tables FK to
--       aggregation_rules via the composite (tenant_id, rule_id) ->
--       aggregation_rules(tenant_id, id) — an insert whose tenant_id does
--       not align with the rule's owning tenant is refused. The engine
--       never reaches across tenants because RLS makes other tenants'
--       rules invisible AND the composite FK is the defense-in-depth guard.
--   #9  Manual aggregation is first-class. Rule-driven meta-risks and
--       slice-053 manual aggregations coexist in the SAME
--       `risk_aggregations` table; the existing nullable `rule_id` column
--       on that table distinguishes the source (NULL = manual, set =
--       rule-driven). This migration does not touch `risk_aggregations`.
--
-- Anti-criteria honored at the schema layer (P0):
--   - No auto-activation: `status` defaults to 'staged'. There is no DDL
--     path that sets 'active' automatically; only an application UPDATE
--     (the PATCH .../activate handler, gated on a human actor) can. The
--     `activated_by` / `activated_at` columns are NULL until that human
--     action and the aggregation_rule_audit_log records who/when.
--   - One meta-risk per (rule_id, window_start): enforced in the engine
--     via an idempotency key. This migration carries `window_start` on
--     aggregation_rule_evaluations so the audit trail shows which window a
--     firing belonged to; the meta-risk de-dup itself keys off
--     risk_aggregations + the meta-risk's inherent_score blob (slice 053
--     aggregation_key pattern) so no schema-level UNIQUE is needed here.
--   - No re-fire on historical data after re-activation: the engine only
--     considers risks written AFTER the rule's most recent activation; the
--     `activated_at` column is the cut-off the engine reads. Recorded in
--     aggregation_rule_evaluations so the behavior is auditable.
--   - custom_rego requires a policy: CHECK `aggregation_rules_rego_present`
--     refuses a row whose severity_function is 'custom_rego' but whose
--     rule_body lacks a non-empty 'custom_rego' string. Defense-in-depth;
--     the application validates first for a friendly error.
--
-- No new enum types: `parent_level` reuses slice 052's `risk_level` enum.
-- `severity_function`, `status`, and the log `outcome` / `event` columns
-- are TEXT + CHECK rather than enums — the same choice slice 052 made for
-- decision constraints[] and slice 011 for exception status: the
-- vocabularies may grow (a v2 severity function, a v2 lifecycle state) and
-- TEXT + CHECK evolves with a one-line ALTER instead of the
-- ALTER TYPE ... ADD VALUE ceremony.
--
-- Migration is reversible via 20260511000026_aggregation_rules.down.sql
-- which drops all three tables in FK-safe order for a byte-clean
-- up -> down -> up round-trip.

-- ===== 1. aggregation_rules =====

CREATE TABLE aggregation_rules (
    id                  UUID PRIMARY KEY,
    tenant_id           UUID NOT NULL,
    -- Human-authored stable identifier ("ownership-cross-team-2026"). This
    -- is what the canvas §6.6 YAML calls `rule_id`. Tenant-unique, not
    -- global — see the composite UNIQUE below. The system uses the UUID
    -- `id` for FKs; `rule_id` is the operator-facing handle.
    rule_id             TEXT NOT NULL,
    -- The theme this rule watches (canvas §6.5 taxonomy). A risk write
    -- whose themes include this value triggers re-evaluation of the rule.
    target_theme        TEXT NOT NULL,
    -- Threshold fields, canvas §6.6. Typed columns (not buried in JSON) so
    -- the engine's per-write hot path reads them without a JSON parse.
    min_risks           INT NOT NULL,
    min_teams           INT NOT NULL,
    window_days         INT NOT NULL,
    -- The level of the meta-risk this rule creates. Reuses slice 052's
    -- risk_level enum ('team' | 'org' | 'company').
    parent_level        risk_level NOT NULL,
    -- One of the four canvas §6.6 severity functions. max/weighted_max/sum
    -- reuse slice 053's already-unit-tested ComputeSeverity; custom_rego
    -- evaluates the OPA Rego policy carried in rule_body.
    severity_function   TEXT NOT NULL,
    -- Full canonical rule body: title_template, the custom_rego policy
    -- bytes (when severity_function = 'custom_rego'), and any future
    -- fields. The typed columns above are a denormalized projection of the
    -- queryable subset; rule_body is the complete record.
    rule_body           JSONB NOT NULL,
    -- Lifecycle. Defaults to 'staged' — the HITL gate. Only an explicit
    -- human-driven UPDATE moves a rule to 'active'. 'inactive' stops new
    -- firings while preserving historical meta-risks.
    status              TEXT NOT NULL DEFAULT 'staged',
    -- Set when a human activates the rule (PATCH .../activate). NULL until
    -- then. The engine reads `activated_at` as the "do not consider risks
    -- older than this" cut-off so re-activation never re-fires on stale
    -- data (anti-criterion P0).
    activated_by        TEXT NULL,
    activated_at        TIMESTAMPTZ NULL,
    created_at          TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at          TIMESTAMPTZ NOT NULL DEFAULT now(),

    -- Tenant-scoped uniqueness on the human-readable id, not a global PK.
    UNIQUE (tenant_id, rule_id),
    -- Composite UNIQUE so the two log tables can FK by (tenant_id, id)
    -- with a cross-tenant-safe target. Same D3 pattern slice 052 added to
    -- org_units / decisions and slice 011 to exceptions.
    UNIQUE (tenant_id, id),

    CONSTRAINT aggregation_rules_rule_id_nonempty
        CHECK (length(rule_id) > 0),
    CONSTRAINT aggregation_rules_target_theme_nonempty
        CHECK (length(target_theme) > 0),
    -- Thresholds must be positive. A rule with min_risks = 0 would fire on
    -- an empty set; min_teams = 0 / window_days = 0 are equally nonsensical.
    CONSTRAINT aggregation_rules_min_risks_positive
        CHECK (min_risks > 0),
    CONSTRAINT aggregation_rules_min_teams_positive
        CHECK (min_teams > 0),
    CONSTRAINT aggregation_rules_window_days_positive
        CHECK (window_days > 0),
    CONSTRAINT aggregation_rules_severity_function_chk
        CHECK (severity_function IN ('max', 'weighted_max', 'sum', 'custom_rego')),
    CONSTRAINT aggregation_rules_status_chk
        CHECK (status IN ('staged', 'active', 'inactive')),
    -- A 'custom_rego' rule MUST carry a non-empty Rego policy string in
    -- rule_body->>'custom_rego'. Defense-in-depth: the application
    -- validates first (friendly 400), the DB CHECK is the backstop.
    CONSTRAINT aggregation_rules_rego_present
        CHECK (
            severity_function <> 'custom_rego'
            OR (
                rule_body ? 'custom_rego'
                AND jsonb_typeof(rule_body->'custom_rego') = 'string'
                AND length(rule_body->>'custom_rego') > 0
            )
        ),
    -- An active/inactive rule must have been activated by someone at some
    -- point. A 'staged' rule must NOT have activation metadata. Keeps the
    -- HITL audit story coherent at the schema layer.
    CONSTRAINT aggregation_rules_activation_coherent
        CHECK (
            (status = 'staged' AND activated_by IS NULL AND activated_at IS NULL)
            OR (status IN ('active', 'inactive') AND activated_by IS NOT NULL AND activated_at IS NOT NULL)
        )
);

-- The engine's hot path: "give me every active rule for this tenant" runs
-- on every risk write. status is selective (most rules are staged or
-- inactive at any time), so a composite (tenant_id, status) lands the
-- active rows contiguously.
CREATE INDEX idx_aggregation_rules_tenant_status
    ON aggregation_rules (tenant_id, status);

-- General time-ordered listing for the GET /v1/aggregation_rules surface.
CREATE INDEX idx_aggregation_rules_tenant_created_at
    ON aggregation_rules (tenant_id, created_at DESC);

-- ===== 2. aggregation_rule_evaluations =====
--
-- Append-only evaluation ledger. One row per rule per evaluation cycle,
-- canvas §6.6 + AC-8 — including `no_match` outcomes so an auditor can see
-- the engine ran and found nothing, rather than wondering if it ran at all.

CREATE TABLE aggregation_rule_evaluations (
    id              UUID PRIMARY KEY,
    tenant_id       UUID NOT NULL,
    -- FK to the UUID id of the rule, composite for cross-tenant safety.
    -- ON DELETE CASCADE: deleting a rule cleans up its evaluation history
    -- (the meta-risks it created survive — they live in `risks` and are
    -- not FK-linked back to the rule).
    rule_id         UUID NOT NULL,
    evaluated_at    TIMESTAMPTZ NOT NULL DEFAULT now(),
    -- fired       - threshold met; a meta-risk was created or updated.
    -- near_miss   - at least one threshold dimension was met but not all
    --               (e.g. enough risks but not enough teams). Surfaced so
    --               operators can see a rule is "close" and tune it.
    -- no_match    - no threshold dimension met. Still logged (AC-8).
    outcome         TEXT NOT NULL,
    -- The counts the engine observed this cycle. risk_count / team_count
    -- are what the threshold was compared against.
    risk_count      INT NOT NULL,
    team_count      INT NOT NULL,
    -- Set only when outcome = 'fired': the window boundary the meta-risk
    -- was keyed to. NULL for near_miss / no_match.
    window_start    TIMESTAMPTZ NULL,
    -- Set only when outcome = 'fired': the meta-risk created or updated.
    -- Not a FK — the audit trail must survive meta-risk deletion.
    meta_risk_id    UUID NULL,
    created_at      TIMESTAMPTZ NOT NULL DEFAULT now(),

    CONSTRAINT aggregation_rule_evaluations_outcome_chk
        CHECK (outcome IN ('fired', 'near_miss', 'no_match')),
    CONSTRAINT aggregation_rule_evaluations_counts_nonneg
        CHECK (risk_count >= 0 AND team_count >= 0),
    -- A 'fired' row must name its window and its meta-risk; a non-fired row
    -- must not. Keeps the ledger internally consistent.
    CONSTRAINT aggregation_rule_evaluations_fired_coherent
        CHECK (
            (outcome = 'fired' AND window_start IS NOT NULL AND meta_risk_id IS NOT NULL)
            OR (outcome <> 'fired' AND window_start IS NULL AND meta_risk_id IS NULL)
        ),

    FOREIGN KEY (tenant_id, rule_id)
        REFERENCES aggregation_rules(tenant_id, id) ON DELETE CASCADE
);

-- Per-rule evaluation history, newest first — the auditor view and the
-- engine's "when did this rule last fire" lookup.
CREATE INDEX idx_aggregation_rule_evaluations_tenant_rule
    ON aggregation_rule_evaluations (tenant_id, rule_id, evaluated_at DESC);

-- ===== 3. aggregation_rule_audit_log =====
--
-- Append-only activation-event log. The HITL gate's evidence trail: every
-- staged -> active flip, every deactivation/reactivation, and every
-- threshold edit writes one row naming the human actor.

CREATE TABLE aggregation_rule_audit_log (
    id              UUID PRIMARY KEY,
    tenant_id       UUID NOT NULL,
    rule_id         UUID NOT NULL,
    -- created | activated | deactivated | reactivated | threshold_changed
    event           TEXT NOT NULL,
    -- The human (or service principal) who performed the action. The HITL
    -- guarantee: an activation is never anonymous.
    actor           TEXT NOT NULL,
    from_status     TEXT NULL,
    to_status       TEXT NULL,
    -- Structured detail — for threshold_changed, the before/after values;
    -- for activation events, optional reviewer notes. Defaults to '{}'.
    detail          JSONB NOT NULL DEFAULT '{}'::jsonb,
    created_at      TIMESTAMPTZ NOT NULL DEFAULT now(),

    CONSTRAINT aggregation_rule_audit_log_event_chk
        CHECK (event IN ('created', 'activated', 'deactivated', 'reactivated', 'threshold_changed')),
    CONSTRAINT aggregation_rule_audit_log_actor_nonempty
        CHECK (length(actor) > 0),
    CONSTRAINT aggregation_rule_audit_log_from_status_chk
        CHECK (from_status IS NULL OR from_status IN ('staged', 'active', 'inactive')),
    CONSTRAINT aggregation_rule_audit_log_to_status_chk
        CHECK (to_status IS NULL OR to_status IN ('staged', 'active', 'inactive')),

    FOREIGN KEY (tenant_id, rule_id)
        REFERENCES aggregation_rules(tenant_id, id) ON DELETE CASCADE
);

-- Per-rule activation history, newest first.
CREATE INDEX idx_aggregation_rule_audit_log_tenant_rule
    ON aggregation_rule_audit_log (tenant_id, rule_id, created_at DESC);

-- ===== 4. risks performance index (AC-9) =====
--
-- The engine's candidate-risk query filters `risks` by
--   (tenant_id, themes @> ARRAY[target_theme], created_at >= window_start).
-- Slice 052's idx_risks_themes_gin handles theme containment but a GIN
-- index cannot carry the created_at range or give the planner a tenant +
-- time-ordered scan. For the AC-9 target (200ms p95, tenant with 500
-- active risks + 10 active rules) the planner wants a B-tree range scan on
-- (tenant_id, created_at) and then an in-memory theme filter on the small
-- window slice. Partial — only risks that carry at least one theme are
-- ever aggregation candidates, which keeps the index small.
CREATE INDEX idx_risks_tenant_created_themes
    ON risks (tenant_id, created_at DESC)
    WHERE array_length(themes, 1) > 0;

-- ===== Row-Level Security =====

ALTER TABLE aggregation_rules ENABLE ROW LEVEL SECURITY;
ALTER TABLE aggregation_rules FORCE ROW LEVEL SECURITY;
CREATE POLICY tenant_read ON aggregation_rules
    FOR SELECT USING (current_tenant_matches(tenant_id));
CREATE POLICY tenant_write ON aggregation_rules
    FOR INSERT WITH CHECK (current_tenant_matches(tenant_id));
CREATE POLICY tenant_update ON aggregation_rules
    FOR UPDATE USING (current_tenant_matches(tenant_id))
    WITH CHECK (current_tenant_matches(tenant_id));
CREATE POLICY tenant_delete ON aggregation_rules
    FOR DELETE USING (current_tenant_matches(tenant_id));

-- aggregation_rule_evaluations is append-only by construction: SELECT +
-- INSERT policies only. No UPDATE/DELETE policy under FORCE ROW LEVEL
-- SECURITY means atlas_app cannot mutate evaluation rows. Mirrors slice
-- 011's exception_audit_log, slice 013's evidence_audit_log, slice 020's
-- audit_period_audit_log.
ALTER TABLE aggregation_rule_evaluations ENABLE ROW LEVEL SECURITY;
ALTER TABLE aggregation_rule_evaluations FORCE ROW LEVEL SECURITY;
CREATE POLICY tenant_read ON aggregation_rule_evaluations
    FOR SELECT USING (current_tenant_matches(tenant_id));
CREATE POLICY tenant_write ON aggregation_rule_evaluations
    FOR INSERT WITH CHECK (current_tenant_matches(tenant_id));

-- aggregation_rule_audit_log is append-only by construction: same pattern.
ALTER TABLE aggregation_rule_audit_log ENABLE ROW LEVEL SECURITY;
ALTER TABLE aggregation_rule_audit_log FORCE ROW LEVEL SECURITY;
CREATE POLICY tenant_read ON aggregation_rule_audit_log
    FOR SELECT USING (current_tenant_matches(tenant_id));
CREATE POLICY tenant_write ON aggregation_rule_audit_log
    FOR INSERT WITH CHECK (current_tenant_matches(tenant_id));

GRANT SELECT, INSERT, UPDATE, DELETE ON aggregation_rules TO atlas_app;
GRANT SELECT, INSERT ON aggregation_rule_evaluations TO atlas_app;
GRANT SELECT, INSERT ON aggregation_rule_audit_log TO atlas_app;
