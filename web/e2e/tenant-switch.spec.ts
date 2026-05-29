// Slice 351 (AC-2) — Playwright E2E for the multi-tenant tenant-switch
// flow. Closes slice 333 Q-9: the most security-critical multi-tenant
// flow had zero e2e coverage.
//
// The flow under test (slice 192 `<TenantSwitcher>`):
//
//   1. A user whose JWT carries >1 `atlas:available_tenants[]` sees the
//      switcher dropdown in the topbar. GET /api/me/tenants drives it.
//   2. Picking a non-current tenant calls switchTenant() →
//      POST /api/auth/switch-tenant, which the BFF exchanges via the
//      RFC 8693 token-exchange grant and rewrites the atlas_jwt cookie.
//      The page then router.refresh()es so server components re-render
//      under the new tenant scope.
//   3. A single-tenant user sees NO tenant chrome at all
//      (P0-192-1 / canvas §11 #13: `if (tenants.length <= 1) return null`).
//
// MOCK STRATEGY (P0-4 — established `route.fulfill` convention):
//
// The platform's `/v1/test/issue-jwt` endpoint mints a SINGLE-tenant
// JWT (`internal/api/testissuejwt.go`: `AvailableTenants:
// []uuid.UUID{tenant}`), and the RFC 8693 token-exchange requires the
// target tenant to already be in `available_tenants[]`. So a true
// multi-tenant switch cannot be provisioned by the docker-compose
// bring-up today. Per the seed-harness contract (web/e2e/README.md) and
// anti-criterion P0-4, this spec mocks the three BFF endpoints the flow
// consumes (`/api/me/tenants`, `/api/auth/switch-tenant`) with
// `page.route`, exactly as questionnaires.spec.ts does. The
// real-Postgres-RLS cross-tenant-leak variant is filed as slice 389
// (needs the multi-tenant JWT the harness can't mint yet).
//
// The cross-tenant assertion this mocked spec CAN make honestly: after
// the switch round-trips, the edge-visible tenant context (which tenant
// is flagged `current` by the JWT-gated, RLS-backed /api/me/tenants)
// flips to the target — i.e. the switch carried real tenant scope, not
// a no-op. Combined with the switch POST body assertion (the target
// tenant id is what crosses the wire), this proves the flow changes the
// tenant context for subsequent requests. The deeper "tenant-A row not
// visible in tenant-B view through real RLS" assertion is slice 389.
//
// Determinism: every assertion auto-waits via `expect(...).toBeVisible()`
// / `expect(...).toHaveText()` or gates on `page.waitForResponse(...)`
// before reading post-fetch state. No fixed sleeps; no `.count()`
// snapshots of async-arriving data (web/e2e/README.md timing rules).
//
// Hard rule (P0-A9): all ids/values below are neutral test strings. No
// vendor-prefixed tokens.

import { expect, test } from "./fixtures";

const TENANT_A = "aaaaaaaa-aaaa-aaaa-aaaa-aaaaaaaaaaaa";
const TENANT_B = "bbbbbbbb-bbbb-bbbb-bbbb-bbbbbbbbbbbb";
const TENANT_A_NAME = "Acme Security (tenant A)";
const TENANT_B_NAME = "Globex Compliance (tenant B)";

type TenantRow = { id: string; name: string; current: boolean };

function tenantsBody(currentId: string): { tenants: TenantRow[] } {
  return {
    tenants: [
      { id: TENANT_A, name: TENANT_A_NAME, current: currentId === TENANT_A },
      { id: TENANT_B, name: TENANT_B_NAME, current: currentId === TENANT_B },
    ],
  };
}

