// Slice 094 — Playwright E2E for the compliance calendar.
//
// Seeded by fixtures/e2e/calendar.sql: a tenant of the spec's own with
// one audit period, exception, policy review, vendor review and manual
// control review, all inside the default 90-day window and refreshed
// relative to now() on every run. The "compliance calendar" tests sign
// in to it with an admin JWT minted by /v1/test/issue-jwt. #2755
// restored the assertions slice 094 left commented out behind the
// slice-082 quarantine.
//
// Run locally:
//   cd web
//   npx playwright install chromium     # once per machine
//   npx playwright test e2e/calendar.spec.ts
//
// AC-20 coverage targets: agenda view renders, filter checkbox hides
// events, month-grid view renders, day-popover opens on click, ICS-copy
// button puts a URL on the clipboard.

import type { Page } from "@playwright/test";

import { expect, test } from "./fixtures";
import { seedFromFixture } from "./seed";
import { signInToTenant } from "./tenant-session";

// Mirror the tenant and rows in fixtures/e2e/calendar.sql.
const TENANT = "00000000-0000-0000-0000-2755a0000031";
const AUDIT_PERIOD = "cccccccc-cccc-cccc-cccc-2755a0000311";
const VENDOR = "77777777-7777-7777-7777-2755a0000311";
const MONTH_HEADING =
  /^(January|February|March|April|May|June|July|August|September|October|November|December) \d{4}$/;

test.describe("compliance calendar — today highlight (slice 668)", () => {
  // AC-3: the month grid marks the ACTUAL current day (not a hardcoded
  // value). This spec needs no seed data — the today affordance renders
  // for any signed-in user — so it runs live (not quarantined). The
  // expected day number is derived from the same wall-clock the renderer
  // reads, so the assertion is deterministic regardless of run date.
  test("AC-668: month grid marks today with aria-current='date' and an emphasized day number", async ({
    authedPage: page,
  }) => {
    await page.goto("/calendar?view=month");

    // Exactly one cell is the current day.
    const todayCell = page.locator('button[aria-current="date"]');
    await expect(todayCell).toHaveCount(1);
    await expect(todayCell).toBeVisible();

    // AC-1 / AC-3: that cell shows the real current day-of-month. The grid
    // keys cells on LOCAL dates, so compare against the local day number.
    const expectedDay = String(new Date().getDate());
    await expect(todayCell).toContainText(expectedDay);

    // The today affordance is also tagged for the unit/e2e contract.
    await expect(todayCell).toHaveAttribute("data-today", "true");
  });
});

