// #2494 — a signed-in member with no role in the current tenant sees one
// "You don't have a role in <tenant>" page, not a dashboard of 403s, end
// to end against the real backend (the (authed) layout reads
// `has_tenant_role` from GET /v1/me server-side, so a browser route-mock
// cannot reach it).
//
//   1. The member's token carries an empty role claim (#2401) and lists a
//      second tenant. /dashboard shows the no-role page with the tenant
//      switcher and Sign out, no sidebar, and no request answers 403.
//   2. An admin grants the member a role through the admin API. A reload,
//      on the same token, shows the dashboard (live roles, #2442).
//
// The tenant and member are the spec's own (fixtures/e2e/no-tenant-role.sql),
// whose roles are cleared each run.

import type { Page } from "@playwright/test";

import { expect, test } from "./fixtures";
import { seedFromFixture } from "./seed";

const TENANT = "00000000-0000-0000-0000-000000002494";
const OTHER_TENANT = "00000000-0000-0000-0000-000000002495";
const MEMBER = "44444444-4444-4444-4444-444444442494";

function atlasBaseURL(): string {
  return process.env.ATLAS_HTTP_URL ?? "http://localhost:8080";
}

async function mintJWT(body: Record<string, unknown>): Promise<string> {
  const url = `${atlasBaseURL()}/v1/test/issue-jwt`;
  const res = await fetch(url, {
    method: "POST",
    headers: { "Content-Type": "application/json" },
    body: JSON.stringify(body),
  });
  if (!res.ok) {
    throw new Error(
      `#2494: ${url} returned ${res.status}: ${await res.text()}`,
    );
  }
  const parsed = (await res.json()) as { token?: string };
  if (!parsed.token) {
    throw new Error(`#2494: ${url} returned 200 but no token field`);
  }
  return parsed.token;
}

// The member's token: an empty role claim in TENANT, as every login path
// mints for a user with no user_roles row there.
function memberJWT(): Promise<string> {
  return mintJWT({
    tenant_id: TENANT,
    user_id: MEMBER,
    super_admin: false,
    available_tenants: [TENANT, OTHER_TENANT],
    roles_by_tenant: { [TENANT]: [], [OTHER_TENANT]: ["viewer"] },
  });
}

function adminJWT(): Promise<string> {
  return mintJWT({ tenant_id: TENANT, roles: ["admin"], super_admin: true });
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

async function setRoles(admin: string, roles: string[]): Promise<void> {
  const path = `/v1/admin/users/${MEMBER}/roles`;
  const res = await fetch(`${atlasBaseURL()}${path}`, {
    method: "PATCH",
    headers: {
      Authorization: `Bearer ${admin}`,
      "Content-Type": "application/json",
    },
    body: JSON.stringify({ roles }),
  });
  if (!res.ok) {
    throw new Error(`#2494: PATCH ${path}: ${res.status} ${await res.text()}`);
  }
}

test.describe("#2494 — a member with no role in the tenant", () => {
  test.describe.configure({ mode: "serial" });

  test.beforeAll(() => {
    seedFromFixture("no-tenant-role");
  });

  test("sees the no-role page, then the dashboard once granted a role", async ({
    page,
  }) => {
    const forbidden: string[] = [];
    page.on("response", (res) => {
      if (res.status() === 403) forbidden.push(res.url());
    });

    await signInAs(page, await memberJWT());
    await page.goto("/dashboard");

    const panel = page.getByTestId("no-tenant-role");
    await expect(panel).toBeVisible();
    await expect(
      page.getByRole("heading", {
        name: "You don't have a role in No role (e2e)",
      }),
    ).toBeVisible();
    await expect(page.getByRole("button", { name: "Sign out" })).toBeVisible();
    await expect(
      page.getByRole("button", { name: "Switch tenant" }),
    ).toBeVisible();
    await expect(page.getByTestId("sidebar-desktop")).toHaveCount(0);
    expect(page.url()).toContain("/dashboard");
    // Let the switcher's directory read settle before counting.
    await page.waitForLoadState("networkidle");
    expect(forbidden, "no request answers 403 on the no-role page").toEqual([]);

    // Grant a role; the next load shows the app, with no re-login.
    await setRoles(await adminJWT(), ["viewer"]);
    await page.reload();
    await expect(page.getByTestId("sidebar-desktop")).toBeVisible();
    await expect(page.getByTestId("no-tenant-role")).toHaveCount(0);
  });

  test.afterAll(async () => {
    // Leave the member role-less for the next run (the fixture clears it
    // too).
    await setRoles(await adminJWT(), []);
  });
});