test.describe("tenant-switch (slice 351 AC-2)", () => {
  test("AC-2a: a user with N tenants sees the switcher with the current tenant labelled", async ({
    authedPage: page,
  }) => {
    // Current tenant = A. The dropdown button shows the current tenant
    // name; the list (when opened) marks A as current.
    await page.route("**/api/me/tenants", async (route) => {
      await route.fulfill({
        status: 200,
        contentType: "application/json",
        body: JSON.stringify(tenantsBody(TENANT_A)),
      });
    });

    const tenantsResp = page.waitForResponse(
      (r) => r.url().includes("/api/me/tenants") && r.status() === 200,
      { timeout: 30_000 },
    );
    await page.goto("/dashboard");
    await tenantsResp;

    // The switcher renders (>1 tenant). Button label = current tenant.
    const switcher = page.getByRole("button", { name: "Switch tenant" });
    await expect(switcher).toBeVisible({ timeout: 30_000 });
    await expect(switcher).toContainText(TENANT_A_NAME);

    // Open the dropdown; both tenants are listed, A is the current one.
    await switcher.click();
    const listbox = page.getByRole("listbox", { name: "Available tenants" });
    await expect(listbox).toBeVisible();
    // Exact-string match — the names contain "(tenant A)" parens which
    // would be regex groups; use literal matching with exact:true.
    const optionA = page.getByRole("option", {
      name: TENANT_A_NAME,
      exact: true,
    });
    const optionB = page.getByRole("option", {
      name: TENANT_B_NAME,
      exact: true,
    });
    await expect(optionA).toBeVisible();
    await expect(optionB).toBeVisible();
    await expect(optionA).toHaveAttribute("aria-selected", "true");
    await expect(optionB).toHaveAttribute("aria-selected", "false");
  });

  test("AC-2b: switching changes the tenant context for subsequent requests", async ({
    authedPage: page,
  }) => {
    // State-carrying closure: /api/me/tenants reports A current until the
    // switch POST lands, then reports B current — mirroring the
    // server-side cookie rotation the BFF performs on a successful
    // token-exchange. This is the edge-visible proof the context flipped.
    let currentTenant = TENANT_A;
    await page.route("**/api/me/tenants", async (route) => {
      await route.fulfill({
        status: 200,
        contentType: "application/json",
        body: JSON.stringify(tenantsBody(currentTenant)),
      });
    });

    // Capture the switch POST body to assert the target tenant crosses
    // the wire (the flow goes through the real switchTenant() path, not
    // a local-only state mutation).
    let switchTargetId: string | null = null;
    await page.route("**/api/auth/switch-tenant", async (route, req) => {
      const body = JSON.parse(req.postData() ?? "{}") as {
        target_tenant_id?: string;
      };
      switchTargetId = body.target_tenant_id ?? null;
      // BFF success contract: 200 + { ok: true }. The real BFF also
      // rewrites the atlas_jwt cookie; the closure flip above models the
      // post-switch tenant context the new cookie would carry.
      currentTenant = TENANT_B;
      await route.fulfill({
        status: 200,
        contentType: "application/json",
        body: JSON.stringify({ ok: true }),
      });
    });

    const firstTenantsResp = page.waitForResponse(
      (r) => r.url().includes("/api/me/tenants") && r.status() === 200,
      { timeout: 30_000 },
    );
    await page.goto("/dashboard");
    await firstTenantsResp;

    const switcher = page.getByRole("button", { name: "Switch tenant" });
    await expect(switcher).toBeVisible({ timeout: 30_000 });
    await expect(switcher).toContainText(TENANT_A_NAME);

    await switcher.click();
    await expect(
      page.getByRole("listbox", { name: "Available tenants" }),
    ).toBeVisible();

    // Pick tenant B. The component calls switchTenant() → the switch
    // POST → postTenantSwitched() → router.refresh(). The re-fetch of
    // /api/me/tenants then reports B as current.
    const switchResp = page.waitForResponse(
      (r) => r.url().includes("/api/auth/switch-tenant") && r.status() === 200,
      { timeout: 30_000 },
    );
    await page
      .getByRole("option", { name: TENANT_B_NAME, exact: true })
      .click();
    await switchResp;

    // The target tenant id is what crossed the wire — the switch carried
    // real tenant scope.
    expect(switchTargetId).toBe(TENANT_B);

    // After the switch, the edge-visible tenant context is B. The
    // switcher button label reflects the new current tenant once the
    // component's post-switch re-fetch settles. Auto-waiting assertion
    // closes the refresh race.
    await expect(switcher).toContainText(TENANT_B_NAME, { timeout: 30_000 });
  });

  test("AC-2c: a single-tenant user sees NO tenant chrome (no cross-tenant surface)", async ({
    authedPage: page,
  }) => {
    // P0-192-1: the switcher returns null when tenants.length <= 1. A
    // single-tenant operator must never see tenant chrome — the absence
    // of the control is itself the multi-tenant-isolation guarantee at
    // the UI edge (you cannot switch into a tenant you don't belong to).
    await page.route("**/api/me/tenants", async (route) => {
      await route.fulfill({
        status: 200,
        contentType: "application/json",
        body: JSON.stringify({
          tenants: [{ id: TENANT_A, name: TENANT_A_NAME, current: true }],
        }),
      });
    });

    const tenantsResp = page.waitForResponse(
      (r) => r.url().includes("/api/me/tenants") && r.status() === 200,
      { timeout: 30_000 },
    );
    await page.goto("/dashboard");
    await tenantsResp;

    // Gate on a stable authed-shell element so we know the page settled
    // before asserting the switcher's ABSENCE (asserting absence without
    // a settled page is a false-pass risk).
    await expect(page.getByRole("navigation").first()).toBeVisible({
      timeout: 30_000,
    });
    await expect(
      page.getByRole("button", { name: "Switch tenant" }),
    ).toHaveCount(0);
  });
});
