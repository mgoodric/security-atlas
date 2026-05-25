// Slice 277 — Playwright e2e for the mobile-responsive baseline.
//
// Scope (slice 277 AC-10): viewport meta + sidebar drawer behaviour at
// 375px. NOT a per-page mobile fan-out — those land as spillovers from
// `docs/responsive-audit.md`.
//
// The spec verifies five surfaces:
//
//   1. The rendered HTML carries `<meta name="viewport">`. (AC-2)
//   2. At 375px viewport, the hamburger trigger is visible AND the
//      desktop sidebar is hidden (CSS `display: none`). (AC-3)
//   3. Clicking the hamburger opens the drawer; clicking a nav link
//      navigates AND closes the drawer. (AC-4 + AC-6 partial)
//   4. The drawer closes on Escape. (AC-7)
//   5. At 1280px viewport (desktop), the hamburger is hidden AND the
//      desktop sidebar is visible. (AC-5 — no desktop UX regression)
//
// The mobile viewport is set per-test via
// `page.setViewportSize({ width: 375, height: 812 })` so the spec
// reads the same as the slice 040 dashboard responsive case. The
// desktop case uses the default Playwright viewport (1280x720 per
// `devices["Desktop Chrome"]`).
//
// AC-4 acceptance language calls out the drawer composing with slice
// 213's audit pill + slice 214's count badges + slice 223's ⌘K + slice
// 250's avatar. Per the slice 277 D5 decision, the audit pill / ⌘K /
// avatar remain in the TOPBAR (not relocated into the drawer); the
// drawer hosts the navigation Links + slice 214 badges (which are
// re-mounted inside the drawer via the `MobileSidebar` render). The
// asserted shape below pins the loadbearing parts:
//
//   - drawer hosts the same nav rows as desktop (assertion on label)
//   - drawer dismisses cleanly (assertion on hidden state)
//   - desktop UX at 1280px is unchanged (assertion on inline sidebar)

import { expect, test } from "./fixtures";

const MOBILE_VIEWPORT = { width: 375, height: 812 };

test.describe("mobile-responsive baseline (slice 277)", () => {
  test("AC-2: rendered HTML includes <meta name='viewport'>", async ({
    authedPage: page,
  }) => {
    // The viewport export lives in `web/app/layout.tsx` (the ROOT
    // layout, NOT the authed layout), so it renders on every page.
    // We check /dashboard because it's the most-trafficked authed
    // landing — but any authed route would do.
    await page.goto("/dashboard");
    const meta = await page
      .locator('head meta[name="viewport"]')
      .getAttribute("content");
    expect(meta).not.toBeNull();
    // Tolerant on whitespace + casing; load-bearing tokens are
    // "device-width" and "initial-scale=1".
    expect(meta).toMatch(/width=device-width/i);
    expect(meta).toMatch(/initial-scale=1/);
  });

  test("AC-3 + AC-5: at 375px the hamburger is visible and the desktop sidebar is hidden", async ({
    authedPage: page,
  }) => {
    await page.setViewportSize(MOBILE_VIEWPORT);
    await page.goto("/dashboard");

    // Hamburger trigger renders on every authed page (mounted in the
    // shared topbar). At 375px the trigger's `md:hidden` is inactive
    // (we are < md), so it is visible.
    const trigger = page.getByTestId("mobile-sidebar-trigger");
    await expect(trigger).toBeVisible();

    // Desktop sidebar carries `hidden md:block` — at 375px the
    // `hidden` wins, so it is in the DOM but not visible.
    const desktopSidebar = page.getByTestId("sidebar-desktop");
    await expect(desktopSidebar).toBeHidden();
  });

  test("AC-4 + AC-6: clicking the hamburger opens the drawer; clicking a nav link navigates and closes the drawer", async ({
    authedPage: page,
  }) => {
    await page.setViewportSize(MOBILE_VIEWPORT);
    await page.goto("/dashboard");

    // Drawer not visible before click.
    await expect(page.getByTestId("mobile-sidebar-drawer")).toBeHidden();

    await page.getByTestId("mobile-sidebar-trigger").click();

    // Drawer visible after click; the same nav items render.
    const drawer = page.getByTestId("mobile-sidebar-drawer");
    await expect(drawer).toBeVisible();
    await expect(drawer.getByText("Dashboard")).toBeVisible();
    await expect(drawer.getByText("Controls")).toBeVisible();
    await expect(drawer.getByText("Risks")).toBeVisible();
    await expect(drawer.getByText("Settings")).toBeVisible();

    // Click a nav link; the router navigates AND the drawer closes.
    await drawer.getByText("Settings").click();
    await expect(page).toHaveURL(/\/settings/);
    await expect(page.getByTestId("mobile-sidebar-drawer")).toBeHidden();
  });

  test("AC-7: drawer closes on Escape", async ({ authedPage: page }) => {
    await page.setViewportSize(MOBILE_VIEWPORT);
    await page.goto("/dashboard");

    await page.getByTestId("mobile-sidebar-trigger").click();
    await expect(page.getByTestId("mobile-sidebar-drawer")).toBeVisible();

    // Escape dismisses (WAI-ARIA dialog default; @base-ui/react
    // implements this).
    await page.keyboard.press("Escape");
    await expect(page.getByTestId("mobile-sidebar-drawer")).toBeHidden();
  });

  test("AC-5: at 1280px (desktop) the hamburger is hidden and the inline sidebar is visible", async ({
    authedPage: page,
  }) => {
    // No viewport set — uses Playwright's `devices["Desktop Chrome"]`
    // default (1280x720 per `playwright.config.ts`).
    await page.goto("/dashboard");

    // Hamburger carries `md:hidden` — at 1280px (>= md=768px) the
    // class hides it.
    await expect(page.getByTestId("mobile-sidebar-trigger")).toBeHidden();

    // Desktop sidebar visible (the slice-277-pre baseline).
    await expect(page.getByTestId("sidebar-desktop")).toBeVisible();
  });
});
