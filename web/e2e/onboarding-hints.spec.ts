// #2145 — first-visit hints, remembered per user.
//
// On a module's first visit, "New to Controls? See how this works" sits at
// the top of the page; the link opens the help panel. Dismissing it writes
// the user's server-side preferences, so it stays gone after a reload and
// in a fresh browser context signed in as the same user. Settings' "Show
// onboarding hints again" brings it back.
//
// Dismissals are per user and the rest of the suite runs as the shared demo
// user, so this spec signs in as a user of its own (fixtures/e2e/
// onboarding-hints.sql clears its dismissals each run) and runs serially
// because its tests share that user's stored state.

import AxeBuilder from "@axe-core/playwright";
import {
  test as base,
  expect,
  type BrowserContext,
  type Page,
} from "@playwright/test";

import { ATLAS_JWT_COOKIE } from "../lib/auth";

import { issueTestJWT } from "./global-setup";
import { HINTS_USER_ID, seedFromFixture } from "./seed";

let bearer = "";

async function signIn(context: BrowserContext, baseURL: string | undefined) {
  const url = new URL(baseURL ?? "http://localhost:3000");
  await context.addCookies([
    {
      name: ATLAS_JWT_COOKIE,
      value: bearer,
      domain: url.hostname,
      path: "/",
      httpOnly: true,
      secure: url.protocol === "https:",
      sameSite: "Lax",
    },
  ]);
}

const test = base.extend<{ hintsPage: Page }>({
  hintsPage: async ({ page, baseURL }, use) => {
    await signIn(page.context(), baseURL);
    await use(page);
  },
});

test.describe.configure({ mode: "serial" });

test.beforeAll(async () => {
  seedFromFixture("onboarding-hints");
  bearer = await issueTestJWT(HINTS_USER_ID);
});

test.describe("First-visit hints (#2145)", () => {
  test("shows on the first visit; dismissed, it stays gone after a reload and in a new browser", async ({
    hintsPage: page,
    browser,
    baseURL,
  }) => {
    await page.goto("/controls");
    const hint = page.getByTestId("onboarding-hint");
    await expect(hint).toBeVisible({ timeout: 30_000 });
    await expect(hint).toContainText("New to Controls? See how this works");

    const axe = await new AxeBuilder({ page })
      .include('[data-testid="onboarding-hint"]')
      .analyze();
    expect(
      axe.violations.filter(
        (v) => v.impact === "serious" || v.impact === "critical",
      ),
    ).toEqual([]);

    // The link opens the module's "How this works" panel.
    await page.getByTestId("onboarding-hint-open").click();
    await expect(page.getByTestId("help-panel")).toBeVisible();
    await page.keyboard.press("Escape");
    await expect(page.getByTestId("help-panel")).toHaveCount(0);

    const saved = page.waitForResponse(
      (r) =>
        r.url().endsWith("/api/me/preferences") &&
        r.request().method() === "PATCH",
    );
    await page
      .getByRole("button", { name: "Dismiss the Controls hint" })
      .click();
    expect((await saved).status()).toBe(200);
    await expect(hint).toHaveCount(0);

    // Reload: the dismissal comes back from the server.
    const read = page.waitForResponse(
      (r) =>
        r.url().endsWith("/api/me/preferences") &&
        r.request().method() === "GET",
    );
    await page.reload();
    expect((await read).status()).toBe(200);
    await expect(page.getByTestId("help-button")).toBeVisible();
    await expect(page.getByTestId("onboarding-hint")).toHaveCount(0);

    // A new browser context, same user: no cookies or storage carried over.
    const other = await browser.newContext({ baseURL });
    try {
      await signIn(other, baseURL);
      const fresh = await other.newPage();
      const freshRead = fresh.waitForResponse(
        (r) =>
          r.url().endsWith("/api/me/preferences") &&
          r.request().method() === "GET",
      );
      await fresh.goto("/controls");
      expect((await freshRead).status()).toBe(200);
      await expect(fresh.getByTestId("help-button")).toBeVisible();
      await expect(fresh.getByTestId("onboarding-hint")).toHaveCount(0);
    } finally {
      await other.close();
    }
  });

  test('"Show onboarding hints again" brings the hint back', async ({
    hintsPage: page,
  }) => {
    // Still dismissed from the previous test.
    await page.goto("/controls");
    await expect(page.getByTestId("help-button")).toBeVisible();
    await expect(page.getByTestId("onboarding-hint")).toHaveCount(0);

    await page.goto("/settings");
    const reset = page.getByRole("button", {
      name: "Show onboarding hints again",
    });
    await expect(reset).toBeVisible({ timeout: 30_000 });
    const saved = page.waitForResponse(
      (r) =>
        r.url().endsWith("/api/me/preferences") &&
        r.request().method() === "PATCH",
    );
    await reset.click();
    expect((await saved).status()).toBe(200);

    await page.goto("/controls");
    await expect(page.getByTestId("onboarding-hint")).toBeVisible({
      timeout: 30_000,
    });
  });
});
