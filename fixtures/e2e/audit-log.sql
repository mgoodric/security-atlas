-- Slice 125 — Playwright e2e seed for `web/e2e/audit-log.spec.ts`.
--
-- Builds on fixtures/walkthroughs/00-seed.sql (applied first by the
-- harness). The audit-log spec needs:
--
--   * Several audit-log rows visible to the admin caller, across more
--     than one `kind` (so the kind-filter test can assert narrowing).
--   * Enough rows that the "Load more" / sentinel branch is reachable
--     against a 1000-row page cap — we seed three rows here because the
--     pagination assertion only needs to verify the cursor round-trip,
--     and a 1500-row seed would slow the suite. The cursor branch is
--     exercised in the slice-124 Go integration tests
--     (`internal/api/adminauditlog/unified_integration_test.go`); this
--     e2e spec asserts the wire path renders.
--
-- All inserts are ON CONFLICT DO NOTHING for idempotency across re-runs.
-- Slice 124 extended `me_audit_log.action`'s CHECK to include the
-- `audit_log_query_unified` value, which lands automatically via the
-- bootstrap migrations applied before this fixture runs.

\set ON_ERROR_STOP on

BEGIN;

SET LOCAL app.current_tenant = '00000000-0000-0000-0000-00000000d3a0';

-- ============================================================
-- kind=feature_flag: two flips for the demo tenant.
-- The actor here is a neutral test fixture identifier — NOT a real
-- bearer or vendor-prefixed token (slice 125 P0-A4 + slice 069 P0-A9).
-- ============================================================
INSERT INTO feature_flag_audit_log (
    id, tenant_id, flag_key, from_enabled, to_enabled, actor, reason, occurred_at
)
VALUES
(
    'a1010101-0000-0000-0000-000000000001',
    '00000000-0000-0000-0000-00000000d3a0',
    'demo-flag-board-pack-export',
    FALSE,
    TRUE,
    'test-fixture-actor-a',
    'slice 125 e2e fixture seed',
    now() - interval '2 days'
),
(
    'a1010101-0000-0000-0000-000000000002',
    '00000000-0000-0000-0000-00000000d3a0',
    'demo-flag-risk-aggregation-v2',
    TRUE,
    FALSE,
    'test-fixture-actor-b',
    'slice 125 e2e fixture seed',
    now() - interval '1 day'
)
ON CONFLICT DO NOTHING;

-- Ensure the referenced flags exist (audit-log rows reference them by key
-- — the schema does not require it but the demo dataset is more honest
-- when both halves are present).
INSERT INTO feature_flags (tenant_id, flag_key, enabled, description, category)
VALUES
(
    '00000000-0000-0000-0000-00000000d3a0',
    'demo-flag-board-pack-export',
    TRUE,
    'Demo feature flag for the slice-125 e2e audit-log spec (board category).',
    'board'
),
(
    '00000000-0000-0000-0000-00000000d3a0',
    'demo-flag-risk-aggregation-v2',
    FALSE,
    'Demo feature flag for the slice-125 e2e audit-log spec (risk category).',
    'risk'
)
ON CONFLICT DO NOTHING;

-- ============================================================
-- kind=evidence: one accepted push for the demo control.
-- The slice-124 aggregator includes evidence_audit_log under kind="evidence".
-- ============================================================
INSERT INTO evidence_audit_log (
    id, tenant_id, credential_id,
    decision, reason_code,
    idempotency_key, evidence_kind, record_id,
    received_at
)
VALUES (
    'b2020202-0000-0000-0000-000000000001',
    '00000000-0000-0000-0000-00000000d3a0',
    'test-fixture-credential-id',
    'accepted',
    '',
    'slice-125-fixture-key-001',
    'sast.scan_result.v1',
    NULL,
    now() - interval '6 hours'
)
ON CONFLICT DO NOTHING;

COMMIT;
