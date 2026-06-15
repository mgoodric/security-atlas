-- Slice 743 — Playwright e2e seed for `web/e2e/controls-list.spec.ts`.
--
-- Builds on fixtures/walkthroughs/00-seed.sql (applied first by the
-- harness). Wires the long-quarantined controls-list spec (slice 098 +
-- 224 + 226 + 227 + 225 + 448 + 468 assertion bodies) to a real
-- bring-up so its assertions can be observed passing in CI's
-- `Frontend · Playwright e2e` job.
--
-- ==================================================================
-- WHY THIS FIXTURE SEEDS ITS OWN SCF CATALOG ROWS
-- ==================================================================
-- The controls list view at `web/app/(authed)/controls/page.tsx`
-- renders `GET /v1/anchors?include=state` — i.e. SCF *anchors*
-- (`scf_anchors`), NOT the tenant `controls` table directly. The list
-- query (ListSCFAnchorsLatestWithState) selects anchors in the CURRENT
-- SCF framework_version:
--
--     WHERE f.slug = 'scf' AND fv.status = 'current' AND f.tenant_id IS NULL
--     ORDER BY a.scf_id
--
-- The CI Playwright job (.github/workflows/ci.yml `frontend-playwright`)
-- applies migrations + boots atlas, but runs NO SCF catalog import step,
-- and atlas boot seeds only the metrics catalog — so `scf_anchors` is
-- EMPTY in the CI e2e database. Without anchor rows the list renders
-- zero `list-table-row`s and every slice-098/448/468 assertion that
-- counts or clicks a row fails. This fixture therefore seeds a small,
-- self-contained current-SCF catalog (a global `scf` framework + a
-- `current` version + 3 anchors with varied `family`).
--
-- ==================================================================
-- WHY EACH ANCHOR ALSO GETS A MATCHING `controls` ROW (same UUID)
-- ==================================================================
-- The slice-468 bulk-assign-owner round-trip (the load-bearing AC-2)
-- sends the selected ROW's id — which is the *anchor* id (`row.anchor.id`)
-- — to `/v1/controls:bulk-assign-owner`. The backend's per-item check
-- (ControlExistsInTenant, internal/api/controls/owner_assign.go) runs:
--
--     SELECT EXISTS(SELECT 1 FROM controls
--                   WHERE tenant_id = $1 AND id = $2 AND superseded_by IS NULL)
--
-- i.e. it looks the id up in the `controls` table. So for the assign to
-- return `assigned N` (not 404), a `controls` row whose `id` EQUALS the
-- selected anchor's id must exist in the demo tenant. We mirror each
-- seeded anchor id into a `controls` row of the same id (unique
-- `bundle_id`, `superseded_by IS NULL`).
--
-- ==================================================================
-- WHY THE scf_id VALUES START WITH A DIGIT
-- ==================================================================
-- The spec clicks `getByTestId("controls-row-select").first()`. `.first()`
-- is the first row by `ORDER BY a.scf_id`. Real SCF ids are letter-
-- prefixed (`AAA-01`, `CRY-05`, `IAC-06`, …). Our scf_ids begin with a
-- DIGIT (`0E2E743-1` …) so they sort lexically BEFORE any real SCF id.
-- This guarantees `.first()` lands on one of OUR anchors — which is
-- backed by a `controls` row of the same id — in BOTH the empty-catalog
-- CI database AND a compose bundle that has imported the real SCF
-- catalog. (ASCII: digits 0x30 < uppercase 0x41.)
--
-- ==================================================================
-- DETERMINISM RESET (saved-views + owner-assignments)
-- ==================================================================
-- The save/load/delete/duplicate-name + assign assertions mutate
-- per-(tenant,user) state. The shared docker-compose Postgres persists
-- across CI runs, so a prior run's "Weekly triage" / "My view" rows or a
-- prior bulk-assign would make a re-run non-deterministic. We DELETE the
-- demo (tenant, user)'s `saved_views` and the demo tenant's
-- `control_owner_assignments` for our seeded controls at fixture start
-- so every run begins clean. (The saved-views user scope is the verified
-- credential subject = DEMO_USER_ID; the JWT minted by
-- web/e2e/global-setup.ts carries tenant=DEMO_TENANT_ID, sub=DEMO_USER_ID.)
--
-- ==================================================================
-- Hard constraints (P0-743-1 / P0-743-2):
--   - NO vendor-prefixed token strings, no real-looking secrets.
--   - All IDs deterministic so re-runs are byte-stable.
--   - Every INSERT uses ON CONFLICT DO NOTHING for idempotency (run
--     twice → second run is a no-op).
--   - Writes target ONLY the demo (tenant, user) this fixture owns.

