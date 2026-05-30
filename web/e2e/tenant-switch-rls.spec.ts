// Slice 389 — REAL-Postgres-RLS cross-tenant-leak e2e spec.
//
// This is the depth assertion slice 351's mocked `tenant-switch.spec.ts`
// explicitly deferred here (see that file's MOCK STRATEGY block + slice
// 333 Q-9). Where 351 proves the switch FLOW with `route.fulfill`
// mocks, this spec drives the REAL stack end-to-end and asserts the
// constitutional v1 binary tenant-isolation criterion (invariant #6):
//
//   a row that exists ONLY in tenant A is NOT visible from a tenant-B
//   view, through genuine PostgreSQL Row-Level Security — not a mock.
//
// Why 351 couldn't do this: the slice-201 `/v1/test/issue-jwt` endpoint
// minted a SINGLE-tenant JWT, and the RFC 8693 token-exchange
// tenant-switch (slice 188/192) requires the target tenant to already
// be in `atlas:available_tenants[]`. Slice 389 extends that endpoint to
// mint a genuine MULTI-tenant JWT (still ATLAS_TEST_MODE-gated — the
// extension does not widen the production attack surface), which lets
// this spec exercise the real switch + real RLS.
//
// THE FLOW:
//   1. Seed (beforeAll): two `tenants` rows (A + B) + a known "canary"
//      risk in tenant A only (fixtures/e2e/tenant-switch.sql).
//   2. Mint a multi-tenant JWT: current = A, available = [A, B].
//      WHY super_admin = true (decisions log D4): super_admin is an
//      OPA-authz + token-exchange-allowlist concept ONLY — it does NOT
//      bypass PostgreSQL RLS. The jwtmw middleware sets
//      `app.current_tenant` from the verified claim's CURRENT tenant
//      regardless of super_admin (internal/auth/jwtmw/middleware.go
//      P0-190-3, line ~184), and the slice-005 `risks` RLS policy
//      filters on that GUC. So the cross-tenant-leak assertion is
//      exactly as strong with super_admin=true. We need it because the
//      synthetic test user has no `user_roles` rows; the authz input
//      bridge (internal/authz/input.go derivedRolesFor) maps a
//      non-super-admin synthetic credential whose OwnerRoles are
//      non-empty to `control_owner` — which cannot read /v1/risks — so
//      a super_admin=false JWT would 403 at the OPA layer BEFORE RLS
//      ever runs, masking the very thing under test. super_admin=true
//      gets us a clean read path at authz while leaving RLS the sole
//      gate on tenant visibility — which is the whole point.
//   3. Inject the JWT as the `atlas_jwt` session cookie.
//   4. Visit /risks while current = A → the canary risk IS visible.
//   5. Switch to tenant B via the real `<TenantSwitcher>` → the BFF
//      calls /oauth/token (RFC 8693), rotates the atlas_jwt cookie to a
//      token whose current_tenant = B.
//   6. Re-visit /risks while current = B → the canary risk is NOT
//      visible. The jwtmw middleware sets `app.current_tenant = B` from
//      the verified claim (P0-190-3); the slice-005 RLS policy
//      `current_tenant_matches(tenant_id)` on `risks` then filters out
//      every tenant-A row at the database layer.
//
// DETERMINISM: every assertion auto-waits (`expect(...).toBeVisible()` /
// `toHaveCount(0)`) and every navigation that depends on async data is
// gated on `page.waitForResponse(...)` set up BEFORE the `goto`
// (web/e2e/README.md timing rules). No fixed sleeps; no `.count()`
// snapshots of async-arriving data.
//
// HARD RULE (P0-A9): all ids/values are neutral test strings. The
// minted JWT is fetched at runtime and injected as a cookie value — it
// is NEVER written as a literal in this file (no `eyJ*` literal that
// GitGuardian would flag).

import { expect, test, type Page } from "./fixtures";
import { seedFromFixture } from "./seed";

const TENANT_A = "aaaaaaaa-aaaa-aaaa-aaaa-aaaaaaaaaaaa";
const TENANT_B = "bbbbbbbb-bbbb-bbbb-bbbb-bbbbbbbbbbbb";
const TENANT_A_NAME = "Acme Security (tenant A)";
const TENANT_B_NAME = "Globex Compliance (tenant B)";

// The canary risk seeded ONLY in tenant A (fixtures/e2e/tenant-switch.sql).
const CANARY_RISK_TITLE = "Tenant-A-only cross-tenant-leak canary risk";

function atlasBaseURL(): string {
  return process.env.ATLAS_HTTP_URL ?? "http://localhost:8080";
}

// mintMultiTenantJWT POSTs to the slice-389-extended
// /v1/test/issue-jwt, requesting a JWT whose current tenant is A and
// whose available_tenants[] spans A + B. super_admin=true gives a clean
// OPA-authz read path for the synthetic (no user_roles) test user while
// leaving PostgreSQL RLS as the SOLE gate on tenant visibility — see
// the WHY super_admin block in the file header + decisions log D4.
// Throws loudly on any non-200 — the spec cannot run without the
// multi-tenant credential.
async function mintMultiTenantJWT(): Promise<string> {
  const url = `${atlasBaseURL()}/v1/test/issue-jwt`;
  const res = await fetch(url, {
    method: "POST",
    headers: { "Content-Type": "application/json" },
    body: JSON.stringify({
      tenant_id: TENANT_A,
      available_tenants: [TENANT_A, TENANT_B],
      roles: ["admin"],
      super_admin: true,
    }),
  });
  if (res.status === 404) {
    throw new Error(
      `slice 389: ${url} returned 404 — ATLAS_TEST_MODE=1 must be set on the atlas server process (NOT on this Playwright runner).`,
    );
  }
  if (!res.ok) {
    throw new Error(
      `slice 389: ${url} returned ${res.status}: ${await res.text()}`,
    );
  }
  const parsed = (await res.json()) as { token?: string };
  if (!parsed.token) {
    throw new Error(`slice 389: ${url} returned 200 but no token field`);
  }
  return parsed.token;
}

