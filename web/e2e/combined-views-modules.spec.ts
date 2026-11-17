// #2173 — combined views show only modules the viewer can see, end to end
// against the real backend (the dashboard and the calendar read
// GET /v1/calendar, which filters by module on the server, so a browser
// route-mock cannot reach it).
//
// The spec's tenant (fixtures/e2e/combined-views-modules.sql) has a
// policy review, a risk review and a vendor review due in five days, and
// one grc_engineer member whose module access is Policies only. The
// tenant's admin sees all three on the dashboard's Upcoming list and on the
// Calendar; the member sees only the policy review.

import type { Page } from "@playwright/test";

import { expect, test } from "./fixtures";
import { seedFromFixture } from "./seed";

const TENANT = "00000000-0000-0000-0000-000000002173";
const MEMBER = "44444444-4444-4444-4444-444444442173";

const POLICY = "E2E 2173 policy";
const HIDDEN = ["E2E 2173 risk", "E2E 2173 vendor"];
// The Upcoming badges a member limited to Policies may see: core types
// and the Policies module's two.
const ALLOWED_BADGES = [
  "Audit period",
  "Control review",
  "Policy review",
  "Policy ack",
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
      `#2173: ${url} returned ${res.status}: ${await res.text()}`,
    );
  }
  const parsed = (await res.json()) as { token?: string };
  if (!parsed.token) {
    throw new Error(`#2173: ${url} returned 200 but no token field`);
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

async function upcomingTitles(page: Page): Promise<string[]> {
  await page.goto("/dashboard");
  await expect(page.getByTestId("upcoming-panel")).toBeVisible({
    timeout: 30_000,
  });
  return page.getByTestId("upcoming-row-title").allInnerTexts();
}

test.describe("#2173 — combined views follow module access", () => {
  test.describe.configure({ mode: "serial" });

  test.beforeAll(() => {
    seedFromFixture("combined-views-modules");
  });

  test("the admin sees the policy, risk and vendor reviews", async ({
    page,
  }) => {
    await signInAs(page, await mintJWT(false));
    const titles = (await upcomingTitles(page)).join("\n");
    for (const title of [POLICY, ...HIDDEN]) {
      expect(titles, `Upcoming lists ${title}`).toContain(title);
    }

    await page.goto("/calendar");
    for (const title of [POLICY, ...HIDDEN]) {
      await expect(page.locator("li", { hasText: title }).first()).toBeVisible({
        timeout: 30_000,
      });
    }
  });

  test("a member limited to Policies sees only core and policy items", async ({
    page,
  }) => {
    await signInAs(page, await mintJWT(true));
    const titles = (await upcomingTitles(page)).join("\n");
    expect(titles, "Upcoming lists the policy review").toContain(POLICY);
    for (const title of HIDDEN) {
      expect(titles, `Upcoming leaves out ${title}`).not.toContain(title);
    }
    const badges = await page
      .getByTestId("upcoming-row-category")
      .allInnerTexts();
    for (const badge of badges) {
      expect(ALLOWED_BADGES, `Upcoming badge ${badge}`).toContain(badge.trim());
    }

    await page.goto("/calendar");
    await expect(page.locator("li", { hasText: POLICY }).first()).toBeVisible({
      timeout: 30_000,
    });
    for (const title of HIDDEN) {
      await expect(page.locator("li", { hasText: title })).toHaveCount(0);
    }
  });
});