\set ON_ERROR_STOP on

BEGIN;

SET LOCAL app.current_tenant = '00000000-0000-0000-0000-00000000d3a0';

-- ============================================================
-- Global SCF framework + current version (tenant_id IS NULL)
-- ============================================================
-- The list query keys on f.slug='scf' AND fv.status='current' AND
-- f.tenant_id IS NULL. In a database that already imported the real SCF
-- catalog (a compose bundle) these rows already exist under the
-- importer's deterministic ids; ON CONFLICT DO NOTHING leaves them
-- untouched and our anchors below simply join the existing current
-- version we reference by id. In the empty-catalog CI database these
-- rows establish the current SCF version our anchors live in.
--
-- The framework id is unique-constrained on (tenant_id, slug). Because
-- Postgres treats NULL as DISTINCT in UNIQUE, a second global 'scf' row
-- would NOT collide on the (NULL, 'scf') tuple — so we pin a fixed PK id
-- and rely on ON CONFLICT (id) for idempotency, and on the digit-prefix
-- ordering rule above to stay safe if a real catalog also provides one.
INSERT INTO frameworks (id, tenant_id, name, slug, issuer, description)
VALUES (
    'e2e74300-0000-0000-0000-0000000f0001',
    NULL,
    'Secure Controls Framework',
    'scf',
    'Secure Controls Framework Council',
    'Slice 743 e2e synthetic SCF spine — current version for the controls-list spec.'
)
ON CONFLICT (id) DO NOTHING;

INSERT INTO framework_versions (
    id, tenant_id, framework_id, version, effective_from, status, oscal_catalog_uri
)
VALUES (
    'e2e74300-0000-0000-0000-0000000f0002',
    NULL,
    'e2e74300-0000-0000-0000-0000000f0001',
    '2026.1-e2e',
    '2026-01-01',
    'current',
    'urn:scf:e2e:2026.1'
)
ON CONFLICT (id) DO NOTHING;

-- ============================================================
-- users — the bulk-assign "assign to me" target + saved-views owner
-- ============================================================
-- DEMO_USER_ID (44444444-…-0001) is the JWT subject the e2e harness
-- mints (web/e2e/global-setup.ts) and the value `me.user_id` resolves
-- to in the page's bulkAssignOwner(controlIds, me.user_id) call. The
-- backend's UserExistsInTenant gate requires a real, status='active'
-- user in the tenant, else the assign returns 422. status='active'.
INSERT INTO users (
    id, tenant_id, email, display_name, status, idp_issuer, idp_subject
)
VALUES (
    '44444444-4444-4444-4444-444444440001',
    '00000000-0000-0000-0000-00000000d3a0',
    'controls-list-e2e-user@example.invalid',
    'Controls-list E2E Operator',
    'active',
    '',
    ''
)
ON CONFLICT DO NOTHING;

-- ============================================================
-- 3 SCF anchors (varied family) + 3 matching controls (same id)
-- ============================================================
-- Three distinct families so the Family filter pill has >=2 non-ALL
-- options and selectOption({ index: 1 }) actually narrows the row set.
-- Anchor ids are deterministic; each control reuses the anchor's id so
-- bulk-assign-owner on any selected row resolves a real control.
--
-- scf_id values are digit-prefixed (sort before all real SCF ids).

-- ---- anchor 1 (family: Cryptography) ----
INSERT INTO scf_anchors (id, framework_version_id, scf_id, family, title, description)
VALUES (
    'e2e74300-0000-0000-0000-00000000a001',
    'e2e74300-0000-0000-0000-0000000f0002',
    '0E2E743-1',
    'Cryptography',
    'Encryption of data at rest (e2e fixture anchor 1)',
    'Synthetic SCF anchor seeded by slice 743 for the controls-list e2e spec.'
)
ON CONFLICT (id) DO NOTHING;

