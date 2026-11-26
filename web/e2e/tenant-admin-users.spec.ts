// #2503 — an organisation admin manages its own organisation's users,
// end to end against the real platform (the /admin layout and the sidebar
// read GET /v1/me server-side, so a browser route-mock cannot reach them).
//
//   1. A tenant admin (a user_roles 'admin' row, no super_admin) sees the
//      sidebar Admin entry, and the admin section lists SSO, Users, API
//      keys, Features and Audit and none of the platform pages.
//   2. In Admin › Users it sees its own tenant's members and grants one of
//      them a role; the platform writes it.
//   3. Its SSO page opens (#2503), and a platform page (/admin/tenants)
//      shows the super-admin panel, not the page.
//
// The tenant and members are the spec's own
// (fixtures/e2e/tenant-admin-users.sql), reset each run.
//
// Hard rule (P0-A9 / GitGuardian): every id is a neutral test string.

import type { Page } from "@playwright/test";

import { expect, test } from "./fixtures";
import { seedFromFixture } from "./seed";

const TENANT = "00000000-0000-0000-0000-000000002503";
const ADMIN = "44444444-4444-4444-4444-444444442503";
const MEMBER = "44444444-4444-4444-4444-444444442513";

function atlasBaseURL(): string {
  return process.env.ATLAS_HTTP_URL ?? "http://localhost:8080";
}

async function mintTenantAdminJWT(): Promise<string> {
  const url = `${atlasBaseURL()}/v1/test/issue-jwt`;
  const res = await fetch(url, {
    method: "POST",
    headers: { "Content-Type": "application/json" },
    body: JSON.stringify({
      tenant_id: TENANT,
      user_id: `user:${ADMIN}`,
      roles: ["admin"],
      super_admin: false,
    }),
  });
  if (!res.ok) {
    throw new Error(
      `#2503: ${url} returned ${res.status}: ${await res.text()}`,
    );
  }
  const parsed = (await res.json()) as { token?: string };
  if (!parsed.token) {
    throw new Error(`#2503: ${url} returned 200 but no token field`);
  }
  return parsed.token;
}

async function signInAs(page: Page, bearer: string): Promise<void> {
  const url = new URL(process.env.PLATFORM_BASE_URL ?? "http://localhost:3000");
  await page.context().clearCookies();
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

test.describe("#2503 — an organisation admin manages its own users", () => {
  test.describe.configure({ mode: "serial" });

  test.beforeAll(() => {
    seedFromFixture("tenant-admin-users");
  });

  test("the admin section shows its tenant pages, no platform pages", async ({
    page,
  }) => {
    await signInAs(page, await mintTenantAdminJWT());
    await page.goto("/dashboard");
    await expect(
      page.getByTestId("sidebar-desktop").locator('a[href="/admin"]'),
    ).toBeVisible();

    await page.goto("/admin");
    const sections = page.getByRole("navigation", { name: "Admin sections" });
    for (const name of ["SSO", "Users", "API keys", "Features", "Audit"]) {
      await expect(sections.getByRole("link", { name })).toBeVisible();
    }
    for (const name of ["Tenants", "Super admins"]) {
      await expect(sections.getByRole("link", { name })).toHaveCount(0);
    }
  });

  test("grants a member of its own tenant a role", async ({ page }) => {
    await signInAs(page, await mintTenantAdminJWT());
    await page.goto("/admin/users");

    const row = page.getByTestId(`user-row-${MEMBER}`);
    await expect(row).toBeVisible();
    await expect(row.getByTestId("role-badge-viewer")).toBeVisible();
    await expect(page.getByTestId("open-self-assign")).toHaveCount(0);

    await page.getByTestId("open-assign-user").click();
    const dialog = page.getByTestId("assign-user-dialog");
    await expect(dialog).toBeVisible();
    await dialog.getByTestId("assign-user-select").selectOption(MEMBER);
    await expect(dialog.getByTestId("assign-tenant-select")).toHaveCount(0);
    await dialog.getByTestId("assign-role-auditor").click();
    await dialog.getByTestId("assign-user-submit").click();
    await expect(dialog).toBeHidden();

    await expect(row.getByTestId("role-badge-auditor")).toBeVisible();
    await expect(row.getByTestId("role-badge-viewer")).toBeVisible();
  });

  test("its SSO page opens; a platform page shows the super-admin panel", async ({
    page,
  }) => {
    await signInAs(page, await mintTenantAdminJWT());
    await page.goto("/admin/sso");
    await expect(page.getByTestId("sso-config-form")).toBeVisible();
    await expect(page.getByTestId("admin-platform-forbidden")).toHaveCount(0);

    await page.goto("/admin/tenants");
    await expect(page.getByTestId("admin-platform-forbidden")).toBeVisible();
  });

  // #2760: the SSO page's delete confirms with the mapping and role counts
  // GET reports, and fires DELETE only from the confirm. The browser-side
  // /api/admin/sso calls are mocked; the page shell is the real platform.
  test("its SSO delete confirms with the mapping and role counts", async ({
    page,
  }) => {
    let deletes = 0;
    let deleted = false;
    await page.route("**/api/admin/sso", async (route) => {
      const method = route.request().method();
      if (method === "DELETE") {
        deletes++;
        deleted = true;
        await route.fulfill({
          status: 200,
          contentType: "application/json",
          body: JSON.stringify({
            config_id: "cfg-2760",
            mapping_count: 2,
            revoked_role_count: 5,
            affected_user_count: 3,
          }),
        });
        return;
      }
      await route.fulfill({
        status: 200,
        contentType: "application/json",
        body: JSON.stringify({
          config: deleted
            ? null
            : {
                id: "cfg-2760",
                name: "primary",
                issuer_url: "https://idp.example",
                client_id: "cid",
                redirect_url: "https://app.example/cb",
                allowed_email_domains: [],
                trust_unverified_email_for_link: false,
                created_at: "2026-11-20T00:00:00Z",
                updated_at: "2026-11-20T00:00:00Z",
                mapping_count: 2,
                derived_role_count: 5,
              },
        }),
      });
    });

    await signInAs(page, await mintTenantAdminJWT());
    await page.goto("/admin/sso");
    await page.getByTestId("sso-delete-trigger").click();
    const dialog = page.getByTestId("sso-delete-dialog");
    await expect(dialog).toBeVisible();
    await expect(dialog.getByTestId("sso-delete-mapping-count")).toHaveText(
      /2 group-role mappings/,
    );
    await expect(dialog.getByTestId("sso-delete-role-count")).toHaveText(
      /5 group-derived roles/,
    );
    expect(deletes).toBe(0);

    await dialog.getByTestId("sso-delete-confirm").click();
    await expect(dialog).toBeHidden();
    expect(deletes).toBe(1);
    await expect(page.getByTestId("sso-delete-card")).toHaveCount(0);
    await expect(page.getByTestId("sso-delete-success")).toHaveText(
      /Removed 2 mappings and revoked 5 roles from 3 users/,
    );
  });
});
