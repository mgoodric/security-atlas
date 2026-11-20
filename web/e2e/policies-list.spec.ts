// Slice 101 — Playwright E2E for the /policies list view.
//
// Seeded by fixtures/e2e/policies-list.sql: a tenant of the spec's own
// with two published policies (owners security / engineering, ack rates
// 100% and 50%) and one security-owned draft, plus an empty tenant for
// the zero-state. The spec signs in to each with an admin JWT minted by
// /v1/test/issue-jwt. #2755 restored the assertions slice 101 left
// commented out behind the slice-082 quarantine, against what ships
// now: the ack-rate join (slice 107) and the live "New policy" link
// (#2116) have landed since the bodies were written.
//
// Run locally:
//   cd web
//   npx playwright install chromium     # once per machine
//   npx playwright test e2e/policies-list.spec.ts
//
// AC-10 coverage targets: page chrome + table render, horizontal pill
// row, ack-rate column renders honestly (bar for published rows,
// em-dash otherwise), empty-state CTA wording, row click navigates.

import type { Page } from "@playwright/test";

import { expect, test } from "./fixtures";
import { seedFromFixture } from "./seed";
import { signInToTenant } from "./tenant-session";

// Mirror the tenants in fixtures/e2e/policies-list.sql.
const POLICIES_TENANT = "00000000-0000-0000-0000-2755d0000002";
const EMPTY_TENANT = "00000000-0000-0000-0000-2755d0000003";

const ACCESS = "Access control policy";
const CHANGE = "Change management policy";
const RETENTION = "Data retention policy";

function signIn(page: Page, tenantId: string): Promise<void> {
  return signInToTenant(page, {
    tenantId,
    roles: ["admin"],
    superAdmin: true,
  });
}

/** Opens /policies (plus `query`) and waits for the seeded rows. */
async function openPolicies(page: Page, query = ""): Promise<void> {
  await page.goto(`/policies${query}`);
  await expect(page.getByTestId("list-page")).toBeVisible({ timeout: 30_000 });
  await expect(page.getByTestId("list-loading-skeleton")).toHaveCount(0, {
    timeout: 30_000,
  });
}

function rows(page: Page) {
  return page.getByTestId("list-table-wrap").getByTestId("list-table-row");
}