INSERT INTO controls (
    id, tenant_id, scf_id, scf_anchor_id, title, description, control_family,
    implementation_type, owner_role, lifecycle_state, applicability_expr,
    bundle_id, freshness_class
)
VALUES (
    'e2e74300-0000-0000-0000-00000000a001',
    '00000000-0000-0000-0000-00000000d3a0',
    '0E2E743-1',
    'e2e74300-0000-0000-0000-00000000a001',
    'Encryption at rest (e2e fixture control 1)',
    'Tenant control instantiated for e2e anchor 1 so bulk-assign-owner resolves.',
    'Cryptography',
    'automated',
    'security-engineering',
    'active',
    'true',
    'e2e-743-control-1',
    'monthly'
)
ON CONFLICT (id) DO NOTHING;

-- ---- anchor 2 (family: Identity & Access Management) ----
INSERT INTO scf_anchors (id, framework_version_id, scf_id, family, title, description)
VALUES (
    'e2e74300-0000-0000-0000-00000000a002',
    'e2e74300-0000-0000-0000-0000000f0002',
    '0E2E743-2',
    'Identity & Access Management',
    'Least-privilege access reviews (e2e fixture anchor 2)',
    'Synthetic SCF anchor seeded by slice 743 for the controls-list e2e spec.'
)
ON CONFLICT (id) DO NOTHING;

INSERT INTO controls (
    id, tenant_id, scf_id, scf_anchor_id, title, description, control_family,
    implementation_type, owner_role, lifecycle_state, applicability_expr,
    bundle_id, freshness_class
)
VALUES (
    'e2e74300-0000-0000-0000-00000000a002',
    '00000000-0000-0000-0000-00000000d3a0',
    '0E2E743-2',
    'e2e74300-0000-0000-0000-00000000a002',
    'Access reviews (e2e fixture control 2)',
    'Tenant control instantiated for e2e anchor 2 so bulk-assign-owner resolves.',
    'Identity & Access Management',
    'manual_periodic',
    'security-engineering',
    'active',
    'true',
    'e2e-743-control-2',
    'quarterly'
)
ON CONFLICT (id) DO NOTHING;

-- ---- anchor 3 (family: Logging & Monitoring) ----
INSERT INTO scf_anchors (id, framework_version_id, scf_id, family, title, description)
VALUES (
    'e2e74300-0000-0000-0000-00000000a003',
    'e2e74300-0000-0000-0000-0000000f0002',
    '0E2E743-3',
    'Logging & Monitoring',
    'Centralized audit logging (e2e fixture anchor 3)',
    'Synthetic SCF anchor seeded by slice 743 for the controls-list e2e spec.'
)
ON CONFLICT (id) DO NOTHING;

INSERT INTO controls (
    id, tenant_id, scf_id, scf_anchor_id, title, description, control_family,
    implementation_type, owner_role, lifecycle_state, applicability_expr,
    bundle_id, freshness_class
)
VALUES (
    'e2e74300-0000-0000-0000-00000000a003',
    '00000000-0000-0000-0000-00000000d3a0',
    '0E2E743-3',
    'e2e74300-0000-0000-0000-00000000a003',
    'Audit logging (e2e fixture control 3)',
    'Tenant control instantiated for e2e anchor 3 so bulk-assign-owner resolves.',
    'Logging & Monitoring',
    'automated',
    'security-engineering',
    'active',
    'true',
    'e2e-743-control-3',
    'monthly'
)
ON CONFLICT (id) DO NOTHING;

-- ============================================================
-- Determinism reset — saved_views + control_owner_assignments
-- ============================================================
-- NOT ON CONFLICT DO NOTHING: these DELETEs are the reset that makes the
-- save/load/delete/duplicate-name + assign assertions deterministic
-- across reruns on the shared docker-compose Postgres. They are scoped
-- strictly to the demo (tenant, user) / the demo tenant's seeded
-- controls — they never touch any other tenant's rows (P0-743-2). Both
-- DELETEs are inherently idempotent (a second run deletes zero rows).
DELETE FROM saved_views
 WHERE tenant_id = '00000000-0000-0000-0000-00000000d3a0'
   AND user_id  = '44444444-4444-4444-4444-444444440001'
   AND surface  = 'controls';

DELETE FROM control_owner_assignments
 WHERE tenant_id = '00000000-0000-0000-0000-00000000d3a0'
   AND control_id IN (
       'e2e74300-0000-0000-0000-00000000a001',
       'e2e74300-0000-0000-0000-00000000a002',
       'e2e74300-0000-0000-0000-00000000a003'
   );

COMMIT;
