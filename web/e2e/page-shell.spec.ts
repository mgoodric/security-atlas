// #2080 — PageShell + AppShell contracts that the route sweep
// (`route-sweep.spec.ts`) checks only in general.
//
//   1. The risk register's table (~1,822px of columns) scrolls inside its
//      own container; the page and the main column do not scroll
//      sideways at 1316px.
//   2. The audit log, the auditor workspace and the 404 page render
//      inside the app shell (top bar + sidebar + main column). Before
//      #2080 the first and last rendered bare and the workspace had only
//      the top bar.
//   3. The version tag appears only in the sidebar footer, not in the
//      top bar or as a fixed overlay.

import { expect, test } from "./fixtures";
import { seedFromFixture } from "./seed";

test.describe("PageShell (#2080)", () => {
  // The register only renders its table when it has rows. The dashboard
  // fixture (idempotent, ON CONFLICT DO NOTHING) seeds risks for the
  // demo tenant, so this spec does not depend on another spec's writes.
  test.beforeAll(() => {
    seedFromFixture("dashboard");
  });

  test("the risk register's table scrolls in its container, not the page, at 1316px", async ({
    authedPage: page,
  }) => {
    await page.setViewportSize({ width: 1316, height: 900 });
    await page.goto("/risks");

    const container = page
      .getByTestId("list-table-wrap")
      .locator('[data-slot="table-container"]');
    await expect(container).toBeVisible({ timeout: 30_000 });

    // The table is wider than its container, so the container scrolls...
    await expect
      .poll(() => container.evaluate((el) => el.scrollWidth - el.clientWidth))
      .toBeGreaterThan(0);
    const style = await container.evaluate(
      (el) => getComputedStyle(el).overflowX,
    );
    expect(["auto", "scroll"]).toContain(style);

    // ...and neither the page nor the main column does.
    const overflow = await page.evaluate(() => {
      const doc = document.documentElement;
      const main = document.getElementById("main-content")!;
      return {
        page: doc.scrollWidth - doc.clientWidth,
        main: main.scrollWidth - main.clientWidth,
      };
    });
    expect(overflow.page).toBeLessThanOrEqual(1);
    expect(overflow.main).toBeLessThanOrEqual(1);
  });

  for (const { route, body } of [
    { route: "/audit-log", body: "heading" },
    { route: "/audit", body: "workspace" },
    { route: "/route-sweep-no-such-page", body: "not-found" },
  ] as const) {
    test(`${route} renders inside the app shell`, async ({
      authedPage: page,
    }) => {
      await page.goto(route);

      await expect(page.getByTestId("app-topbar")).toBeVisible();
      await expect(page.getByTestId("sidebar-desktop")).toBeVisible();
      const main = page.locator("main#main-content");
      await expect(main).toBeVisible();
      await expect(page.locator("main")).toHaveCount(1);

      if (body === "heading") {
        await expect(
          main.getByRole("heading", { level: 1, name: "Audit log" }),
        ).toBeVisible();
      } else if (body === "workspace") {
        // Either the assigned period's workspace or the no-period state,
        // depending on the harness; both render in the main column.
        await expect(
          main
            .getByTestId("audit-no-control-selected")
            .or(main.getByTestId("audit-no-period"))
            .first(),
        ).toBeVisible({ timeout: 30_000 });
      } else {
        await expect(main.getByTestId("authed-not-found")).toBeVisible();
        await expect(page).toHaveTitle("Page not found · security-atlas");
      }
    });
  }

  test("the version tag appears only in the sidebar footer", async ({
    authedPage: page,
  }) => {
    await page.goto("/dashboard");

    const sidebar = page.getByTestId("sidebar-desktop");
    await expect(
      sidebar.getByRole("button", { name: "Show build info" }),
    ).toBeVisible();
    await expect(
      page.getByRole("button", { name: "Show build info" }),
    ).toHaveCount(1);
    await expect(page.getByTestId("version-footer")).toHaveAttribute(
      "data-placement",
      "sidebar",
    );
    await expect(page.getByTestId("app-topbar")).not.toContainText("self-host");
  });
});
