-- security-atlas — Decision Log audit log + audit-narrative opt-out (slice 055).
--
-- Implements docs/issues/055-decision-log.md migration `_030`.
--
-- ----------------------------------------------------------------------------
-- Slice 052 (migration `_014`) shipped the `decisions` table, the four M:N
-- link tables (`decision_risks`, `decision_controls`, `decision_exceptions`,
-- `decision_scope_predicates`), and the `decision_status` enum. It did NOT
-- ship an audit log for Decision Log mutations. Slice 055 adds two things:
--
--   1. `decisions_audit` — an append-only mutation log. Every PATCH,
--      supersede, link add/remove, denied cross-tenant link attempt, and
--      overdue-notification emission writes one row. The `overdue_notified`
--      action row is also the authoritative "already notified" marker the
--      daily overdue job checks before emitting (anti-criterion P0: no
--      repeated overdue spam).
--
--      NOTE on naming: the pre-existing `decision_audit_log` table (slice
--      035, migration `_018`) is the OPA *authorization* allow/deny log — a
--      different concept entirely. `decisions_audit` (this slice, the issue's
--      chosen name) is the Decision Log's domain-mutation log. The two names
--      are deliberately distinct.
--
--   2. `decisions.audit_narrative_opt_out` — a per-decision boolean. When
--      `true`, the decision is excluded from OSCAL SSP narrative emission
--      (slice 030). Per-decision rather than a tenant-config table: opt-out
--      is a per-record judgement and a tenant-config table is not warranted
--      by v1. Default `false` so every existing slice-052 decision row keeps
--      its current (included) behaviour.
--
-- Constitutional invariants honored:
--
--   #6  Tenant isolation at the database layer. `decisions_audit` gets
--       FORCE ROW LEVEL SECURITY with SELECT + INSERT policies ONLY — the
--       explicit absence of UPDATE/DELETE policies under FORCE makes the
--       table append-only by construction. Same pattern as slice 013's
--       `evidence_audit_log`, slice 021's `exception_audit_log`, slice 026's
--       `sample_audit_log`, slice 036's `artifact_access_log`.
--   #8  OSCAL is the wire format, not the daily model. The opt-out flag
--       gates *narrative emission* (a slice-030 export concern); it does not
--       change the daily `decisions` data model's meaning.
--   #9  Manual evidence is first-class. The Decision Log is the explicit
--       operational counterpart to manual evidence; its mutation trail is
--       recorded with the same audit weight as every other primitive.
--
-- Anti-criteria honored at the schema layer (P0):
--   - No silent mutation: there is no path that mutates a `decisions` row
--     without a corresponding `decisions_audit` row (application-layer
--     invariant, exercised by integration test).
--   - No overdue spam: the daily job writes one `overdue_notified` row per
--     overdue decision and checks for it before re-emitting.
--   - Cross-tenant linkage denied: a `cross_tenant_link_denied` action is
--     recorded when a link attempt references a foreign-tenant entity.
--
-- Idempotency / reversibility:
--
--   The ADD COLUMN on `decisions` carries NOT NULL DEFAULT false so the
--   ALTER succeeds against a table that already holds slice-052 rows (each
--   existing row is back-filled with the default). Fully reversible via
--   20260511000030_decisions_audit.down.sql for a byte-clean
--   up -> down -> up round-trip.
-- ----------------------------------------------------------------------------

-- ===== decisions.audit_narrative_opt_out =====

ALTER TABLE decisions
    ADD COLUMN audit_narrative_opt_out BOOLEAN NOT NULL DEFAULT false;

-- ===== decisions_audit =====
--
-- Append-only mutation log. No FK to `decisions` because the audit trail
-- must survive a future hard-delete or admin cleanup of a decision row —
-- the `decision_id` (the row UUID, not the human-readable identifier) is
-- preserved verbatim. Tenant-scoped via RLS plus the composite
-- (tenant_id, decision_id) lookup pattern. Same shape as
-- `exception_audit_log`.

CREATE TABLE decisions_audit (
    id            UUID PRIMARY KEY,
    tenant_id     UUID NOT NULL,
    -- The `decisions.id` UUID this audit row describes. NOT an FK (see
    -- above). For a `cross_tenant_link_denied` action this is the
    -- (in-tenant) decision the caller tried to link FROM.
    decision_id   UUID NOT NULL,
    action        TEXT NOT NULL,
    -- The credential id / decision_maker / system actor that drove the
    -- change. Mirrors `exception_audit_log.actor`.
    actor         TEXT NOT NULL,
    -- Free-form detail. For `updated`: a compact human-readable diff. For
    -- `link_added`/`link_removed`: the link kind + target id. For
    -- `superseded`: the replacement decision id. For
    -- `cross_tenant_link_denied`: the link kind + the foreign target id.
    -- For `overdue_notified`: the recipient + revisit_by.
    detail        TEXT NOT NULL DEFAULT '',
    occurred_at   TIMESTAMPTZ NOT NULL DEFAULT now(),

    CONSTRAINT decisions_audit_action_chk
        CHECK (action IN (
            'created',
            'updated',
            'superseded',
            'link_added',
            'link_removed',
            'cross_tenant_link_denied',
            'overdue_notified'
        )),
    CONSTRAINT decisions_audit_actor_nonempty
        CHECK (length(actor) > 0)
);

CREATE INDEX idx_decisions_audit_tenant_occurred
    ON decisions_audit (tenant_id, occurred_at DESC);

CREATE INDEX idx_decisions_audit_tenant_decision
    ON decisions_audit (tenant_id, decision_id, occurred_at DESC);

-- Partial index serving the overdue-job dedup probe: "has this decision
-- already had an overdue_notified row written?". Keeps the
-- check-before-emit lookup index-only.
CREATE INDEX idx_decisions_audit_overdue_notified
    ON decisions_audit (tenant_id, decision_id)
    WHERE action = 'overdue_notified';

-- ===== Row-Level Security =====
--
-- decisions_audit is append-only by construction: SELECT + INSERT policies
-- only. No UPDATE/DELETE policy under FORCE ROW LEVEL SECURITY means
-- atlas_app cannot mutate audit rows. Mirrors slice 013's
-- evidence_audit_log, slice 021's exception_audit_log, slice 026's
-- sample_audit_log, slice 036's artifact_access_log.

ALTER TABLE decisions_audit ENABLE ROW LEVEL SECURITY;
ALTER TABLE decisions_audit FORCE ROW LEVEL SECURITY;
CREATE POLICY tenant_read ON decisions_audit
    FOR SELECT
    USING (current_tenant_matches(tenant_id));
CREATE POLICY tenant_write ON decisions_audit
    FOR INSERT
    WITH CHECK (current_tenant_matches(tenant_id));

-- ===== Grants =====
--
-- atlas_app gets SELECT + INSERT on the append-only audit table.
-- atlas_migrate (BYPASSRLS) retains DDL access via role membership.
GRANT SELECT, INSERT ON decisions_audit TO atlas_app;
