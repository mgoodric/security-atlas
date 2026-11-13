// #1756 — /login keeps its email/password form after first install, and a
// local user signs in through it.
//
// Before #1756 the form rendered only while install-state returned the
// bootstrap tenant id, which it stops doing at the first sign-in. This
// spec marks first sign-in done, then signs in through the real form
// against the real atlas server (the server action posts from Node, so a
// page.route() mock could not stand in for it anyway).
//
// The local user is made at run time: a random password, hashed by
// `atlas-cli bootstrap hash-password` (the same argon2id encoding the
// server verifies), inserted with psql. No credential is committed.
//
// Marking first sign-in is not undone: afterAll removes the user, but
// platform_status.first_signin_at stays set in the shared e2e database,
// so specs that run later see a post-first-install platform. Specs that
// depend on first-install state mock /api/install-state rather than read it.
//
// The e2e database is shared by every spec, and other specs add tenants,
// so the install may be single- or multi-tenant when this runs. The form
// follows install-state: with one tenant it shows no tenant field and the
// server resolves the tenant; with several it shows a Tenant ID field,
// which this spec fills. The single-tenant 200 / multi-tenant 400 split is
// pinned by internal/api/auth/local_login_integration_test.go.

import { execFileSync } from "node:child_process";
import { randomBytes, randomUUID } from "node:crypto";
import { resolve } from "node:path";

import { expect, test } from "@playwright/test";

import { ATLAS_JWT_COOKIE } from "../lib/auth";
import { DEMO_TENANT_ID } from "./seed";

const REPO_ROOT = resolve(__dirname, "..", "..");
const NIL_TENANT = "00000000-0000-0000-0000-000000000000";

function databaseURL(): string {
  const url = process.env.DATABASE_URL;
  if (!url) {
    throw new Error(
      "local-login spec: DATABASE_URL is required to create the local user",
    );
  }
  return url;
}

function psql(sql: string, vars: Record<string, string> = {}): string {
  const args = [databaseURL(), "-v", "ON_ERROR_STOP=1", "-At"];
  for (const [k, v] of Object.entries(vars)) args.push("-v", `${k}=${v}`);
  // SQL on stdin so psql interpolates :'var' (it does not for -c).
  return execFileSync("psql", args, { input: sql, encoding: "utf8" }).trim();
}

function hashPassword(plaintext: string): string {
  return execFileSync(
    "go",
    ["run", "./cmd/atlas-cli", "bootstrap", "hash-password"],
    { cwd: REPO_ROOT, input: plaintext, encoding: "utf8" },
  ).trim();
}

// The tenant the user lives in: the only tenant when there is one (the one
// the server resolves), else the demo tenant when present, else the first.
function pickTenant(): string {
  const ids = psql(
    `SELECT id FROM tenants WHERE id <> :'nil' ORDER BY created_at, id;`,
    { nil: NIL_TENANT },
  )
    .split("\n")
    .filter(Boolean);
  if (ids.length === 1) return ids[0];
  if (ids.length === 0 || ids.includes(DEMO_TENANT_ID)) return DEMO_TENANT_ID;
  return ids[0];
}

// The test types a real password. A retained trace or video would carry it
// into the uploaded failure artifact. Top level: Playwright refuses trace
// and video options inside a describe group.
test.use({ trace: "off", video: "off" });

test.describe("local email/password sign-in after first install", () => {
  // `go run` compiles atlas-cli on a cold cache.
  test.describe.configure({ timeout: 300_000 });

  const password = randomBytes(18).toString("base64url");
  const email = `local-login-${randomUUID()}@example.invalid`;
  const userID = randomUUID();
  let tenantID = "";

  test.beforeAll(() => {
    tenantID = pickTenant();
    psql(
      `BEGIN;
       SELECT set_config('app.current_tenant', :'tenant', true);
       INSERT INTO users (id, tenant_id, email, display_name, status)
         VALUES (:'uid', :'tenant', :'email', 'Local Login E2E', 'active');
       INSERT INTO local_credentials (user_id, tenant_id, password_hash, algo, params)
         VALUES (:'uid', :'tenant', :'hash', 'argon2id', '{}'::jsonb);
       INSERT INTO user_roles (tenant_id, user_id, role, granted_by)
         VALUES (:'tenant', :'uid', 'admin', 'e2e:local-login');
       UPDATE platform_status SET first_signin_at = COALESCE(first_signin_at, now());
       COMMIT;`,
      { tenant: tenantID, uid: userID, email, hash: hashPassword(password) },
    );
  });

  test.afterAll(() => {
    psql(
      `BEGIN;
       SELECT set_config('app.current_tenant', :'tenant', true);
       DELETE FROM user_roles WHERE tenant_id = :'tenant' AND user_id = :'uid';
       DELETE FROM users WHERE id = :'uid';
       COMMIT;`,
      { tenant: tenantID, uid: userID },
    );
  });

  test("shows the email/password form and signs a local user in", async ({
    page,
  }) => {
    await page.goto("/login");

    const email$ = page.getByLabel("Email");
    const password$ = page.getByLabel("Password", { exact: true });
    await expect(email$).toBeVisible();
    await expect(password$).toBeVisible();

    const tenantField = page.getByLabel("Tenant ID");
    if (await tenantField.isVisible()) {
      await tenantField.fill(tenantID);
    }
    await email$.fill(email);
    await password$.fill(password);
    await page.getByRole("button", { name: "Sign in" }).first().click();

    await expect(page).not.toHaveURL(/\/login/);
    const cookies = await page.context().cookies();
    expect(cookies.some((c) => c.name === ATLAS_JWT_COOKIE && c.value)).toBe(
      true,
    );
  });
});
