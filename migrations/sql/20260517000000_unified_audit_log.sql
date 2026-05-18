-- security-atlas — slice 124: unified audit-log aggregation API.
--
-- Two schema changes in support of the new `GET /v1/admin/audit-log/unified`
-- endpoint that UNION ALLs across the nine per-domain audit-log tables:
--
--   1. Composite index `(tenant_id, created_at DESC)` on `aggregation_rule_audit_log`
--      — the only one of the nine tables missing the index after audit.
--      The other eight already ship the equivalent `(tenant_id, occurred_at DESC)`
--      (or `(tenant_id, received_at DESC)` for `evidence_audit_log`) from
--      their owning slice's migration:
--        - decision_audit_log         (slice 035 / _018) idx_decision_audit_log_tenant_occurred
--        - evidence_audit_log         (slice 013 / _004) idx_evidence_audit_log_tenant_received
--        - exception_audit_log        (slice 021 / _011) idx_exception_audit_log_tenant_occurred
--        - sample_audit_log           (slice 026 / _010) idx_sample_audit_log_tenant_occurred
--        - audit_period_audit_log     (slice 028 / _020) idx_audit_period_audit_log_tenant_occurred
--        - aggregation_rule_audit_log (slice 053 / _026) <MISSING — added here>
--        - feature_flag_audit_log     (slice 059 / _019) idx_feature_flag_audit_log_tenant_occurred
--        - me_audit_log               (slice 108 / _016003) me_audit_log_tenant_occurred
--        - walkthrough_audit_log      (slice 027 / _025) idx_walkthrough_audit_log_tenant_occurred
--
--   2. Extend `me_audit_log.action` CHECK constraint to permit the new
--      `'audit_log_query_unified'` value. AC-10 of slice 124 records every
--      successful unified-log query as a `me_audit_log` row; the existing
--      CHECK only allowed `('profile.update', 'preferences.update', 'session.revoke')`
--      (slice 108 / migration _016003) so this extension is load-bearing.
--      `audit_log_query_unified` is intentionally distinct from the slice-108
--      mutation-actions: it is a READ event, not a profile/preference/session
--      mutation, but the meta-audit reuses the same append-only table to
--      keep the wire shape uniform.
--
-- Constitutional invariants honored:
--
--   #6 Tenant isolation via PostgreSQL RLS. The new index does not alter RLS;
--      the CHECK extension does not alter RLS. The aggregator package
--      (`internal/audit/unifiedlog/`) executes its UNION ALL under
--      `tenancy.ApplyTenant` as `atlas_app`; RLS on each base table fires
--      automatically.
--
--   Append-only ledger (canvas §4.3). The aggregator is read-only by
--   construction (P0-A1 / P0-A8). The CHECK extension permits a new READ-event
--   action value in `me_audit_log`, which itself remains append-only via its
--   SELECT-+-INSERT-only RLS policy split (slice 108).
--
-- Idempotency: CREATE INDEX IF NOT EXISTS + ALTER TABLE ... DROP/ADD CONSTRAINT
-- both succeed on re-apply against an already-migrated database. Reversible
-- via 20260517000000_unified_audit_log.down.sql.

-- ===== 1. aggregation_rule_audit_log composite index =====
--
-- The slice-053 migration only added `(tenant_id, rule_id, created_at DESC)`.
-- For the unified-log range scan, the planner wants `(tenant_id, created_at DESC)`
-- without the rule_id middle column so the scan can short-circuit on the
-- occurred_at-window predicate without an in-memory rule_id sort.

CREATE INDEX IF NOT EXISTS idx_aggregation_rule_audit_log_tenant_created
    ON aggregation_rule_audit_log (tenant_id, created_at DESC);

-- ===== 2. me_audit_log.action CHECK extension =====
--
-- Drop-and-recreate the CHECK so the constraint name remains stable for any
-- future migration that needs to extend it again. The new
-- `audit_log_query_unified` value is the meta-audit action written by the
-- slice-124 aggregator on every successful query.

ALTER TABLE me_audit_log
    DROP CONSTRAINT IF EXISTS me_audit_log_action_check;

ALTER TABLE me_audit_log
    ADD CONSTRAINT me_audit_log_action_check
    CHECK (action IN (
        'profile.update',
        'preferences.update',
        'session.revoke',
        'audit_log_query_unified'
    ));
