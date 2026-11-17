// #2174 — a record's link sections follow the caller's effective
// modules, end to end against the real backend (the modules reach the
// page through GET /v1/me/modules, read server-side in the authed layout,
// so a browser route-mock cannot reach them).
//
// On a control linked to a policy, an admin sees the linked-policies
// section and the Policies tab. A member without Policies access sees
// neither: no section, no tab, no count.
//
// The tenant, control, policy and member are the spec's own
// (fixtures/e2e/module-links.sql); the member's access is reset each run.

import type { Page } from "@playwright/test";

import { expect, test } from "./fixtures";
import { seedFromFixture } from "./seed";

const TENANT = "00000000-0000-0000-0000-000000002174";
const MEMBER = "44444444-4444-4444-4444-444444442174";
const CONTROL = "88888888-8888-8888-8888-888888882174";

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
      `#2174: ${url} returned ${res.status}: ${await res.text()}`,
    );
  }
  const parsed = (await res.json()) as { token?: string };
  if (!parsed.token) {
    throw new Error(`#2174: ${url} returned 200 but no token field`);
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

test.describe("#2174 — link sections follow module access", () => {
  test.beforeAll(() => {
    seedFromFixture("module-links");
  });

  test("an admin sees the control's linked policies", async ({ page }) => {
    await signInAs(page, await mintJWT(false));
    await page.goto(`/controls/${CONTROL}`);

    const section = page.getByTestId("policies-section");
    await expect(section).toBeVisible();
    await expect(section).toContainText("E2E 2174 linked policy");
    await expect(page.getByTestId("control-tab-policies")).toBeVisible();
  });

  test("a member without Policies access sees no linked-policies section", async ({
    page,
  }) => {
    await signInAs(page, await mintJWT(true));
    await page.goto(`/controls/${CONTROL}`);

    // The page itself renders (Controls is core), and so does a link
    // section into a module the member can see (Risk).
    await expect(page.getByTestId("control-tabs")).toBeVisible();
    await expect(page.getByTestId("risks-section")).toBeVisible();
    await expect(page.getByTestId("control-tab-risks")).toBeVisible();

    await expect(page.getByTestId("policies-section")).toHaveCount(0);
    await expect(page.getByTestId("control-tab-policies")).toHaveCount(0);
    await expect(page.getByText("E2E 2174 linked policy")).toHaveCount(0);

    // A direct ?tab=policies falls back to the overview.
    await page.goto(`/controls/${CONTROL}?tab=policies`);
    await expect(page.getByTestId("control-tab-panel-overview")).toBeVisible();
    await expect(page.getByTestId("policies-section")).toHaveCount(0);
  });
});
