-- security-atlas — exception / waiver workflow (slice 021).
--
-- Two tables in one migration:
--   exceptions             - mutable state for the time-bounded, scope-bounded
--                            waiver of a control's normal evaluation. Lifecycle
--                            transitions are gated by the application layer
--                            (the state column is a denormalized projection of
--                            the requested/approved/denied/activated/expired_at
--                            timestamp columns -- the application is the
--                            single writer).
--   exception_audit_log    - append-only state-transition log. Every lifecycle
--                            event writes one row including the system-driven
--                            auto-expiry tick (anti-criterion P0: no silent
--                            expiry).
--
-- Constitutional invariants honored:
--   #6  Tenant isolation at the database layer. Both tables get FORCE ROW
--       LEVEL SECURITY. `exceptions` uses the four-policy split established
--       by slices 014/017/018/036 (tenant_read FOR SELECT, tenant_write FOR
--       INSERT WITH CHECK, tenant_update FOR UPDATE USING + WITH CHECK,
--       tenant_delete FOR DELETE). `exception_audit_log` uses SELECT + INSERT
--       policies ONLY; the explicit absence of UPDATE/DELETE policies under
--       FORCE makes the table append-only by construction. Same pattern as
--       slice 013's evidence_audit_log, slice 026's sample_audit_log, and
--       slice 036's artifact_access_log.
--   D3  Cross-tenant FK leakage blocked. Composite FK (tenant_id, control_id)
--       -> controls(tenant_id, id) refuses any insert whose tenant_id does
--       not align with the control's owning tenant.
--   #9  Manual evidence is first-class. Exceptions are evidence-trail recorded
--       (every transition logged); auditors see the explicit waiver chain.
--
-- Anti-criteria honored at the schema layer (P0):
--   - Auto-renewal forbidden: there is no UPDATE path that extends
--     expires_at past its initial setting. The application rejects PATCH on
--     expires_at; defense-in-depth at the DB is the CHECK
--     `exceptions_max_365d` which clamps to <= requested_at + 365 days at
--     INSERT time. Auto-renewal would have to re-INSERT a new row (which
--     IS the canvas-intended workflow).
--   - 365-day cap enforced at the schema: CHECK `exceptions_max_365d`.
--   - Silent expiry forbidden: see exception_audit_log; the cron writes a
--     row per expired exception. No path mutates exceptions.status without
--     a corresponding audit row (application-layer invariant, exercised by
--     integration test).
--
-- Migration is reversible via 20260511000011_exceptions.down.sql which
-- drops both tables in dependency order.

-- ===== 1. exceptions =====

