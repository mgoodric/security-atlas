-- Reverse slice 439 — drop the evidence-staleness rollup idempotency ledger.
-- Pure additive table in the up migration; the down is a clean DROP. The
-- slice-029 notifications it deduped are independent rows in the notifications
-- table and are unaffected.

DROP TABLE IF EXISTS staleness_rollup_log;
