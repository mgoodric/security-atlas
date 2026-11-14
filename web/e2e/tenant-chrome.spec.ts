// #1764 — Playwright E2E: the web app's tenant chrome follows the install's
// tenant count (ADR 0023 decision 2; tenant-count signal from #1763's
// `GET /v1/me/tenancy`, read through the `/api/me/tenancy` BFF).
//
//   - One tenant: no Tenants / Super admins links in the admin nav; the
//     Settings "Tenant" card reads "Organisation"; the assign-user dialog
//     has no tenant field and the assignment lands in the sole tenant.
//   - Two tenants (the multi-tenant opt-in has been used): the links, the
//     "Tenant" settings label, the tenant field and the switcher appear.
//
// HERMETIC BY CONSTRUCTION: the shared docker-compose DB holds however
// many tenants earlier specs seeded, so the tenant count is mocked at the
// BFF (`/api/me/tenancy`), as are the tenant directory (`/api/me/tenants`,
// which drives the switcher and the Settings card) and the admin users
// BFFs — the same `page.route` convention as admin-users.spec.ts and
// tenant-switch.spec.ts. The seed bearer is is_admin=true, so /admin and
// the admin-gated Settings card render.

import type { Page } from "@playwright/test";

import { expect, test } from "./fixtures";

const SOLE_TENANT = "11111111-1111-4111-8111-111111111111";
const OTHER_TENANT = "22222222-2222-4222-8222-222222222222";
const USER_1 = "aaaaaaaa-1111-4111-8111-111111111111";

async function mockTenancy(page: Page, tenantCount: number): Promise<void> {
  await page.route("**/api/me/tenancy", (route) =>
    route.fulfill({
      status: 200,
      contentType: "application/json",
      body: JSON.stringify({
        tenant_count: tenantCount,
        multi_tenant: tenantCount > 1,
      }),
    }),
  );
}

async function mockTenantDirectory(page: Page, ids: string[]): Promise<void> {
  await page.route("**/api/me/tenants", (route) =>
    route.fulfill({
      status: 200,
      contentType: "application/json",
      body: JSON.stringify({
        tenants: ids.map((id, i) => ({
          id,
          name: i === 0 ? "Acme Security" : "Globex Compliance",
          current: i === 0,
        })),
      }),
    }),
  );
}

// mockAdminUsers stubs the users page BFFs in the super-admin shape (the
// bootstrap admin is super-admin on a single-tenant install, #1759) and
// records each assign POST body.
function mockAdminUsers(
  page: Page,
  tenantIds: string[],
): { posts: Array<{ tenant_id?: string; user_id?: string }> } {
  const posts: Array<{ tenant_id?: string; user_id?: string }> = [];
  void page.route("**/api/me", (route) =>
    route.fulfill({
      status: 200,
      contentType: "application/json",
      body: JSON.stringify({ tenant_id: SOLE_TENANT }),
    }),
  );
  void page.route("**/api/admin/tenants", (route) =>
    route.fulfill({
      status: 200,
      contentType: "application/json",
      body: JSON.stringify({
        items: tenantIds.map((id, i) => ({
          id,
          name: i === 0 ? "Acme Security" : "Globex Compliance",
          slug: i === 0 ? "acme" : "globex",
          is_bootstrap_tenant: i === 0,
          created_at: "2026-11-01T00:00:00Z",
        })),
      }),
    }),
  );
  void page.route("**/api/admin/users", async (route) => {
    if (route.request().method() === "POST") {
      posts.push(route.request().postDataJSON());
      await route.fulfill({
        status: 200,
        contentType: "application/json",
        body: JSON.stringify({
          user_id: USER_1,
          tenant_id: SOLE_TENANT,
          roles: ["viewer"],
          reauth_required: false,
        }),
      });
      return;
    }
    await route.fulfill({
      status: 200,
      contentType: "application/json",
      body: JSON.stringify({
        cross_tenant: true,
        items: [
          {
            id: USER_1,
            tenant_id: SOLE_TENANT,
            email: "alpha@example.com",
            display_name: "Alpha User",
            status: "active",
            roles: ["admin"],
          },
        ],
      }),
    });
  });
  return { posts };
}