CREATE TABLE exceptions (
    id                        UUID PRIMARY KEY,
    tenant_id                 UUID NOT NULL,
    control_id                UUID NOT NULL,
    -- scope_cell_predicate uses the slice-017 JSON-AST shape. Empty object
    -- {} and {"op":"true"} both mean "applies to every cell" (canvas §5.4
    -- isEmptyExpr semantics). The application canonicalizes on the write
    -- path; the DB only stores the canonical form.
    scope_cell_predicate      JSONB NOT NULL,
    justification             TEXT NOT NULL,
    -- compensating_controls is free-form narrative -- "what we're doing
    -- instead". A future slice may add a sibling
    -- compensating_control_ids UUID[] for structured FK linkage; this
    -- slice ships only the freeform shape because compensating mitigations
    -- are often informal ("weekly SRE manual review until IAM federation
    -- lands"). See CONTEXT.md for the canonical definition.
    compensating_controls     TEXT[] NOT NULL DEFAULT '{}',
    requested_by              TEXT NOT NULL,
    requested_at              TIMESTAMPTZ NOT NULL DEFAULT now(),
    approved_by               TEXT NULL,
    approved_at               TIMESTAMPTZ NULL,
    denied_by                 TEXT NULL,
    denied_at                 TIMESTAMPTZ NULL,
    activated_by              TEXT NULL,
    activated_at              TIMESTAMPTZ NULL,
    effective_from            TIMESTAMPTZ NULL,
    expires_at                TIMESTAMPTZ NOT NULL,
    expired_at                TIMESTAMPTZ NULL,
    status                    TEXT NOT NULL DEFAULT 'requested',
    created_at                TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at                TIMESTAMPTZ NOT NULL DEFAULT now(),

    CONSTRAINT exceptions_status_chk
        CHECK (status IN ('requested', 'approved', 'denied', 'active', 'expired')),
    CONSTRAINT exceptions_justification_nonempty
        CHECK (length(justification) > 0),
    CONSTRAINT exceptions_requested_by_nonempty
        CHECK (length(requested_by) > 0),
    -- AC-2 + anti-criterion P0: expires_at cannot exceed 365 days from the
    -- request time. The application also enforces this against now() at
    -- request time so the error message is friendly; the DB constraint is
    -- defense-in-depth.
    CONSTRAINT exceptions_max_365d
        CHECK (expires_at <= requested_at + INTERVAL '365 days'),
    -- Segregation of duties: approver MUST differ from requester. NULL
    -- approved_by is fine (pre-approval). When set, it cannot equal the
    -- requester. The application returns 403; the DB CHECK is the
    -- defense-in-depth guard.
    CONSTRAINT exceptions_sod
        CHECK (approved_by IS NULL OR approved_by <> requested_by),
    CONSTRAINT exceptions_denied_by_sod
        CHECK (denied_by IS NULL OR denied_by <> requested_by),

    -- D3 invariant: composite FK blocks cross-tenant control references.
    -- ON DELETE RESTRICT because deleting a control out from under an
    -- active exception would orphan the waiver -- safer to require the
    -- exception be cleaned up first.
    FOREIGN KEY (tenant_id, control_id)
        REFERENCES controls(tenant_id, id) ON DELETE RESTRICT
);

-- Composite UNIQUE on (tenant_id, id) lets other tables FK to exceptions
-- with a cross-tenant-safe target. None do in this slice, but it costs
-- nothing and removes a class of future-slice rework.
ALTER TABLE exceptions
    ADD CONSTRAINT exceptions_tenant_id_unique UNIQUE (tenant_id, id);

-- Active + expiring queries (AC-4 read accessor, AC-6 calendar) hit this
-- composite. Status is selective (only a few exceptions are 'active' at any
-- time); expires_at gives the calendar its sort order.
CREATE INDEX idx_exceptions_tenant_status_expires
    ON exceptions (tenant_id, status, expires_at);

-- Control-scoped queries (Active(controlID)) hit this; status is the
-- innermost filter so 'active' rows for a control land contiguously.
CREATE INDEX idx_exceptions_tenant_control
    ON exceptions (tenant_id, control_id, status);

-- General time-ordered listing
CREATE INDEX idx_exceptions_tenant_created_at
    ON exceptions (tenant_id, created_at DESC);

-- ===== 2. exception_audit_log =====
--
-- Append-only state-transition log. No FK to exceptions because the audit
-- trail must survive exception deletion (admin-cleanup or future
-- soft-delete) -- the exception_id is preserved verbatim. Tenant-scoped
-- via RLS plus the composite (tenant_id, exception_id) lookup pattern.

CREATE TABLE exception_audit_log (
    id              UUID PRIMARY KEY,
    tenant_id       UUID NOT NULL,
    exception_id    UUID NOT NULL,
    action          TEXT NOT NULL,
    actor           TEXT NOT NULL,
    from_state      TEXT NULL,
    to_state        TEXT NOT NULL,
    reason          TEXT NOT NULL DEFAULT '',
    occurred_at     TIMESTAMPTZ NOT NULL DEFAULT now(),

    CONSTRAINT exception_audit_log_action_chk
        CHECK (action IN ('requested', 'approved', 'denied', 'activated', 'expired')),
    CONSTRAINT exception_audit_log_actor_nonempty
        CHECK (length(actor) > 0),
    CONSTRAINT exception_audit_log_to_state_chk
        CHECK (to_state IN ('requested', 'approved', 'denied', 'active', 'expired'))
);

CREATE INDEX idx_exception_audit_log_tenant_occurred
    ON exception_audit_log (tenant_id, occurred_at DESC);

CREATE INDEX idx_exception_audit_log_tenant_exception
    ON exception_audit_log (tenant_id, exception_id, occurred_at DESC);

-- ===== Row-Level Security =====

ALTER TABLE exceptions ENABLE ROW LEVEL SECURITY;
ALTER TABLE exceptions FORCE ROW LEVEL SECURITY;
CREATE POLICY tenant_read ON exceptions
    FOR SELECT USING (current_tenant_matches(tenant_id));
CREATE POLICY tenant_write ON exceptions
    FOR INSERT WITH CHECK (current_tenant_matches(tenant_id));
CREATE POLICY tenant_update ON exceptions
    FOR UPDATE USING (current_tenant_matches(tenant_id))
    WITH CHECK (current_tenant_matches(tenant_id));
CREATE POLICY tenant_delete ON exceptions
    FOR DELETE USING (current_tenant_matches(tenant_id));

-- exception_audit_log is append-only by construction: SELECT + INSERT
-- policies only. No UPDATE/DELETE policy under FORCE ROW LEVEL SECURITY
-- means atlas_app cannot mutate audit rows. Mirrors slice 013's
-- evidence_audit_log, slice 026's sample_audit_log, slice 036's
-- artifact_access_log.
ALTER TABLE exception_audit_log ENABLE ROW LEVEL SECURITY;
ALTER TABLE exception_audit_log FORCE ROW LEVEL SECURITY;
CREATE POLICY tenant_read ON exception_audit_log
    FOR SELECT USING (current_tenant_matches(tenant_id));
CREATE POLICY tenant_write ON exception_audit_log
    FOR INSERT WITH CHECK (current_tenant_matches(tenant_id));

GRANT SELECT, INSERT, UPDATE, DELETE ON exceptions TO atlas_app;
GRANT SELECT, INSERT ON exception_audit_log TO atlas_app;