test.describe("compliance calendar", () => {
  test.beforeAll(() => {
    seedFromFixture("calendar");
  });

  test.beforeEach(async ({ page }) => {
    await signInToTenant(page, {
      tenantId: TENANT,
      roles: ["admin"],
      superAdmin: true,
    });
  });

  // An agenda row (`<li>`) whose text includes `title`.
  const agendaRow = (page: Page, title: string) =>
    page.locator("section li", { hasText: title });

  test("AC-9: /calendar renders the calendar view for any signed-in user", async ({
    page,
  }) => {
    await page.goto("/calendar");
    // Page heading + sidebar entry exist. The page title is "Calendar"
    // (lib/page-titles.ts), not slice 094's "Compliance calendar".
    await expect(
      page.getByRole("heading", { level: 1, name: "Calendar" }),
    ).toBeVisible({ timeout: 30_000 });
    await expect(
      page.getByRole("link", { name: "Calendar", exact: true }),
    ).toBeVisible();
    await expect(
      agendaRow(page, "Audit period: E2E calendar period"),
    ).toBeVisible();
  });

  test("AC-10: default view is agenda; grouped by month; rows link to detail", async ({
    page,
  }) => {
    await page.goto("/calendar");
    const auditRow = agendaRow(page, "Audit period: E2E calendar period");
    await expect(auditRow).toBeVisible({ timeout: 30_000 });
    // Agenda layout: each group opens with a "<Month> <YYYY>" header.
    const monthHeaders = page
      .locator("section h2")
      .filter({ hasText: MONTH_HEADING });
    await expect(monthHeaders.first()).toBeVisible();
    await expect(monthHeaders.first()).toHaveText(MONTH_HEADING);
    // The audit row links to its period detail page.
    await expect(auditRow.locator("a")).toHaveAttribute(
      "href",
      `/audits/${AUDIT_PERIOD}`,
    );
  });

  test("AC-11: month-grid toggle renders a 7-column grid; day click opens popover", async ({
    page,
  }) => {
    await page.goto("/calendar");
    await page.getByRole("button", { name: "Month", exact: true }).click({
      timeout: 30_000,
    });
    await expect(page).toHaveURL(/[?&]view=month/);
    for (const day of ["Sun", "Mon", "Tue", "Wed", "Thu", "Fri", "Sat"]) {
      await expect(page.getByText(day, { exact: true })).toBeVisible();
    }
    // The never-evaluated manual control's review falls on today, so the
    // today cell always holds a seeded event.
    const dayCell = page.locator('button[aria-current="date"]');
    await expect(dayCell).toContainText("Control review: E2E calendar control");
    await dayCell.click();
    const dialog = page.getByRole("dialog");
    await expect(dialog).toBeVisible();
    await expect(dialog).toHaveAttribute(
      "aria-label",
      /^Events on \d{4}-\d{2}-\d{2}$/,
    );
    await expect(dialog).toContainText("Control review: E2E calendar control");
  });

  // Product bug: /exceptions/[id] now exists, but components/calendar/
  // link-for.ts still renders exception events unlinked with a tooltip
  // saying a detail page "is not available yet". Its header says the
  // branch flips to a link once that page ships; it did not flip. #2772.
  test.fixme(
    "AC-183-1: exception events render as a static span, NOT an <a> (no 404)",
    async () => {},
  );

  // Product bug: /policies/[id] now exists, but link-for.ts still renders
  // policy events unlinked with a "not available yet" tooltip. #2772.
  test.fixme(
    "AC-183-2: policy events render as a static span, NOT an <a> (no 404)",
    async () => {},
  );

  test("AC-675-1: legend lists 'Vendor reviews' and the agenda surfaces vendor events", async ({
    page,
  }) => {
    // Slice 675: the calendar sources vendor reviews, and the legend has
    // a "Vendor reviews" entry. The seeded monthly vendor's next review
    // falls inside the default 90-day window.
    await page.goto("/calendar");
    await expect(page.getByLabel("Vendor reviews")).toBeVisible({
      timeout: 30_000,
    });
    const vendorRow = agendaRow(page, "Vendor review: E2E calendar vendor");
    await expect(vendorRow).toBeVisible();
    // Vendor rows link to the real per-vendor detail page.
    await expect(vendorRow.locator("a")).toHaveAttribute(
      "href",
      `/vendors/${VENDOR}`,
    );
  });

  test("AC-12: filter checkbox hides events of that type", async ({ page }) => {
    await page.goto("/calendar");
    const exceptionRow = agendaRow(page, "Exception on E2E calendar control");
    await expect(exceptionRow).toBeVisible({ timeout: 30_000 });
    const rows = page.locator("section ul li");
    const initial = await rows.count();
    // The checkbox is controlled by the `types` URL param, so it flips only
    // after the router replace lands; click, then wait for the new state.
    const checkbox = page.getByLabel("Exception expirations");
    await checkbox.click();
    await expect(checkbox).not.toBeChecked();
    await expect(page).toHaveURL(/[?&]types=/);
    await expect(exceptionRow).toHaveCount(0);
    // Only the one exception event left; the other types stay.
    await expect(rows).toHaveCount(initial - 1);
    await expect(
      agendaRow(page, "Audit period: E2E calendar period"),
    ).toBeVisible();
  });

  test("AC-14: 'Subscribe in calendar' click puts a URL on the clipboard", async ({
    page,
    context,
  }) => {
    await context.grantPermissions(["clipboard-read", "clipboard-write"]);
    await page.goto("/calendar");
    await page
      .getByRole("button", { name: /Subscribe in calendar/ })
      .click({ timeout: 30_000 });
    await expect(
      page.getByRole("status").filter({ hasText: "URL copied" }),
    ).toBeVisible({ timeout: 30_000 });
    const clip = await page.evaluate(() => navigator.clipboard.readText());
    expect(clip).toMatch(/\/v1\/calendar\.ics\?token=.+/);
  });
});