// injectBearer sets the freshly-minted multi-tenant JWT as the
// `atlas_jwt` session cookie on the page's context. Mirrors the auth
// fixture's mode-1 path but with OUR bearer (the shared fixture injects
// the demo single-tenant bearer, which has no second tenant to switch
// into). Uses the same cookie name both the risks BFF (SESSION_COOKIE)
// and the switch BFF (ATLAS_JWT_COOKIE) read — they are both `atlas_jwt`.
async function injectBearer(page: Page, bearer: string): Promise<void> {
  const url = new URL(process.env.PLATFORM_BASE_URL ?? "http://localhost:3000");
  await page.context().addCookies([
    {
      name: "atlas_jwt",
      value: bearer,
      domain: url.hostname,
      path: "/",
      httpOnly: true,
      secure: url.protocol === "https:",
      sameSite: "Lax",
    },
  ]);
}

// gotoRisks navigates to /risks and waits for the BFF risks fetch to
// land, so downstream assertions on the rendered list aren't racing the
// data fetch (web/e2e/README.md — gate the first visibility assertion
// on the network round-trip).
async function gotoRisks(page: Page): Promise<void> {
  const risksResp = page.waitForResponse(
    (r) => r.url().includes("/api/risks") && r.status() === 200,
    { timeout: 30_000 },
  );
  await page.goto("/risks");
  await risksResp;
}

test.describe("tenant-switch real-RLS cross-tenant-leak (slice 389)", () => {
  test.beforeAll(() => {
    // Seed two tenant identity rows + the tenant-A canary risk.
    seedFromFixture("tenant-switch");
  });

  test("AC-2: a tenant-A row is NOT visible from tenant B through real PostgreSQL RLS", async ({
    page,
  }) => {
    const bearer = await mintMultiTenantJWT();
    await injectBearer(page, bearer);

    // --- 1. While current_tenant = A: the canary risk IS visible. ---
    await gotoRisks(page);

    // The list shell renders the title twice — once in the desktop table
    // (`list-cell-title`) and once in the `< md` card stack
    // (`list-card-cell-title`) — both present in the DOM, only one
    // visible per viewport (slice 281 responsive `mobileMode="cards"`).
    // Scope the canary locator to the desktop table cell so the
    // presence assertion is unambiguous (strict-mode safe) and the
    // absence assertion below counts the same single surface.
    const canaryRow = canaryLocator(page);
    await expect(canaryRow).toBeVisible({ timeout: 30_000 });

    // The multi-tenant switcher chrome renders (>1 tenant), labelled with
    // the current tenant (A). This also proves the slice-389 multi-tenant
    // JWT drove the real /v1/me/tenants enrichment (two named tenants).
    const switcher = page.getByRole("button", { name: "Switch tenant" });
    await expect(switcher).toBeVisible({ timeout: 30_000 });
    await expect(switcher).toContainText(TENANT_A_NAME);

    // --- 2. Switch to tenant B via the REAL token-exchange. ---
    await switcher.click();
    await expect(
      page.getByRole("listbox", { name: "Available tenants" }),
    ).toBeVisible();

    // The switch POST goes to the BFF, which calls the platform
    // /oauth/token RFC 8693 grant and rotates the atlas_jwt cookie. Gate
    // on the 200 so we know the cookie has been rewritten before we
    // re-navigate.
    const switchResp = page.waitForResponse(
      (r) => r.url().includes("/api/auth/switch-tenant") && r.status() === 200,
      { timeout: 30_000 },
    );
    await page
      .getByRole("option", { name: TENANT_B_NAME, exact: true })
      .click();
    await switchResp;

    // --- 3. While current_tenant = B: the canary risk is NOT visible. ---
    // A fresh navigation re-reads the rotated atlas_jwt cookie
    // server-side; jwtmw sets app.current_tenant = B from the verified
    // claim; the risks RLS policy filters out every tenant-A row.
    await gotoRisks(page);

    // The switcher now reflects tenant B as current — the cookie carried
    // real tenant scope through to the server components.
    await expect(switcher).toContainText(TENANT_B_NAME, { timeout: 30_000 });

    // THE LOAD-BEARING ASSERTION: the tenant-A canary risk does not
    // appear in the tenant-B view. This is the real-RLS isolation
    // proof — invariant #6 enforced at the database layer, not a mock.
    // toHaveCount(0) on the desktop-scoped locator auto-waits/polls, so
    // it fails (correctly) if the row ever appears within the timeout.
    await expect(canaryLocator(page)).toHaveCount(0);
  });
});

// canaryLocator scopes the canary-risk title to the desktop table cell
// (`list-cell-title`), disambiguating from the `< md` card-stack copy
// (`list-card-cell-title`) the list shell also renders. Both presence
// and absence assertions use this single surface so they are exact
// mirrors of one another.
function canaryLocator(page: Page) {
  return page
    .getByTestId("list-cell-title")
    .getByTestId("risks-row-title")
    .filter({ hasText: CANARY_RISK_TITLE });
}
