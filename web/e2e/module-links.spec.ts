// #2174 — a record's link sections follow the caller's effective
// modules, end to end against the real backend (the modules reach the
// page through GET /v1/me/modules, read server-side in the authed layout,
// so a browser route-mock cannot reach them).
//
// On a control linked to a policy, an admin sees the linked-policies
// section. A member without Policies access does not: no section, no
// count. #2140 moved the link sections into the control page's Linked
// section and added exceptions there: a member without the Risk and
// Exceptions modules sees no action plans part and no exceptions part.
//
// The tenant, control, policy and members are the spec's own
// (fixtures/e2e/module-links.sql); the members' access is reset each run.

import type { Page } from "@playwright/test";

import { expect, test } from "./fixtures";
import { seedFromFixture } from "./seed";

const TENANT = "00000000-0000-0000-0000-000000002174";
// Every optional module except Policies.
const MEMBER = "44444444-4444-4444-4444-444444442174";
// Every optional module except Risk and Exceptions (#2140).
const MEMBER_B = "44444444-4444-4444-4444-444444442140";
const CONTROL = "88888888-8888-8888-8888-888888882174";

function atlasBaseURL(): string {
  return process.env.ATLAS_HTTP_URL ?? "http://localhost:8080";
}

async function mintJWT(member?: string): Promise<string> {
  const url = `${atlasBaseURL()}/v1/test/issue-jwt`;
  const res = await fetch(url, {
    method: "POST",
    headers: { "Content-Type": "application/json" },
    body: JSON.stringify(
      member
        ? {
            tenant_id: TENANT,
            user_id: member,
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

  test("an admin sees the control's linked policies, plans and exceptions", async ({
    page,
  }) => {
    await signInAs(page, await mintJWT());
    await page.goto(`/controls/${CONTROL}`);

    const section = page.getByTestId("policies-section");
    await expect(section).toBeVisible({ timeout: 30_000 });
    await expect(section).toContainText("E2E 2174 linked policy");
    await expect(page.getByTestId("linked-action-plans")).toBeVisible();
    await expect(page.getByTestId("exceptions-section")).toBeVisible();
  });

  test("a member without Policies access sees no linked-policies section", async ({
    page,
  }) => {
    await signInAs(page, await mintJWT(MEMBER));
    await page.goto(`/controls/${CONTROL}`);

    // The page itself renders (Controls is core), and so does a link
    // section into a module the member can see (Risk).
    await expect(page.getByTestId("control-tabs")).toBeVisible({
      timeout: 30_000,
    });
    await expect(page.getByTestId("risks-section")).toBeVisible();

    await expect(page.getByTestId("policies-section")).toHaveCount(0);
    await expect(page.getByText("E2E 2174 linked policy")).toHaveCount(0);

    // A retired ?tab=policies lands on the overview, still without it.
    await page.goto(`/controls/${CONTROL}?tab=policies`);
    await expect(page.getByTestId("control-tab-panel-overview")).toBeVisible({
      timeout: 30_000,
    });
    await expect(page.getByTestId("policies-section")).toHaveCount(0);
  });

  test("a member without the Risk and Exceptions modules sees no plans or exceptions (#2140)", async ({
    page,
  }) => {
    await signInAs(page, await mintJWT(MEMBER_B));
    await page.goto(`/controls/${CONTROL}`);

    const linked = page.locator(
      '[data-testid="control-detail-section"][data-section="linked"]',
    );
    await expect(linked).toBeVisible({ timeout: 30_000 });
    await expect(page.getByTestId("policies-section")).toBeVisible();

    await expect(page.getByTestId("exceptions-section")).toHaveCount(0);
    await expect(page.getByTestId("linked-action-plans")).toHaveCount(0);
    await expect(page.getByTestId("risks-section")).toHaveCount(0);
    await expect(page.getByTestId("control-strip-action-plan")).toHaveCount(0);
  });

  // #2141: the controls list's bulk actions follow the same modules. The
  // member is an approver, so assign owner and evaluate now are offered;
  // link to a plan (Risk) and request exception (Exceptions) are not.
  test("a member without the Risk and Exceptions modules gets no link-to-plan or request-exception bulk action (#2141)", async ({
    page,
  }) => {
    await signInAs(page, await mintJWT(MEMBER_B));
    await page.goto("/controls");

    const row = page
      .getByTestId("list-table-row")
      .filter({ hasText: "E2E 2174 control" });
    await expect(row).toBeVisible({ timeout: 30_000 });
    await row.getByTestId("controls-row-select").click();

    await expect(page.getByTestId("controls-selection-bar")).toBeVisible();
    await expect(page.getByTestId("controls-bulk-assign-owner")).toBeVisible();
    await expect(page.getByTestId("controls-bulk-evaluate")).toBeVisible();
    await expect(page.getByTestId("controls-bulk-link-plan")).toHaveCount(0);
    await expect(
      page.getByTestId("controls-bulk-request-exception"),
    ).toHaveCount(0);
  });

  test("an admin gets every bulk action on the controls list (#2141)", async ({
    page,
  }) => {
    await signInAs(page, await mintJWT());
    await page.goto("/controls");

    const row = page
      .getByTestId("list-table-row")
      .filter({ hasText: "E2E 2174 control" });
    await expect(row).toBeVisible({ timeout: 30_000 });
    await row.getByTestId("controls-row-select").click();

    await expect(page.getByTestId("controls-bulk-link-plan")).toBeVisible();
    const request = page.getByTestId("controls-bulk-request-exception");
    await expect(request).toHaveAttribute(
      "href",
      `/exceptions/new?control_id=${CONTROL}`,
    );
  });
});
