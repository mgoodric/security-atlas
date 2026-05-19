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
// Then inserts an `api_keys` row whose `token_hash` matches
// HMAC-SHA256("test-bearer-e2e", BEARER_HASH_KEY) so the platform's
// bearer middleware accepts the cookie value the e2e fixture sets.
//
// Idempotent: every fixture INSERT uses `ON CONFLICT DO NOTHING`; the
// api_keys row is upserted via DELETE-then-INSERT keyed on `token_hash`.
//
// Hard rule (P0-A3): every credential / token literal in this file is
// a neutral test string. NO `ghp_*`, `sk_*`, `eyJ*`, `AKIA*` —
// GitGuardian flags them even in test files (slice 069 P0-A9 + slice
// 082 P0-A3).

import { execFileSync } from "node:child_process";
import { createHmac } from "node:crypto";
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
// fixture. fixtures/e2e/settings.sql inserts a users row with this UUID
// and `seedApiKey()` sets `api_keys.issued_by` to it (only for the
// "settings" fixture) so the profile handler resolves a real users row
// (non-synthetic profile path).
export const DEMO_USER_ID = "44444444-4444-4444-4444-444444440001";

// The neutral test bearer the Playwright fixture sets as the session
// cookie. The platform's bearer middleware HMACs this with
// BEARER_HASH_KEY and looks up the row in api_keys; the harness inserts
// that row.
export const TEST_BEARER = "test-bearer-e2e";

// The seven fixture names the harness understands. Each maps to one
// per-spec SQL file under fixtures/e2e/.
export type FixtureName =
  | "dashboard"
  | "control-detail"
  | "audit-workspace"
  | "risk-hierarchy"
  | "admin-bootstrap"
  | "audit-log"
  | "settings";

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

function hexHmacSha256(key: string, message: string): string {
  return createHmac("sha256", key).update(message).digest("hex");
}

function seedApiKey(databaseURL: string, name: FixtureName): void {
  // The platform requires BEARER_HASH_KEY to be >=32 bytes (see
  // internal/auth/bearer/bearer.go HashKeyMinBytes). The harness uses
  // the env value the atlas server boots with so the hash matches at
  // verify time. If the env is missing or short, fall back to a
  // 32-byte test string and document that the atlas process MUST be
  // booted with the same value (the CI workflow sets it; local devs
  // need to export it before running the suite).
  const key =
    process.env.BEARER_HASH_KEY ?? "test-bearer-hash-key-32-bytes-ok!!"; // 33 chars, ASCII

  if (key.length < 32) {
    throw new Error(
      `seed: BEARER_HASH_KEY must be at least 32 bytes; got ${key.length}`,
    );
  }
  const tokenHashHex = hexHmacSha256(key, TEST_BEARER);

  // Slice 164: for the "settings" fixture, the api_keys row's
  // `issued_by` is set to DEMO_USER_ID so the profile handler resolves
  // a real users row (slice 108 synthetic-profile fallback is bypassed
  // — the row matches users.id inserted by fixtures/e2e/settings.sql).
  // The five pre-existing fixtures keep the historical NULL behavior;
  // they don't need a real users row to drive their AC bodies.
  let issuedByColumn = "";
  let issuedByValue = "";
  if (name === "settings") {
    issuedByColumn = ", issued_by";
    issuedByValue = `, '${DEMO_USER_ID}'::uuid`;
  }

  // Composite SQL: clear any prior row with this hash, then insert a
  // fresh admin row in the demo tenant. The DELETE handles the
  // re-run case across separate test invocations; the `ON CONFLICT
  // (token_hash) DO NOTHING` clause handles the parallel-worker case
  // within ONE test invocation (Playwright defaults to multiple
  // workers, each calling `seedFromFixture()` via test.beforeAll —
  // the DELETEs see no row, then the INSERTs race; without ON
  // CONFLICT the second insert fails the UNIQUE constraint on
  // token_hash). All workers within ONE invocation insert identical
  // row content (deterministic from TEST_BEARER + BEARER_HASH_KEY +
  // fixture name), so DO NOTHING is the right semantics. Slice 122 fix.
  // is_admin=true so the admin-bootstrap spec's /admin routes pass
  // the authz gate.
  const sql = `
    DELETE FROM api_keys WHERE token_hash = decode('${tokenHashHex}', 'hex');
    INSERT INTO api_keys (tenant_id, token_hash, is_admin, owner_roles, last4${issuedByColumn})
    VALUES (
      '${DEMO_TENANT_ID}',
      decode('${tokenHashHex}', 'hex'),
      TRUE,
      ARRAY['admin']::TEXT[],
      '${TEST_BEARER.slice(-4)}'${issuedByValue}
    )
    ON CONFLICT (token_hash) DO NOTHING;
  `;
  execFileSync("psql", [databaseURL, "-v", "ON_ERROR_STOP=1", "-c", sql], {
    stdio: "inherit",
  });
}

// seedFromFixture applies the base seed + the named per-spec fixture +
// the api_keys row. Throws on any psql failure.
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

  // 3. The API key the Playwright fixture's cookie matches.
  seedApiKey(databaseURL, name);
}
