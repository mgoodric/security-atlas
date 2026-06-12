// Slice 674 (AC-4) — after switching tenants, the dashboard Program H1
// tenant chip AND the topbar breadcrumb tenant segment update to the
// ACTIVE tenant's name (matching the switcher), not the origin tenant.
//
// The bug: the H1 (`TenantContext`) and `Breadcrumb` fetched
// `/api/me/tenants` only on mount. An in-tab switch rotates the JWT
// cookie + calls router.refresh() (which re-renders server components
// but does NOT re-run a mounted client component's mount effect), so the
// H1/breadcrumb kept showing the origin ("Default Tenant") name while the
// switcher (which re-fetches on the slice-199 `tenant-switched`
// broadcast) showed the new one. The fix routes both surfaces through the
// shared `useCurrentTenantName` hook, which subscribes to that same
// broadcast.
//
// MOCK STRATEGY (mirrors tenant-switch.spec.ts / P0-4): the docker-compose
// bring-up's test JWT is single-tenant, and the RFC 8693 token-exchange
// requires the target tenant to already be in `available_tenants[]`, so a
// true multi-tenant switch cannot be provisioned by the harness today.
// We mock the BFF endpoints the flow consumes (`/api/me/tenants`,
// `/api/auth/switch-tenant`) with a state-carrying closure, exactly as
// the existing tenant-switch spec does (slice 351). The slice-389
// real-Postgres-RLS variant is the deeper cross-tenant-leak proof; this
// spec proves the NAME-DISPLAY surfaces flip with the active tenant.
//
// Determinism: every assertion auto-waits via expect(...).toContainText
// / waitForResponse. No fixed sleeps. All ids/names are neutral test
// strings (P0-A9 — no vendor-prefixed tokens).

import { expect, test } from "./fixtures";

const TENANT_A = "aaaaaaaa-aaaa-aaaa-aaaa-aaaaaaaaaaaa";
const TENANT_B = "bbbbbbbb-bbbb-bbbb-bbbb-bbbbbbbbbbbb";
const TENANT_A_NAME = "Origin Tenant (tenant A)";
const TENANT_B_NAME = "Switched Tenant (tenant B)";

type TenantRow = { id: string; name: string; current: boolean };

function tenantsBody(currentId: string): { tenants: TenantRow[] } {
  return {
    tenants: [
      { id: TENANT_A, name: TENANT_A_NAME, current: currentId === TENANT_A },
      { id: TENANT_B, name: TENANT_B_NAME, current: currentId === TENANT_B },
    ],
  };
}

test.describe("dashboard tenant name after switch (slice 674)", () => {
  test("AC-1/AC-4: H1 + breadcrumb flip to the active tenant name on switch", async ({
    authedPage: page,
  }) => {
    // State-carrying closure: report A current until the switch POST
    // lands, then report B current — modelling the cookie rotation the
    // BFF performs on a successful token-exchange.
    let currentTenant = TENANT_A;
    await page.route("**/api/me/tenants", async (route) => {
      await route.fulfill({
        status: 200,
        contentType: "application/json",
        body: JSON.stringify(tenantsBody(currentTenant)),
      });
    });
    await page.route("**/api/auth/switch-tenant", async (route, req) => {
      const body = JSON.parse(req.postData() ?? "{}") as {
        target_tenant_id?: string;
      };
      currentTenant = body.target_tenant_id === TENANT_B ? TENANT_B : TENANT_A;
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

    // BEFORE switch: every tenant-name surface shows tenant A.
    const h1Tenant = page.getByTestId("dashboard-header-tenant-context");
    const breadcrumbTenant = page.getByTestId("breadcrumb-tenant");
    const switcher = page.getByRole("button", { name: "Switch tenant" });

    await expect(switcher).toBeVisible({ timeout: 30_000 });
    await expect(switcher).toContainText(TENANT_A_NAME);
    await expect(h1Tenant).toHaveText(TENANT_A_NAME, { timeout: 30_000 });
    await expect(breadcrumbTenant).toHaveText(TENANT_A_NAME, {
      timeout: 30_000,
    });

    // Switch to tenant B via the switcher (the real onPick path:
    // switchTenant() → switch POST → postTenantSwitched() →
    // router.refresh()). The broadcast nudges the shared
    // useCurrentTenantName hook to re-fetch.
    await switcher.click();
    await expect(
      page.getByRole("listbox", { name: "Available tenants" }),
    ).toBeVisible();

    const switchResp = page.waitForResponse(
      (r) => r.url().includes("/api/auth/switch-tenant") && r.status() === 200,
      { timeout: 30_000 },
    );
    await page
      .getByRole("option", { name: TENANT_B_NAME, exact: true })
      .click();
    await switchResp;

    // AFTER switch: the switcher button AND both name-display surfaces
    // (H1 + breadcrumb) reflect tenant B — no hard reload required. This
    // is the regression slice 674 closes: pre-fix the H1/breadcrumb
    // stayed on tenant A.
    await expect(switcher).toContainText(TENANT_B_NAME, { timeout: 30_000 });
    await expect(h1Tenant).toHaveText(TENANT_B_NAME, { timeout: 30_000 });
    await expect(breadcrumbTenant).toHaveText(TENANT_B_NAME, {
      timeout: 30_000,
    });
  });
});
