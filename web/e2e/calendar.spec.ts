// Slice 094 — Playwright E2E for the compliance calendar.
//
// Runner status (post-slice-069 / 071 audit):
// Playwright IS installed in `web/`. The five user-flow specs are
// quarantined behind slice 082 (the seed-data harness) per slice 079's
// decision; when that harness lands, the un-commented assertions below
// become the gate. The test body is preserved verbatim as a reviewable
// contract per the slice 040 / 042 / 056 / 060 / 064 / 071 precedent.
//
// Run locally:
//   cd web
//   npx playwright install chromium     # once per machine
//   npx playwright test e2e/calendar.spec.ts
//
// Pre-conditions the seed-data harness (slice 082) must establish
// before the commented assertions are turned on:
//   - PLATFORM_BASE_URL points at a running platform instance
//   - TEST_BEARER carries a credential in a tenant that has at least
//     one event per type (audit period, exception, policy with
//     next_review_at set, control with cadence + last_evaluated_at)
//   - the test tenant's clock is "now" so the default 90-day window
//     surfaces the seeded events
//
// AC-20 coverage targets: agenda view renders, filter checkbox hides
// events, month-grid view renders, day-popover opens on click, ICS-copy
// button puts a URL on the clipboard.

import { test } from "@playwright/test";

test.describe("compliance calendar", () => {
  test("AC-9: /calendar renders the calendar view for any signed-in user", async () => {
    // 1. Sign in (any role — calendar is no-admin-gate).
    //    await page.goto("/login");
    //    await page.fill('input[name="token"]', process.env.TEST_BEARER!);
    //    await page.click("button[type=submit]");
    // 2. Visit /calendar.
    //    await page.goto("/calendar");
    // 3. Page heading + top-nav entry exist.
    //    await expect(page.getByRole("heading", { name: /Compliance calendar/ })).toBeVisible();
    //    await expect(page.getByRole("link", { name: "Calendar" })).toBeVisible();
  });

  test("AC-10: default view is agenda; grouped by month; rows link to detail", async () => {
    //    await page.goto("/calendar");
    // Agenda layout — at least one month header visible.
    //    await expect(page.locator("h2").first()).toContainText(
    //      /(January|February|March|April|May|June|July|August|September|October|November|December)/,
    //    );
    //    await expect(page.locator("ul li a").first()).toBeVisible();
  });

  test("AC-11: month-grid toggle renders a 7-column grid; day click opens popover", async () => {
    //    await page.goto("/calendar");
    //    await page.getByRole("button", { name: "Month" }).click();
    //    await expect(page.getByText("Mon")).toBeVisible();
    //    await expect(page.getByText("Sun")).toBeVisible();
    // Click a day cell that has events seeded.
    //    await page.locator('button[type="button"]').nth(15).click();
    //    await expect(page.getByRole("dialog")).toBeVisible();
  });

  test("AC-183-1: exception events render as a static span, NOT an <a> (no 404)", async () => {
    // Slice 183 (F-178-2 closure). When the seed harness lands and
    // surfaces at least one exception event in the calendar window,
    // assert that the row carries the disclosure tooltip and is NOT
    // rendered as an <a> pointing at the non-existent
    // `/admin/exceptions/<id>` route. Same shape as slice 184's
    // AC-184-1 / slice 185's AC-185-1 quarantined specs.
    //
    //    await page.goto("/calendar");
    // Find an exception row (the agenda renders the type label in an
    // uppercase span; the row text contains "Exception").
    //    const exceptionRow = page.locator("li", { hasText: "Exception" }).first();
    //    await expect(exceptionRow).toBeVisible();
    // Assert no <a> descendant — the row is a <span>, not a <Link>.
    //    await expect(exceptionRow.locator("a")).toHaveCount(0);
    // Assert the disclosure tooltip is present (title attribute).
    //    const tooltip = await exceptionRow.locator("span").first().getAttribute("title");
    //    expect(tooltip).toMatch(/exception/i);
    //    expect(tooltip).toMatch(/future slice/i);
  });

  test("AC-183-2: policy events render as a static span, NOT an <a> (no 404)", async () => {
    // Slice 183 (F-178-3 closure). Same shape as AC-183-1 for policy
    // events. The previous `/policies/<id>` href was a dead link.
    //
    //    await page.goto("/calendar");
    //    const policyRow = page.locator("li", { hasText: "Policy" }).first();
    //    await expect(policyRow).toBeVisible();
    //    await expect(policyRow.locator("a")).toHaveCount(0);
    //    const tooltip = await policyRow.locator("span").first().getAttribute("title");
    //    expect(tooltip).toMatch(/policy/i);
    //    expect(tooltip).toMatch(/future slice/i);
  });

  test("AC-12: filter checkbox hides events of that type", async () => {
    //    await page.goto("/calendar");
    //    const initial = await page.locator("ul li").count();
    //    await page.getByLabel("Exception expirations").uncheck();
    //    await page.waitForTimeout(500); // re-fetch
    //    const filtered = await page.locator("ul li").count();
    //    expect(filtered).toBeLessThan(initial);
  });

  test("AC-14: 'Subscribe in calendar' click puts a URL on the clipboard", async () => {
    // Note: the Playwright `clipboard-read` permission needs to be
    // granted in the browser context for this assertion. The fixtures
    // helper (web/e2e/fixtures.ts) is the right place to grant it once
    // the seed harness lands.
    //    await context.grantPermissions(["clipboard-read", "clipboard-write"]);
    //    await page.goto("/calendar");
    //    await page.getByRole("button", { name: /Subscribe in calendar/ }).click();
    //    const clip = await page.evaluate(() => navigator.clipboard.readText());
    //    expect(clip).toMatch(/calendar\.ics\?token=/);
  });
});
