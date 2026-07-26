-- Slice 272 / OE-398c — Playwright e2e seed for
-- `web/e2e/global-search.spec.ts`.
--
-- Builds on fixtures/walkthroughs/00-seed.sql (applied first by the
-- harness). Unlike `controls-top-bar.sql` — whose spec route-mocks
-- `/api/search` with a hand-written hits payload — this fixture seeds a
-- REAL search corpus so the spec can drive the shipped ⌘K surface
-- against the real BFF `/api/search` → upstream `GET /v1/search` path
-- with no route mock anywhere. That is the whole point of the slice:
-- prove the flow end to end (keyboard shortcut → typed query →
-- cross-entity results → navigation to a real record).
--
-- The corpus is three rows, one per tenant-scoped search type
-- (`internal/api/search/search.go`):
--
--   controls  — ILIKE over controls.title + .description
--                (active versions only: superseded_by IS NULL)
--   risks     — ILIKE over risks.title + .description
--   evidence  — ILIKE over evidence_records.evidence_kind + .control_ref
--
-- All three carry the token `AcmeVault` (a fictional Acme Industries
-- secrets manager — the fixtures' established neutral-demo naming). The
-- token is deliberately absent from every other fixture AND from the
-- bundled SCF anchor catalog, which buys the spec two things:
--
--   1. Determinism under the additive-fixture policy (fixtures/e2e/
--      README.md: fixtures are additive within a CI run, so two specs
--      asserting against the same table must use distinct row sets).
--   2. A knowable result ORDER. The endpoint sorts by relevance DESC
--      then type ASC then id ASC; a single-token query scores every row
--      1.0, so ties break on type: controls < evidence < risks. With no
--      `anchors` hit, the UI's grouped render order (anchors, controls,
--      risks, evidence — GROUP_ORDER in web/components/shell/
--      global-search.tsx) puts the CONTROL first in the flat keyboard
--      navigation order. The spec's Enter-selects-the-control and
--      ArrowDown-selects-the-risk assertions both depend on that.
--
-- All inserts are ON CONFLICT DO NOTHING for idempotency. Neutral
-- content only — no PII, no maintainer references, no vendor-prefixed
-- tokens (fixtures/e2e/README.md constraints).

\set ON_ERROR_STOP on

BEGIN;

SET LOCAL app.current_tenant = '00000000-0000-0000-0000-00000000d3a0';

-- ============================================================
-- controls hit
-- ============================================================
-- `superseded_by` stays NULL (the search's active-version predicate)
-- and `scf_anchor_id` stays NULL — an unanchored control is a 200 with
-- `anchor: null` from GET /v1/controls/{id}/coverage
-- (internal/api/ucfcoverage/control_coverage.go), so the detail page the
-- spec navigates to renders its `control-title` rather than the
-- slice-152 empty-state.
INSERT INTO controls (
    id, tenant_id, scf_id, title, description, control_family,
    implementation_type, owner_role, lifecycle_state, applicability_expr,
    bundle_id, freshness_class
)
VALUES (
    '33333333-3333-3333-3333-33333333c001',
    '00000000-0000-0000-0000-00000000d3a0',
    'IAC-10',
    'AcmeVault secret rotation — production credentials',
    'Credentials issued from AcmeVault for production workloads rotate on a 90-day cycle.',
    'Identity & Access Management',
    'automated',
    'security-engineering',
    'active',
    'env == "prod"',
    'demo-acmevault-rotation',
    'monthly'
)
ON CONFLICT DO NOTHING;

-- ============================================================
-- risks hit
-- ============================================================
-- Both scores are populated so the row never renders as "Pending
-- evaluation" anywhere the risk register is read from real data.
INSERT INTO risks (
    id, tenant_id, title, description, category, treatment,
    treatment_owner, inherent_score, residual_score
)
VALUES (
    '77777777-7777-7777-7777-77777777c001',
    '00000000-0000-0000-0000-00000000d3a0',
    'AcmeVault credential sprawl',
    'Long-lived credentials in AcmeVault outlive the workloads that requested them.',
    'confidentiality',
    'mitigate',
    'security-engineering',
    '{"likelihood": 3, "impact": 3}'::jsonb,
    '{"likelihood": 2, "impact": 3}'::jsonb
)
ON CONFLICT DO NOTHING;

-- ============================================================
-- evidence hit
-- ============================================================
-- evidence_records has no title/description; the endpoint's searchable
-- text is `evidence_kind` + `control_ref`, and the synthesized wire
-- title is "<evidence_kind> · <control_ref>". The kind carries the
-- token so the row surfaces under the same query as the other two.
INSERT INTO evidence_records (
    id, tenant_id, control_id, scope_id, observed_at, provenance, result,
    payload, hash, freshness_class, ingestion_path, source_attribution,
    control_ref, evidence_kind, schema_version
)
VALUES (
    '66666666-6666-6666-6666-66666666c001',
    '00000000-0000-0000-0000-00000000d3a0',
    '33333333-3333-3333-3333-33333333c001',
    '22222222-2222-2222-2222-222222220001',
    '2026-04-01T00:00:00Z',
    '{"source":"e2e-fixture"}'::jsonb,
    'pass',
    '{"rotated_secrets":12,"stale_secrets":0}'::jsonb,
    'sha256-global-search-fixture-01',
    'monthly',
    'push',
    '{"actor":"demo-operator@example.invalid"}'::jsonb,
    'IAC-10',
    'acmevault.secret_rotation.v1',
    '1.0.0'
)
ON CONFLICT DO NOTHING;

COMMIT;
