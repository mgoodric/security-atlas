// #2397 — the axe gate at one narrow viewport.
//
// The route sweep runs axe at 1200px only. At phone width the layout
// changes shape (list tables collapse to cards, the sidebar becomes a
// drawer), so a few key routes are swept again at 390px, plus the open
// drawer. Kept to four routes on purpose: every route at every width
// would multiply the sweep's CI time for little extra signal.

import { expectNoSeriousAxe, seriousAxeViolations } from "./axe-gate";
import { expect, test } from "./fixtures";

const NARROW = { width: 390, height: 844 };

const ROUTES = ["/dashboard", "/controls", "/risks", "/activity"] as const;

test.describe("axe at 390px (#2397)", () => {
  for (const route of ROUTES) {
    test(route, async ({ authedPage: page }) => {
      await page.setViewportSize(NARROW);
      await page.goto(route);
      await expect(page.locator("main#main-content")).toBeVisible();
      await page.waitForLoadState("networkidle");
      expectNoSeriousAxe(await seriousAxeViolations(page), `${route} @ 390px`);
    });
  }

  test("/dashboard with the mobile drawer open", async ({
    authedPage: page,
  }) => {
    await page.setViewportSize(NARROW);
    await page.goto("/dashboard");
    await page.getByTestId("mobile-sidebar-trigger").click();
    await expect(page.getByTestId("mobile-sidebar-drawer")).toBeVisible();
    expectNoSeriousAxe(
      await seriousAxeViolations(page),
      "/dashboard @ 390px, drawer open",
    );
  });
});