test.describe("tenant chrome follows tenant count (#1764)", () => {
  test("one tenant: no tenant chrome; a new user lands in the sole tenant", async ({
    authedPage: page,
  }) => {
    await mockTenancy(page, 1);
    await mockTenantDirectory(page, [SOLE_TENANT]);
    const users = mockAdminUsers(page, [SOLE_TENANT]);

    const tenancyResp = page.waitForResponse("**/api/me/tenancy");
    await page.goto("/admin/users");
    await tenancyResp;

    const nav = page.getByRole("navigation", { name: "Admin sections" });
    await expect(nav.getByRole("link", { name: "Users" })).toBeVisible();
    await expect(nav.getByRole("link", { name: "Tenants" })).toHaveCount(0);
    await expect(nav.getByRole("link", { name: "Super admins" })).toHaveCount(
      0,
    );

    // No super-admin banner, self-assign, or Tenant column.
    await expect(page.getByTestId("users-table")).toBeVisible();
    await expect(page.getByTestId("users-tenancy-banner")).toHaveCount(0);
    await expect(page.getByTestId("open-self-assign")).toHaveCount(0);
    await expect(
      page.getByTestId("users-table").getByRole("columnheader", {
        name: "Tenant",
      }),
    ).toHaveCount(0);

    // Adding a user needs no tenant UUID and lands them in the sole tenant.
    await page.getByTestId("open-assign-user").click();
    await expect(page.getByTestId("assign-user-dialog")).toBeVisible();
    await expect(page.locator("#assign-tenant-id")).toHaveCount(0);
    await page.getByTestId("assign-user-select").selectOption(USER_1);
    await page.getByTestId("assign-role-viewer").check();
    const post = page.waitForRequest(
      (r) => r.url().endsWith("/api/admin/users") && r.method() === "POST",
    );
    await page.getByTestId("assign-user-submit").click();
    await post;
    await expect(page.getByTestId("assign-user-dialog")).toBeHidden();
    expect(users.posts).toHaveLength(1);
    expect(users.posts[0].tenant_id).toBe(SOLE_TENANT);
    expect(users.posts[0].user_id).toBe(USER_1);

    // Settings presents the tenant as the organisation, with no tenant ID.
    await page.goto("/settings");
    await expect(page.getByTestId("settings-tenant-title")).toHaveText(
      "Organisation",
    );
    await expect(page.getByTestId("settings-tenant-id")).toHaveCount(0);
    await expect(page.getByTestId("settings-tenant-gate-policy")).toBeVisible();

    // The switcher stays hidden.
    await expect(
      page.getByRole("button", { name: "Switch tenant" }),
    ).toHaveCount(0);
  });

  test("two tenants: links, Tenant label, tenant field and switcher appear", async ({
    authedPage: page,
  }) => {
    await mockTenancy(page, 2);
    await mockTenantDirectory(page, [SOLE_TENANT, OTHER_TENANT]);
    mockAdminUsers(page, [SOLE_TENANT, OTHER_TENANT]);

    await page.goto("/admin/users");

    const nav = page.getByRole("navigation", { name: "Admin sections" });
    await expect(nav.getByRole("link", { name: "Tenants" })).toBeVisible();
    await expect(nav.getByRole("link", { name: "Super admins" })).toBeVisible();
    await expect(page.getByTestId("users-tenancy-banner")).toBeVisible();
    await expect(page.getByTestId("open-self-assign")).toBeVisible();

    await page.getByTestId("open-assign-user").click();
    await expect(page.getByTestId("assign-tenant-select")).toBeVisible();
    await page.getByRole("button", { name: "Cancel" }).click();

    await page.goto("/settings");
    await expect(page.getByTestId("settings-tenant-title")).toHaveText(
      "Tenant",
    );
    await expect(page.getByTestId("settings-tenant-id")).toHaveText(
      SOLE_TENANT,
    );
    await expect(
      page.getByRole("button", { name: "Switch tenant" }),
    ).toBeVisible();
  });
});