test.describe("/policies list view", () => {
  test.beforeAll(() => {
    seedFromFixture("policies-list");
  });

  test.beforeEach(async ({ page }) => {
    await signIn(page, POLICIES_TENANT);
  });

  test("AC-1: /policies renders the policy library for any signed-in user", async ({
    page,
  }) => {
    await openPolicies(page);
    await expect(
      page.getByRole("heading", { name: "Policies", exact: true }),
    ).toBeVisible();
    await expect(page.getByTestId("list-table-wrap")).toBeVisible();
    await expect(rows(page)).toHaveCount(3);
    await expect(page.getByTestId("list-filter-meta")).toContainText(
      "Showing 3 of 3 policies",
    );
  });

  test("AC-2: table renders the canonical columns from policyWire", async ({
    page,
  }) => {
    await openPolicies(page);
    const table = page.getByTestId("list-table-wrap");
    // #1806 added "Next review" to the slice 101 set.
    for (const header of [
      "Title",
      "Version",
      "Status",
      "Owner role",
      "Published",
      "Next review",
      "Acknowledgment",
      "Updated",
    ]) {
      await expect(
        table.getByRole("columnheader", { name: header, exact: true }),
      ).toBeVisible();
    }
  });

  test("AC-3 (slice 107): ack-rate cell renders a bar for published rows and an em-dash otherwise", async ({
    page,
  }) => {
    // Slice 107 shipped the `?include=ack_rate` join this test once
    // waited for. A draft carries `ack_rate: null` and renders the
    // em-dash; a published row renders the bar and its caption.
    await openPolicies(page);
    const draft = rows(page).filter({ hasText: RETENTION });
    await expect(draft.getByTestId("policies-ack-rate-missing")).toHaveText(
      "—",
    );
    const published = rows(page).filter({ hasText: ACCESS });
    await expect(published.getByTestId("policies-ack-rate-cell")).toBeVisible();
    await expect(
      published.getByTestId("policies-ack-rate-missing"),
    ).toHaveCount(0);
  });

  test("AC-4: horizontal pill filter row narrows by status", async ({
    page,
  }) => {
    await openPolicies(page);
    await expect(rows(page)).toHaveCount(3);
    // The filter row is horizontal (P0-A2 of slice 098) — the pill row
    // mounts, NOT a left sidebar.
    await expect(page.getByTestId("list-filter-pills")).toBeVisible();
    await page
      .getByTestId("list-filter-pill-status")
      .getByRole("combobox")
      .selectOption("draft");
    await expect(page).toHaveURL(/[?&]status=draft/);
    await expect(rows(page)).toHaveCount(1);
    await expect(rows(page).first()).toContainText(RETENTION);
  });

  test("AC-4: horizontal pill filter row narrows by owner_role", async ({
    page,
  }) => {
    await openPolicies(page);
    const ownerPill = page.getByLabel("Owner role");
    const opts = await ownerPill.locator("option").allTextContents();
    expect(opts).toEqual(
      expect.arrayContaining(["All roles", "engineering", "security"]),
    );
    await ownerPill.selectOption("engineering");
    await expect(page).toHaveURL(/[?&]owner_role=engineering/);
    await expect(rows(page)).toHaveCount(1);
    await expect(rows(page).first()).toContainText(CHANGE);
  });

  test("AC-1 (slice 238): ack_status pill renders between owner_role and meta", async ({
    page,
  }) => {
    // Slice 238 — the "Ack status" pill sits after the Owner role pill,
    // ahead of the right-aligned "Showing N of M" meta counter.
    await openPolicies(page);
    const ackPill = page.getByTestId("list-filter-pill-ack_status");
    await expect(ackPill).toBeVisible();
    const order = await page
      .getByTestId("list-filter-pills")
      .locator("[data-testid^='list-filter-']")
      .evaluateAll((els) => els.map((e) => e.getAttribute("data-testid")));
    const owner = order.indexOf("list-filter-pill-owner_role");
    const ack = order.indexOf("list-filter-pill-ack_status");
    const meta = order.indexOf("list-filter-meta");
    expect(owner).toBeGreaterThanOrEqual(0);
    expect(ack).toBeGreaterThan(owner);
    expect(meta).toBeGreaterThan(ack);
    const opts = await page
      .getByLabel("Ack status")
      .locator("option")
      .allTextContents();
    expect(opts).toContain("All ack rates");
    expect(opts.some((o) => o.includes("95%"))).toBe(true);
    expect(opts.some((o) => o.includes("50%"))).toBe(true);
  });

  test("AC-2/AC-3 (slice 238): ack_status=ge95 narrows + URL is bookmarkable", async ({
    page,
  }) => {
    // Band narrowing is client-side; the URL carries the active band so
    // `/policies?ack_status=ge95` is shareable.
    await openPolicies(page, "?ack_status=ge95");
    await expect(page.getByLabel("Ack status")).toHaveValue("ge95");
    // Only the 100% policy is in the band. Rows with a null ack cell
    // MUST NOT appear under a non-ALL band (slice 238 AC-2).
    await expect(rows(page)).toHaveCount(1);
    await expect(rows(page).first()).toContainText(ACCESS);
    await expect(page.getByTestId("policies-ack-rate-missing")).toHaveCount(0);
  });

  test("slice 241 / AC-4: header acknowledgment-report disclosure replaces the disabled button", async ({
    page,
  }) => {
    // Slice 241 replaced a permanently-disabled "Acknowledgment report"
    // button with a non-button `<span>` disclosing the future state.
    await openPolicies(page);
    const disclosure = page.getByTestId("policies-ack-report-future");
    await expect(disclosure).toBeVisible();
    await expect(disclosure).toContainText(/acknowledgment report/i);
    // `title` and `aria-label` carry the same copy as the visible text.
    await expect(disclosure).toHaveAttribute("title", /acknowledgment report/i);
    await expect(disclosure).toHaveAttribute(
      "aria-label",
      /acknowledgment report/i,
    );
    // No disabled <button> with the original label survives.
    await expect(
      page.locator("button[disabled]", { hasText: "Acknowledgment report" }),
    ).toHaveCount(0);
  });

  test("slice 241 / #2116: header New policy is a live link, not a disabled button", async ({
    page,
  }) => {
    // Slice 241 turned the disabled "New policy" button into a
    // disclosure; #2116 then shipped /policies/new and the header now
    // links to it (the slice 247 risks shape).
    await openPolicies(page);
    const link = page.getByTestId("policies-new");
    await expect(link).toBeVisible();
    await expect(link).toHaveText("New policy");
    await expect(link).toHaveAttribute("href", "/policies/new");
    await expect(
      page.locator("button[disabled]", { hasText: /^New policy$/ }),
    ).toHaveCount(0);
    await link.click();
    await expect(page).toHaveURL(/\/policies\/new$/);
  });

  test("AC-5 (slice 242 update): true zero-state replaces the lying scaffold CTA with a label-honest body disclosure", async ({
    page,
  }) => {
    // Slice 242 retired the "Scaffold five foundational policies" CTA
    // that pointed at /admin/credentials; the zero-state carries a body
    // disclosure instead. #2082 dropped the API path from the copy, so
    // it names an administrator as the next step.
    await signIn(page, EMPTY_TENANT);
    await openPolicies(page);
    const empty = page.getByTestId("list-empty-state");
    await expect(empty).toBeVisible();
    await expect(empty).toContainText("No policies published yet");
    await expect(page.getByTestId("list-empty-state-cta")).toHaveCount(0);
    const disclosure = page.getByTestId("policies-scaffold-future");
    await expect(disclosure).toBeVisible();
    await expect(disclosure).toContainText(/policy scaffold/i);
    await expect(disclosure).toContainText("administrator");
  });

  test("AC-5: filter-induced empty surfaces 'Clear filters' instead of scaffold CTA", async ({
    page,
  }) => {
    // No policy can carry `retired` (the status CHECK has no such
    // value), so the filter narrows to nothing.
    await openPolicies(page, "?status=retired");
    await expect(page.getByTestId("list-empty-state")).toContainText(
      "No policies match these filters",
    );
    await expect(page.getByTestId("policies-scaffold-future")).toHaveCount(0);
    const cta = page.getByTestId("list-empty-state-cta");
    await expect(cta).toHaveText("Clear filters");
    await cta.click();
    await expect(page).not.toHaveURL(/status=/);
    await expect(rows(page)).toHaveCount(3);
  });

  test("AC-6: loading skeleton shows three shimmer rows on first paint", async ({
    page,
  }) => {
    // Hold the BFF response so the skeleton stays up long enough to
    // assert, then let it through and watch the table replace it.
    let release: () => void = () => {};
    const held = new Promise<void>((r) => {
      release = r;
    });
    await page.route("**/api/policies", async (route) => {
      await held;
      await route.continue();
    });
    await page.goto("/policies");
    const skeleton = page.getByTestId("list-loading-skeleton");
    await expect(skeleton).toBeVisible({ timeout: 30_000 });
    await expect(skeleton.getByTestId("list-loading-skeleton-row")).toHaveCount(
      3,
    );
    release();
    await expect(rows(page)).toHaveCount(3, { timeout: 30_000 });
    await expect(skeleton).toHaveCount(0);
  });

  test("AC-7: <Progress> primitive carries semantic ARIA label", async ({
    page,
  }) => {
    await openPolicies(page);
    const bars = page.getByTestId("list-table-wrap").getByRole("progressbar");
    // The two published policies render a bar; the draft does not.
    await expect(bars).toHaveCount(2);
    const access = rows(page).filter({ hasText: ACCESS });
    await expect(access.getByRole("progressbar")).toHaveAttribute(
      "aria-label",
      "2 of 2 acknowledged · 100%",
    );
    const change = rows(page).filter({ hasText: CHANGE });
    await expect(change.getByRole("progressbar")).toHaveAttribute(
      "aria-label",
      "1 of 2 acknowledged · 50%",
    );
  });

  test("AC-8: row click navigates to /policies/[id]", async ({ page }) => {
    await openPolicies(page);
    // Click a plain cell, not the title link, so the row handler is the
    // thing under test.
    await rows(page)
      .filter({ hasText: ACCESS })
      .getByTestId("list-cell-version")
      .click();
    await expect(page).toHaveURL(
      /\/policies\/77777777-7777-7777-7777-2755d0000001$/,
    );
  });

  test("P0-A2: page does NOT fan out per-row to /v1/policies/{id}/acknowledgment-rate", async ({
    page,
  }) => {
    // One call to /api/policies carries every row's ack rate; no
    // request may target a per-policy acknowledgment-rate path.
    const ackRateCalls: string[] = [];
    let listCalls = 0;
    page.on("request", (req) => {
      const url = new URL(req.url());
      if (url.pathname.includes("/acknowledgment-rate")) {
        ackRateCalls.push(req.url());
      }
      if (url.pathname === "/api/policies") listCalls++;
    });
    await openPolicies(page);
    await expect(rows(page)).toHaveCount(3);
    await page.waitForLoadState("networkidle");
    expect(listCalls).toBeGreaterThanOrEqual(1);
    expect(ackRateCalls).toEqual([]);
  });
});
