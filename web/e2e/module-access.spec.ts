// #2172 — module access for each member, end to end against the real
// backend (the sidebar and page gates read GET /v1/me/modules
// server-side, so a browser route-mock cannot reach them).
//
//   1. An admin limits a member to Policies and Questionnaires in
//      Admin › Users. The member's sidebar then lists only core modules
//      plus those two, and opening Risk shows the no-access message.
//   2. Activity shows the access change and a module switch change.
//
// The tenant and member are the spec's own (fixtures/e2e/module-access.sql),
// reset to "all modules" each run. The admin is a super-admin of that
// tenant, whose Admin › Users list is the cross-tenant one; the list is
// read through the real BFF with a raised limit and narrowed to the spec's
// tenant, so other specs' users cannot push the member off the page.

import type { Page } from "@playwright/test";

import { expect, test } from "./fixtures";
import { seedFromFixture } from "./seed";

const TENANT = "00000000-0000-0000-0000-000000002172";
const MEMBER = "44444444-4444-4444-4444-444444442172";

// Entries owned by an optional module other than Policies and
// Questionnaires: none may be listed for the limited member.
const HIDDEN = [
  "Risk register",
  "Risk hierarchy",
  "Risk taxonomy",
  "Action plans",
  "Vendors",
  "Personnel security",
  "Exceptions",
  "Board packs",
];
// Core entries, plus the two granted modules.
const SHOWN = [
  "Dashboard",
  "Activity",
  "Controls",
  "Evidence",
  "Audit periods",
  "Framework scopes",
  "Policies",
  "Questionnaires",
  "Settings",
];

function atlasBaseURL(): string {
  return process.env.ATLAS_HTTP_URL ?? "http://localhost:8080";
}

async function mintJWT(member: boolean): Promise<string> {
  const url = `${atlasBaseURL()}/v1/test/issue-jwt`;
  const res = await fetch(url, {
    method: "POST",
    headers: { "Content-Type": "application/json" },
    body: JSON.stringify(
      member
        ? {
            tenant_id: TENANT,
            user_id: MEMBER,
            roles: ["grc_engineer"],
            super_admin: false,
          }
        : { tenant_id: TENANT, roles: ["admin"], super_admin: true },
    ),
  });
  if (!res.ok) {
    throw new Error(
      `#2172: ${url} returned ${res.status}: ${await res.text()}`,
    );
  }
  const parsed = (await res.json()) as { token?: string };
  if (!parsed.token) {
    throw new Error(`#2172: ${url} returned 200 but no token field`);
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

// onlySpecTenant serves the real users list, raised to the API's page cap
// and narrowed to the spec's tenant.
async function onlySpecTenant(page: Page): Promise<void> {
  await page.route("**/api/admin/users", async (route) => {
    if (route.request().method() !== "GET") {
      return route.continue();
    }
    const url = new URL(route.request().url());
    url.searchParams.set("limit", "200");
    const response = await route.fetch({ url: url.toString() });
    const body = (await response.json()) as {
      items?: { id: string; tenant_id?: string }[];
    };
    body.items = (body.items ?? []).filter(
      (u) => !u.tenant_id || u.tenant_id === TENANT,
    );
    await route.fulfill({ response, json: body });
  });
}

// api calls the platform directly as bearer.
async function api(
  bearer: string,
  method: string,
  path: string,
  body: unknown,
): Promise<void> {
  const res = await fetch(`${atlasBaseURL()}${path}`, {
    method,
    headers: {
      Authorization: `Bearer ${bearer}`,
      "Content-Type": "application/json",
    },
    body: JSON.stringify(body),
  });
  if (!res.ok) {
    throw new Error(
      `#2172: ${method} ${path}: ${res.status} ${await res.text()}`,
    );
  }
}

test.describe("#2172 — module access for each member", () => {
  test.describe.configure({ mode: "serial" });

  // onlySpecTenant's handler awaits route.fetch; one still in flight when
  // the test ends fails the run outside any test (#2589).
  test.afterEach(async ({ page }) => {
    await page.unrouteAll({ behavior: "ignoreErrors" });
  });

  test.beforeAll(() => {
    seedFromFixture("module-access");
  });

  test("an admin limits a member to Policies and Questionnaires", async ({
    page,
  }) => {
    await signInAs(page, await mintJWT(false));
    await onlySpecTenant(page);
    await page.goto("/admin/users");

    const cell = page.getByTestId(`module-access-${MEMBER}`);
    await expect(cell).toHaveText("All modules");
    await page.getByTestId(`module-access-edit-${MEMBER}`).click();

    const dialog = page.getByTestId("module-access-dialog");
    await expect(dialog).toBeVisible();
    await dialog.getByTestId("module-access-all").click();
    await dialog.getByTestId("module-access-module-policies").click();
    await dialog.getByTestId("module-access-module-questionnaires").click();
    await dialog.getByTestId("module-access-save").click();
    await expect(dialog).toBeHidden();
    await expect(cell).toHaveText("Policies, Questionnaires");

    // The member's sidebar: core modules plus the two granted.
    await signInAs(page, await mintJWT(true));
    await page.goto("/dashboard");
    const sidebar = page.getByTestId("sidebar-desktop");
    await expect(sidebar).toBeVisible();
    const labels = await sidebar
      .locator("a > span:first-child")
      .allInnerTexts();
    for (const label of SHOWN) {
      expect(labels, `${label} is listed`).toContain(label);
    }
    for (const label of HIDDEN) {
      expect(labels, `${label} is not listed`).not.toContain(label);
    }

    // Opening Risk directly shows the no-access message, not the page.
    await page.goto("/risks");
    await expect(page.getByTestId("feature-no-access-state")).toHaveText(
      "You don't have access to Risk. Ask an administrator.",
    );
    await expect(page.getByTestId("feature-off-state")).toHaveCount(0);
  });

  test("Activity shows the access change and a module switch change", async ({
    page,
  }) => {
    const admin = await mintJWT(false);
    await api(admin, "PUT", `/v1/admin/users/${MEMBER}/module-access`, {
      modules: ["policies"],
    });
    await api(admin, "PATCH", "/v1/admin/features/exceptions.enabled", {
      enabled: false,
      reason: "#2172 e2e",
    });
    await api(admin, "PATCH", "/v1/admin/features/exceptions.enabled", {
      enabled: true,
      reason: "#2172 e2e",
    });

    await signInAs(page, admin);
    // An explicit window around now: the page's default `to` is today's
    // 00:00 UTC, which the server reads as exclusive, so events from today
    // fall outside the default view.
    const day = 24 * 60 * 60 * 1000;
    const from = new Date(Date.now() - day).toISOString();
    const to = new Date(Date.now() + day).toISOString();
    await page.goto(
      `/activity?from=${encodeURIComponent(from)}&to=${encodeURIComponent(to)}`,
    );
    const access = page.locator(
      '[data-testid="activity-row"][data-kind="module_access"]',
    );
    await expect(access.first()).toContainText("changed the module access of");
    const flags = page.locator(
      '[data-testid="activity-row"][data-kind="feature_flag"]',
    );
    await expect(flags.filter({ hasText: "disabled" }).first()).toBeVisible();
    await expect(flags.filter({ hasText: "enabled" }).first()).toBeVisible();
  });
});
