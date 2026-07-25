// Slice 082 — seed harness for the Playwright e2e suite.
//
// `seedFromFixture(name)` populates Postgres (+ implicitly MinIO + NATS,
// which the slice-037 docker-compose bring-up already has running) to a
// named spec's preconditions before the spec runs. Invoked from each
// un-shimmed spec's `test.beforeAll()`.
//
// Reads `DATABASE_URL` from env. CI sets it; local devs export it.
//
// Spawns `psql` as a subprocess to apply two SQL files:
//
//   1. `fixtures/walkthroughs/00-seed.sql` — base tenant + scope +
//      framework + control. Shared across all specs.
//   2. `fixtures/e2e/<name>.sql` — per-spec rows (risks, drift, audit
//      period, org tree, feature flags, etc.). One file per spec.
//
// Idempotent: every fixture INSERT uses `ON CONFLICT DO NOTHING`.
//
// Slice 201 — credential issuance moved out of seed.
//
// Before slice 197: this module ALSO inserted an `api_keys` row whose
// `token_hash` matched HMAC-SHA256("test-bearer-e2e", BEARER_HASH_KEY).
// The slice 034 bearer middleware accepted the cookie value the e2e
// fixture set via the `atlas_test_` carve-out. Slice 197 removed both
// the middleware mount and the carve-out — the api_keys row is no
// longer consulted by any code path, so this module no longer inserts
// it. Credential issuance is now the responsibility of
// `web/e2e/global-setup.ts`, which POSTs to the env-gated
// `/v1/test/issue-jwt` endpoint and writes the resulting JWT into
// `process.env.TEST_BEARER`.
//
// The `users` row that backed the slice 164 `/v1/me` resolution path
// continues to be inserted by `fixtures/e2e/settings.sql` directly —
// the JWT minted by global-setup uses the SAME deterministic UUID
// (DEMO_USER_ID, re-exported below) as its `sub` claim so jwtmw's
// synthesized credential's UserID matches the seeded row.

import { execFileSync } from "node:child_process";
import { existsSync } from "node:fs";
import { resolve } from "node:path";

// Deterministic IDs that match fixtures/walkthroughs/00-seed.sql.
// Re-exported so specs can reference rows by symbolic name via
// `web/e2e/fixtures.ts` rather than hard-coding UUIDs in the spec body.
export const DEMO_TENANT_ID = "00000000-0000-0000-0000-00000000d3a0";
export const DEMO_USER_EMAIL = "demo-operator@example.invalid";
export const DEMO_CONTROL_ID = "33333333-3333-3333-3333-333333330001";
export const DEMO_FRAMEWORK_VERSION_ID = "11111111-1111-1111-1111-111111110002";
export const DEMO_AUDIT_PERIOD_ID = "bbbbbbbb-bbbb-bbbb-bbbb-bbbbbbbb0001";
// Slice 164: the principal /v1/me resolves to under the "settings"
// fixture. fixtures/e2e/settings.sql inserts a users row with this UUID.
// Slice 201: the global-setup module also uses this UUID as the JWT
// `sub` claim so the synthesized credential's UserID matches.
export const DEMO_USER_ID = "44444444-4444-4444-4444-444444440001";

// The fixture names the harness understands. Each maps to one
// per-spec SQL file under fixtures/e2e/.
//
// Slice 213 added "audits-header" — seeds one in_progress audit period
// for the topbar in-progress pill assertion.
//
// Slice 223 added "controls-top-bar" — seeds a tenants row for the
// demo tenant UUID so the slice-223 breadcrumb has a non-empty
// left segment to render. The /v1/me/tenants handler joins the
// JWT's available_tenants[] claim against the slice-144 tenants
// table; the d3a0 UUID has no bootstrap row by default in CI.
//
// Slice 389 added "tenant-switch" — seeds two canonical `tenants`
// identity rows (A + B) so the slice-192 GET /v1/me/tenants name
// enrichment resolves, plus a known risk in tenant A only. The
// real-RLS cross-tenant-leak spec (tenant-switch-rls.spec.ts) asserts
// the tenant-A risk is invisible from tenant B through PostgreSQL RLS.
//
// Slice 743 added "controls-list" — un-quarantines
// controls-list.spec.ts. The /controls list renders SCF *anchors*
// (GET /v1/anchors), which are EMPTY in the CI e2e database (no SCF
// import step runs there). The fixture seeds a current-SCF spine + 3
// anchors with varied families, mirrors each anchor id into a matching
// `controls` row (so the slice-468 bulk-assign-owner round-trip
// resolves a real control per selected row), seeds the demo user as the
// assign target, and resets the demo (tenant,user)'s saved_views +
// owner-assignments so the save/load/delete/assign assertions are
// deterministic. See fixtures/e2e/controls-list.sql for the full
// rationale.
export type FixtureName =
  | "dashboard"
  | "control-detail"
  | "audit-workspace"
  | "risk-hierarchy"
  | "admin-bootstrap"
  | "audit-log"
  | "settings"
  | "audits-header"
  | "controls-top-bar"
  | "controls-list"
  | "tenant-switch";

const REPO_ROOT_FROM_WEB = resolve(__dirname, "..", "..");

function fixturesDir(): string {
  return resolve(REPO_ROOT_FROM_WEB, "fixtures");
}

function runPsql(databaseURL: string, sqlPath: string): void {
  if (!existsSync(sqlPath)) {
    throw new Error(`seed: fixture not found at ${sqlPath}`);
  }
  // -v ON_ERROR_STOP=1 makes psql exit non-zero on the first failed
  // statement instead of staggering forward. stdio:inherit so any psql
  // diagnostic shows up in the Playwright runner output and the CI log.
  execFileSync("psql", [databaseURL, "-v", "ON_ERROR_STOP=1", "-f", sqlPath], {
    stdio: "inherit",
  });
}

// seedFromFixture applies the base seed + the named per-spec fixture.
// Throws on any psql failure.
//
// Slice 201: credential issuance is no longer part of seeding. See
// `web/e2e/global-setup.ts` for the JWT mint that runs once per
// Playwright invocation.
export function seedFromFixture(name: FixtureName): void {
  const databaseURL = process.env.DATABASE_URL;
  if (!databaseURL) {
    throw new Error(
      "seed: DATABASE_URL is required; CI sets it for the Playwright job, local devs must export it before running the suite",
    );
  }

  // 1. Base seed (tenant, scope, framework, one control). Shared.
  const baseSql = resolve(fixturesDir(), "walkthroughs", "00-seed.sql");
  runPsql(databaseURL, baseSql);

  // 2. Per-spec rows.
  const specSql = resolve(fixturesDir(), "e2e", `${name}.sql`);
  runPsql(databaseURL, specSql);
}
