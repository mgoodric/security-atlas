-- security-atlas — slice 439: evidence-staleness rollup idempotency ledger.
--
-- Slice 439 ships the PRODUCER of `evidence.staleness` notifications: a
-- scheduled rollup job (reusing the slice-076 metrics/scheduler cadence
-- pattern) that reads the slice-016 freshness read model per tenant and writes
-- in-app notifications (the slice-029 `notifications` store) when evidence
-- crosses the `eval.FreshnessMaxAge` threshold (a per-control "stale" alert)
-- plus a weekly digest summarizing what is stale / approaching-stale. The
-- already-merged delivery channels (slice 445 email, slice 543 Slack/webhook,
-- slice 582/583 digest scheduler) CONSUME the `evidence.staleness` rows this
-- producer writes — this slice closes the producer gap.
--
-- This table is the IDEMPOTENCY ledger (threat-model T — Tampering; AC-5 /
-- AC-12). The rollup is re-run every recompute tick; without a dedup ledger it
-- would re-fire the same threshold-crossing alert and re-deliver the weekly
-- digest on every tick (notification spam). One row claims one logical
-- delivery; the UNIQUE key makes a re-run a no-op INSERT.
--
-- Dedup key shape (kind-discriminated, all per (tenant, recipient)):
--   - staleness_alert : dedup_key = 'alert:' || control_id || ':' || band ||
--                       ':' || period_key  (one alert per control per band per
--                       recompute period — a control that stays stale across
--                       ticks within the same period is NOT re-alerted).
--   - staleness_digest: dedup_key = 'digest:' || period_key  (one weekly
--                       digest per recipient per ISO-week period).
-- `period_key` is computed in Go (UTC recompute-window bucket for alerts; the
-- ISO-week 'YYYY-Www' string for the digest) and passed explicitly so pgx
-- never infers a bare-placeholder type (SQLSTATE 42P08).
--
-- This is NOT the notification itself — the notification row lives in the
-- slice-029 `notifications` table. This ledger only records "we already
-- delivered THIS logical event to THIS recipient" so the next tick skips it.
-- It carries no confidential evidence payload (only the opaque dedup_key +
-- the kind), so it is not a cross-tenant disclosure surface beyond the
-- tenant_id itself — which RLS already isolates.
--
-- CONSTITUTIONAL INVARIANTS:
--   #6  Tenant isolation enforced at the DB layer via the slice-014
--       four-policy split (tenant_read FOR SELECT, tenant_write FOR INSERT
--       WITH CHECK, tenant_update FOR UPDATE, tenant_delete FOR DELETE) under
--       FORCE ROW LEVEL SECURITY with the slice-002 `current_tenant_matches`
--       helper. The cross-tenant rollup writes EACH tenant's ledger rows only
--       under THAT tenant's GUC; the WITH CHECK denies a mismatched tenant_id.
--   #2  Ingestion/evaluation separation — unchanged. This ledger is written by
--       the rollup CONSUMER of the freshness read model; it never touches
--       `evidence_records`.
--
-- Anti-criteria honored at the schema layer (P0):
--   - P0-439-2 (cross-tenant leak): tenant_id NOT NULL + 4-policy FORCE RLS;
--     the dedup_key carries no cross-tenant identifiers.
--   - The recompute interval is named honestly in the rollup copy + UI, not in
--     this ledger (P0-439-1 is a copy concern, not a schema concern).
--
-- IDEMPOTENCY / REVERSIBILITY: pure additive CREATE TABLE; reversed by
-- 20260609000000_staleness_rollup_log.down.sql (DROP TABLE).

CREATE TABLE staleness_rollup_log (
    id                 UUID PRIMARY KEY,
    tenant_id          UUID NOT NULL,
    recipient_user_id  TEXT NOT NULL,
    kind               TEXT NOT NULL,
    dedup_key          TEXT NOT NULL,
    notification_id    UUID NULL,
    delivered_at       TIMESTAMPTZ NOT NULL DEFAULT now(),

    CONSTRAINT staleness_rollup_log_recipient_nonempty
        CHECK (length(recipient_user_id) > 0),
    CONSTRAINT staleness_rollup_log_kind_check
        CHECK (kind IN ('staleness_alert', 'staleness_digest')),
    CONSTRAINT staleness_rollup_log_dedup_nonempty
        CHECK (length(dedup_key) > 0),
    -- One delivery per (tenant, recipient, dedup_key). A re-run's INSERT hits
    -- this key and is skipped (ON CONFLICT DO NOTHING) — the idempotency
    -- guarantee for both the per-control alert and the weekly digest.
    CONSTRAINT staleness_rollup_log_dedup_unique
        UNIQUE (tenant_id, recipient_user_id, dedup_key)
);

-- Hot path: the dedup pre-check / claim is keyed by the unique constraint
-- above (which Postgres backs with an index). A secondary index on
-- (tenant_id, recipient_user_id, delivered_at) supports any future
-- "what was I told this week" read without a full scan.
CREATE INDEX idx_staleness_rollup_log_recipient_recent
    ON staleness_rollup_log (tenant_id, recipient_user_id, delivered_at DESC);

ALTER TABLE staleness_rollup_log ENABLE ROW LEVEL SECURITY;
ALTER TABLE staleness_rollup_log FORCE ROW LEVEL SECURITY;

CREATE POLICY tenant_read ON staleness_rollup_log
    FOR SELECT USING (current_tenant_matches(tenant_id));
CREATE POLICY tenant_write ON staleness_rollup_log
    FOR INSERT WITH CHECK (current_tenant_matches(tenant_id));
CREATE POLICY tenant_update ON staleness_rollup_log
    FOR UPDATE USING (current_tenant_matches(tenant_id))
    WITH CHECK (current_tenant_matches(tenant_id));
CREATE POLICY tenant_delete ON staleness_rollup_log
    FOR DELETE USING (current_tenant_matches(tenant_id));

GRANT SELECT, INSERT, UPDATE, DELETE ON staleness_rollup_log TO atlas_app;
