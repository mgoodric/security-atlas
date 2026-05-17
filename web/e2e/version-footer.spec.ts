// Slice 072 — Playwright E2E for the VersionFooter component.
//
// The footer renders on every page (authed layout + the login page),
// shows a version string, and the click toggle reveals a build-info
// panel. This spec asserts the unauthenticated path — `/login` — so it
// runs without seed data; the authed-path assertions stay commented
// pending the slice-069 seed-data harness (same convention as the other
// e2e specs in this directory).
//
// Hard rule (slice-069 lesson, P0-A9): `expect` is imported AND used.
// No unused-import drift — every imported symbol shows up below.

import { test, expect } from "@playwright/test";

test.describe("version footer", () => {
  test("renders the version trigger on /login", async ({ page }) => {
    // Hit the login page directly — no auth required.
    await page.goto("/login");

    // The footer's trigger is a <button aria-label="Show build info">.
    const trigger = page.getByRole("button", { name: "Show build info" });
    await expect(trigger).toBeVisible();

    // Pre-click: aria-expanded should be false.
    await expect(trigger).toHaveAttribute("aria-expanded", "false");
  });

  test("clicking the trigger toggles the build-info panel on /login", async ({
    page,
  }) => {
    await page.goto("/login");

    const trigger = page.getByRole("button", { name: "Show build info" });
    await trigger.click();
    await expect(trigger).toHaveAttribute("aria-expanded", "true");

    // The panel exposes the four contract fields. We assert the labels
    // (not the values) because values vary by build.
    const panel = page.getByRole("region", { name: "Show build info" });
    await expect(panel).toBeVisible();
    await expect(panel).toContainText("version");
    await expect(panel).toContainText("commit");
    await expect(panel).toContainText("build_time");
    await expect(panel).toContainText("go_version");

    // Click again — collapses.
    await trigger.click();
    await expect(trigger).toHaveAttribute("aria-expanded", "false");
  });

  // The authed-layout assertion lives commented pending the slice-069
  // seed-data harness. When the harness lands, this test asserts the
  // same footer renders on /dashboard via the `authedPage` fixture.
  //
  // test("renders on /dashboard when signed in", async ({ authedPage }) => {
  //   await authedPage.goto("/dashboard");
  //   const trigger = authedPage.getByRole("button", { name: "Show build info" });
  //   await expect(trigger).toBeVisible();
  // });
});
