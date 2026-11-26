- **admin:** slice 278 — demo-seed UI button (edge-only via
  `ATLAS_ENABLE_DEMO_SEED`). Wraps slice 205's `internal/demoseed/`
  package with a one-click admin surface at `/admin/demo`:
  "Reseed demo dataset" + "Tear down demo tenant" buttons, each
  gated by a confirmation dialog. The backend
  (`internal/api/admindemo/`) exposes three routes
  (`GET /v1/admin/demo/status`, `POST /v1/admin/demo/seed`,
  `POST /v1/admin/demo/teardown`) enforcing a triple gate:
  (1) `ATLAS_ENABLE_DEMO_SEED=true` env-var precondition (unset →
  503 + no audit row), (2) admin role admit via slice 035 OPA
  (super_admin alone denies — admin is the ONLY gate per P0-278-7),
  (3) per-invocation `me_audit_log` + `super_admin_audit_log` rows
  with `action='demo_seed'` or `'demo_teardown'` written BEFORE
  the seeder runs (fail closed — audit-write failure aborts).
  Per-IP token bucket rate-limits Seed + Teardown to 1 invocation
  per 60 seconds; Status is unlimited. The frontend page is a
  Next.js server component that fetches `/api/admin/demo/status`
  and renders either a "Demo tools are not enabled" banner
  (enabled=false) or the two action cards (enabled=true). Sidebar
  breadcrumb in `admin/layout.tsx` conditionally surfaces the "Demo"
  link only when the env-var gate is satisfied (composes with
  slice 186 role-conditional nav).

  The slice ships a CHECK constraint migration
  (`migrations/sql/20260525000000_demo_seed_button_meta_audit.sql`)
  that extends both `me_audit_log.action` and
  `super_admin_audit_log.action` to admit two new action values
  (`demo_seed`, `demo_teardown`) AND RESTORES slice 205's
  `demo_seed_apply` + `demo_seed_teardown` values that slice 269
  inadvertently dropped (D5 in the decisions log). The HTTP
  invocation rows are distinct from the seeder-run rows — two-row
  forensic separation distinguishes who-clicked-the-button from
  what-the-seeder-did; the seeder's `Apply` / `Teardown` continues
  to write its own rows unchanged.

  Test coverage: unit tests for every gate (admin / env / rate
  limit / status), Postgres-bound integration test covering the
  happy path + 403 + 503 + 429 cases, OPA matrix test pinning
  admin-only admit (auditor / grc_engineer / control_owner /
  viewer / no-role all deny seed + teardown writes; super_admin
  alone denies per P0-278-7), vitest for the BFF status route
  (200 passthrough + 403 + 5xx propagation), Playwright spec
  covering the banner-when-disabled / buttons-when-enabled /
  dialog-flow paths via `page.route` mocks. Decisions log:
  `docs/audit-log/278-demo-seed-button-decisions.md`
  (D1 env-var reuse · D2 hard-coded slug + scale · D3 60s rate
  limit · D4 Dialog not AlertDialog · D5 CHECK regression repair ·
  D6 route placement · D7 two-row audit separation · D8 no toast
  lib). Closes
  [`docs/issues/278-demo-seed-edge-button.md`](docs/issues/278-demo-seed-edge-button.md).
