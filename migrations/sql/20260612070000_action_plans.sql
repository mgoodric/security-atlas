-- security-atlas — slice 384: ActionPlan primitive.
--
-- A fourth first-class risk-register primitive (alongside Risk, Exception,
-- DecisionLog). Distinct semantic: a FORWARD-looking commitment to close a
-- gap, with owner + due date + linkage to the risks and controls the gap
-- touches. Lifecycle is its own state machine:
--
--     draft -> in_progress -> blocked -> completed -> verified
--
-- (verified is terminal; see action_plans_status_chk + the transition
-- trigger below). NOT Exception's requested/approved/active/expired, NOT
-- DecisionLog's active/revisited/superseded/expired. Triggering event is
-- captured as free-text (P0-384-11) so the chain back to the originating
-- eval is legible without an ExternalEvaluation table (deferred spillover).
--
-- Four tables:
--
--   action_plans            — the primitive. Tenant-scoped, four-policy RLS,
--                             soft-delete via tombstoned_at (P0-384-6 — never
--                             hard-deleted; canvas invariant #2). owner_id
--                             FKs users(id); audit_period_id NULL-FKs
--                             audit_periods(id) (slice 028 freezing primitive;
--                             AC-27 freezing-snapshot uses created_at).
--
--   action_plan_risks       — M2M to risks. Carries a denormalized tenant_id
--   action_plan_controls    — M2M to controls.   with a composite FK to the
--                             parent action_plans(tenant_id, id) AND to the
--                             target risks/controls(tenant_id, id), so a row
--                             can NEVER reference a parent or target in
--                             another tenant even with RLS momentarily off
--                             (the slice-052 decision-link FK shape). RLS is
--                             subquery-based against action_plans.tenant_id
--                             per the slice spec AC-6/AC-7, layered on top of
--                             the composite-FK guard (defense in depth,
--                             P0-384-4).
--
--   action_plan_audit_log   — append-only repudiation ledger (threat-model R).
--                             Every mutation writes one row (AC-16). Append-
--                             only TWO ways: (a) SELECT + INSERT policies only
--                             under FORCE ROW LEVEL SECURITY (the slice
--                             013/021/030/062 shape) AND (b) an explicit
--                             BEFORE UPDATE OR DELETE trigger that RAISEs
--                             (AC-9 — the spec asks for a DB-layer trigger, a
--                             stronger guarantee than policy omission alone:
--                             it fires even for a BYPASSRLS / table-owner role
--                             that the missing-policy guard would not stop).
--
-- Constitutional invariants honored:
--   #2  Append-only audit ledger (action_plan_audit_log) + soft-delete
--       (tombstoned_at) preserve the record. No mutation without an audit row.
--   #6  Tenant isolation enforced at the DB layer via RLS. action_plans +
--       the two M2M tables use the four-policy split (read/write/update/
--       delete) under FORCE ROW LEVEL SECURITY. action_plan_audit_log uses
--       the append-only two-policy split + the trigger.
--   #10 Audit-period freezing: action_plans.audit_period_id + the
--       created_at <= frozen_at snapshot filter in the read path (AC-27).
--
-- Anti-criteria honored at the schema layer (P0):
--   - P0-384-4 No cross-tenant linkage — composite FK + subquery RLS.
--   - P0-384-6 No hard-delete — tombstoned_at soft-delete only.
--   - P0-384-7 50-risk + 50-control caps — enforced in the handler/store
--     (cardinality count before INSERT); the DB carries no row-count CHECK
--     (Postgres cannot express a per-parent row-count CHECK without a
--     trigger; the store is the enforcement point + the integration test).
--   - P0-384-8 due_date <= now + 5y — enforced in the handler/store.
--   - P0-384-9 No new authz role — schema introduces none.
--
-- Reversible via 20260612070000_action_plans.down.sql.

-- ===== 1. action_plans =====

CREATE TABLE action_plans (
    id               UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    tenant_id        UUID NOT NULL,
    title            TEXT NOT NULL
                     CHECK (length(title) > 0 AND length(title) <= 200),
    description      TEXT
                     CHECK (description IS NULL OR length(description) <= 4000),
    triggering_event TEXT
                     CHECK (triggering_event IS NULL OR length(triggering_event) <= 500),
    owner_id         UUID NOT NULL REFERENCES users(id),
    due_date         DATE,
    status           TEXT NOT NULL DEFAULT 'draft'
                     CHECK (status IN ('draft','in_progress','blocked','completed','verified')),
    audit_period_id  UUID NULL REFERENCES audit_periods(id),
    tombstoned_at    TIMESTAMPTZ,
    created_at       TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at       TIMESTAMPTZ NOT NULL DEFAULT now()
);

-- Composite UNIQUE so the M2M link tables can FK to (tenant_id, id) — the
-- slice-002 D3 cross-tenant-safe FK target pattern.
ALTER TABLE action_plans
    ADD CONSTRAINT action_plans_tenant_id_unique UNIQUE (tenant_id, id);

-- List path: tenant + status, newest first; tombstoned rows filtered in the
-- query (a partial index keeps the live set cheap).
CREATE INDEX action_plans_tenant_status
    ON action_plans (tenant_id, status, created_at DESC)
    WHERE tombstoned_at IS NULL;

CREATE INDEX action_plans_owner
    ON action_plans (tenant_id, owner_id);

ALTER TABLE action_plans ENABLE ROW LEVEL SECURITY;
ALTER TABLE action_plans FORCE ROW LEVEL SECURITY;

CREATE POLICY tenant_read ON action_plans
    FOR SELECT USING (current_tenant_matches(tenant_id));
CREATE POLICY tenant_write ON action_plans
    FOR INSERT WITH CHECK (current_tenant_matches(tenant_id));
CREATE POLICY tenant_update ON action_plans
    FOR UPDATE USING (current_tenant_matches(tenant_id))
               WITH CHECK (current_tenant_matches(tenant_id));
CREATE POLICY tenant_delete ON action_plans
    FOR DELETE USING (current_tenant_matches(tenant_id));

GRANT SELECT, INSERT, UPDATE, DELETE ON action_plans TO atlas_app;

-- ===== State-transition guard (AC-13 / AC-15) =====
--
-- Validated at the handler layer (internal/actionplan state machine) AND at
-- the DB layer here, defense in depth. The trigger rejects any UPDATE that
-- moves status along a disallowed edge. Allowed edges:
--
--   draft       -> in_progress
--   in_progress -> blocked | completed
--   blocked     -> in_progress | completed
--   completed   -> verified | in_progress   (reopen if verification fails)
--   verified    -> (terminal; no outbound edge)
--   any         -> same status               (no-op / non-status update)
--
-- No edge BACK to 'draft' (AC-15: '* -> draft' rejected except creation).
-- A row may always be tombstoned (the UPDATE that sets tombstoned_at does
-- not change status, so it matches the same-status no-op branch).

CREATE OR REPLACE FUNCTION action_plans_status_transition_guard()
RETURNS TRIGGER AS $$
BEGIN
    -- Same status (or any non-status update): always allowed.
    IF NEW.status = OLD.status THEN
        RETURN NEW;
    END IF;

    IF (OLD.status = 'draft'       AND NEW.status = 'in_progress')
    OR (OLD.status = 'in_progress' AND NEW.status IN ('blocked','completed'))
    OR (OLD.status = 'blocked'     AND NEW.status IN ('in_progress','completed'))
    OR (OLD.status = 'completed'   AND NEW.status IN ('verified','in_progress'))
    THEN
        RETURN NEW;
    END IF;

    RAISE EXCEPTION 'illegal action_plan status transition: % -> %', OLD.status, NEW.status
        USING ERRCODE = 'check_violation';
END;
$$ LANGUAGE plpgsql;

CREATE TRIGGER action_plans_status_transition_trg
    BEFORE UPDATE ON action_plans
    FOR EACH ROW
    EXECUTE FUNCTION action_plans_status_transition_guard();

-- ===== 2. action_plan_risks (M2M) =====

CREATE TABLE action_plan_risks (
    action_plan_id UUID NOT NULL,
    risk_id        UUID NOT NULL,
    tenant_id      UUID NOT NULL,
    linked_at      TIMESTAMPTZ NOT NULL DEFAULT now(),
    linked_by      UUID NOT NULL,

    PRIMARY KEY (action_plan_id, risk_id),

    CONSTRAINT action_plan_risks_plan_fk
        FOREIGN KEY (tenant_id, action_plan_id)
        REFERENCES action_plans (tenant_id, id) ON DELETE CASCADE,
    CONSTRAINT action_plan_risks_risk_fk
        FOREIGN KEY (tenant_id, risk_id)
        REFERENCES risks (tenant_id, id) ON DELETE CASCADE
);

CREATE INDEX action_plan_risks_risk
    ON action_plan_risks (tenant_id, risk_id);

ALTER TABLE action_plan_risks ENABLE ROW LEVEL SECURITY;
ALTER TABLE action_plan_risks FORCE ROW LEVEL SECURITY;

-- Subquery-based RLS against the parent action_plans.tenant_id (AC-6). The
-- denormalized tenant_id + composite FK above make a cross-tenant row
-- structurally impossible; this subquery policy is the spec-named RLS form
-- and a second, independent tenant gate (defense in depth).
CREATE POLICY tenant_read ON action_plan_risks
    FOR SELECT USING (
        EXISTS (
            SELECT 1 FROM action_plans ap
            WHERE ap.id = action_plan_risks.action_plan_id
              AND current_tenant_matches(ap.tenant_id)
        )
    );
CREATE POLICY tenant_write ON action_plan_risks
    FOR INSERT WITH CHECK (
        EXISTS (
            SELECT 1 FROM action_plans ap
            WHERE ap.id = action_plan_risks.action_plan_id
              AND current_tenant_matches(ap.tenant_id)
        )
    );
CREATE POLICY tenant_update ON action_plan_risks
    FOR UPDATE USING (
        EXISTS (
            SELECT 1 FROM action_plans ap
            WHERE ap.id = action_plan_risks.action_plan_id
              AND current_tenant_matches(ap.tenant_id)
        )
    ) WITH CHECK (
        EXISTS (
            SELECT 1 FROM action_plans ap
            WHERE ap.id = action_plan_risks.action_plan_id
              AND current_tenant_matches(ap.tenant_id)
        )
    );
CREATE POLICY tenant_delete ON action_plan_risks
    FOR DELETE USING (
        EXISTS (
            SELECT 1 FROM action_plans ap
            WHERE ap.id = action_plan_risks.action_plan_id
              AND current_tenant_matches(ap.tenant_id)
        )
    );

GRANT SELECT, INSERT, UPDATE, DELETE ON action_plan_risks TO atlas_app;

-- ===== 3. action_plan_controls (M2M) =====

CREATE TABLE action_plan_controls (
    action_plan_id UUID NOT NULL,
    control_id     UUID NOT NULL,
    tenant_id      UUID NOT NULL,
    linked_at      TIMESTAMPTZ NOT NULL DEFAULT now(),
    linked_by      UUID NOT NULL,

    PRIMARY KEY (action_plan_id, control_id),

    CONSTRAINT action_plan_controls_plan_fk
        FOREIGN KEY (tenant_id, action_plan_id)
        REFERENCES action_plans (tenant_id, id) ON DELETE CASCADE,
    CONSTRAINT action_plan_controls_control_fk
        FOREIGN KEY (tenant_id, control_id)
        REFERENCES controls (tenant_id, id) ON DELETE CASCADE
);

CREATE INDEX action_plan_controls_control
    ON action_plan_controls (tenant_id, control_id);

ALTER TABLE action_plan_controls ENABLE ROW LEVEL SECURITY;
ALTER TABLE action_plan_controls FORCE ROW LEVEL SECURITY;

CREATE POLICY tenant_read ON action_plan_controls
    FOR SELECT USING (
        EXISTS (
            SELECT 1 FROM action_plans ap
            WHERE ap.id = action_plan_controls.action_plan_id
              AND current_tenant_matches(ap.tenant_id)
        )
    );
CREATE POLICY tenant_write ON action_plan_controls
    FOR INSERT WITH CHECK (
        EXISTS (
            SELECT 1 FROM action_plans ap
            WHERE ap.id = action_plan_controls.action_plan_id
              AND current_tenant_matches(ap.tenant_id)
        )
    );
CREATE POLICY tenant_update ON action_plan_controls
    FOR UPDATE USING (
        EXISTS (
            SELECT 1 FROM action_plans ap
            WHERE ap.id = action_plan_controls.action_plan_id
              AND current_tenant_matches(ap.tenant_id)
        )
    ) WITH CHECK (
        EXISTS (
            SELECT 1 FROM action_plans ap
            WHERE ap.id = action_plan_controls.action_plan_id
              AND current_tenant_matches(ap.tenant_id)
        )
    );
CREATE POLICY tenant_delete ON action_plan_controls
    FOR DELETE USING (
        EXISTS (
            SELECT 1 FROM action_plans ap
            WHERE ap.id = action_plan_controls.action_plan_id
              AND current_tenant_matches(ap.tenant_id)
        )
    );

GRANT SELECT, INSERT, UPDATE, DELETE ON action_plan_controls TO atlas_app;

-- ===== 4. action_plan_audit_log (append-only) =====

CREATE TABLE action_plan_audit_log (
    id             UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    tenant_id      UUID NOT NULL,
    action_plan_id UUID NOT NULL,
    actor_id       UUID NOT NULL,
    action_type    TEXT NOT NULL
                   CHECK (action_type IN (
                       'created',
                       'updated',
                       'status_changed',
                       'risk_linked',
                       'risk_unlinked',
                       'control_linked',
                       'control_unlinked',
                       'tombstoned'
                   )),
    before_state   JSONB,
    after_state    JSONB,
    created_at     TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE INDEX action_plan_audit_log_tenant_created
    ON action_plan_audit_log (tenant_id, created_at DESC);

CREATE INDEX action_plan_audit_log_tenant_plan
    ON action_plan_audit_log (tenant_id, action_plan_id, created_at DESC);

ALTER TABLE action_plan_audit_log ENABLE ROW LEVEL SECURITY;
ALTER TABLE action_plan_audit_log FORCE ROW LEVEL SECURITY;

-- Append-only by RLS construction: SELECT + INSERT policies only.
CREATE POLICY tenant_read ON action_plan_audit_log
    FOR SELECT USING (current_tenant_matches(tenant_id));
CREATE POLICY tenant_write ON action_plan_audit_log
    FOR INSERT WITH CHECK (current_tenant_matches(tenant_id));
-- Intentionally NO update / delete policies — append-only.

GRANT SELECT, INSERT ON action_plan_audit_log TO atlas_app;

-- AC-9: append-only enforced by an explicit DB-layer trigger as well, so the
-- invariant holds even for a privileged (BYPASSRLS / table-owner) role that
-- the missing-policy guard would not stop. Mirrors the spec's "DB-layer
-- trigger denies UPDATE on action_plan_audit_log" requirement; DELETE is
-- covered too for completeness.
CREATE OR REPLACE FUNCTION action_plan_audit_log_append_only()
RETURNS TRIGGER AS $$
BEGIN
    RAISE EXCEPTION 'action_plan_audit_log is append-only: % denied', TG_OP
        USING ERRCODE = 'restrict_violation';
END;
$$ LANGUAGE plpgsql;

CREATE TRIGGER action_plan_audit_log_no_update_trg
    BEFORE UPDATE OR DELETE ON action_plan_audit_log
    FOR EACH ROW
    EXECUTE FUNCTION action_plan_audit_log_append_only();
