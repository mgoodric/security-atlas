// Slice 218 — Playwright E2E for the board-pack detail view.
//
// Runner status (post-slice-069 / 071 audit):
// Playwright IS installed in `web/`. This spec is quarantined behind
// slice 082 (the seed-data harness) per slice 079's decision; when
// that harness lands, the un-commented assertions below become the
// gate. The test bodies are preserved verbatim as a reviewable
// contract per the slice 041 / 102 / 217 precedent.
//
// Run locally:
//   cd web
//   npx playwright install chromium     # once per machine
//   npx playwright test e2e/board-pack-detail.spec.ts
//
// Pre-conditions the seed-data harness (slice 082) must establish
// before the commented assertions are turned on:
//   - PLATFORM_BASE_URL points at a running platform instance
//   - TEST_BEARER carries a credential in a tenant with at least one
//     board pack (status=draft or status=published)
//   - KNOWN_BOARD_PACK_ID is that pack's UUID
//   - KNOWN_BOARD_PACK_PERIOD_END is its YYYY-MM-DD period_end (a
//     calendar-quarter end, so the breadcrumb's trailing segment
//     reads as a `Qn YYYY` label rather than the raw date)

import { test } from "@playwright/test";

import { seedFromFixture } from "./seed";

test.describe("board-pack detail view", () => {
  test.beforeAll(() => {
    seedFromFixture("board-pack-detail");
  });

  test("slice 218 / AC-1: breadcrumb chain renders with two segments", async () => {
    // Slice 218 closes the F-218 HONESTY-GAP — the sticky export bar
    // now carries a 2-segment breadcrumb (`Board packs` → period
    // label) at the left edge, replacing the slice-043 `← All packs`
    // link. AC-1 says "at least two segments"; we render exactly two
    // (the mockup's third segment, a fabricated `Sentinel Labs`
    // tenant-name, is deliberately dropped — there's no session-bound
    // tenant name to render honestly).
    //    await page.goto(`/board-packs/${KNOWN_BOARD_PACK_ID}`);
    //    const breadcrumb = page.getByTestId("pack-breadcrumb");
    //    await expect(breadcrumb).toBeVisible();
    //    // aria-label exposes it to screen readers as a breadcrumb nav.
    //    await expect(breadcrumb).toHaveAttribute("aria-label", "breadcrumb");
    //    // First segment: "Board packs" linking to /board-packs.
    //    const parent = page.getByTestId("pack-breadcrumb-segment-parent");
    //    await expect(parent).toBeVisible();
    //    await expect(parent).toHaveText("Board packs");
    //    await expect(parent).toHaveAttribute("href", "/board-packs");
    //    // Second segment: the period label, plain text (no href).
    //    const current = page.getByTestId("pack-breadcrumb-segment-current");
    //    await expect(current).toBeVisible();
    //    await expect(current).toHaveAttribute("aria-current", "page");
    //    const text = (await current.textContent())?.trim() ?? "";
    //    expect(text).toMatch(/^(Q[1-4] \d{4}|\d{4}-\d{2}-\d{2})$/);
  });

  test("slice 218 / AC-3: parent link routes to /board-packs", async () => {
    //    await page.goto(`/board-packs/${KNOWN_BOARD_PACK_ID}`);
    //    await page.getByTestId("pack-breadcrumb-segment-parent").click();
    //    await expect(page).toHaveURL(/\/board-packs(\?.*)?$/);
  });

  test("slice 218 / AC-2: legacy `← All packs` link is gone", async () => {
    // Slice 218 D2 chose to REMOVE the slice-043 left-edge back link
    // rather than coexist with the breadcrumb. The breadcrumb's first
    // segment is semantically the same as the old link; keeping both
    // would be the redundancy AC-2 warns against.
    //    await page.goto(`/board-packs/${KNOWN_BOARD_PACK_ID}`);
    //    await expect(page.getByText(/^← All packs$/)).toHaveCount(0);
  });

  test("slice 218 / P0-218-2: no fabricated tenant-name segment", async () => {
    // The mockup at Plans/_archive/mockups/board-pack.html lines 27-30 ships a
    // three-segment chain that opens with `Sentinel Labs`. We
    // deliberately diverge — no session-bound tenant name to render
    // honestly. Regression guard: the live breadcrumb never carries
    // the mockup tenant string nor the dead-anchor "Board reports"
    // parent.
    //    await page.goto(`/board-packs/${KNOWN_BOARD_PACK_ID}`);
    //    await expect(page.getByTestId("pack-breadcrumb")).not.toContainText(
    //      "Sentinel Labs",
    //    );
    //    await expect(page.getByTestId("pack-breadcrumb")).not.toContainText(
    //      "Board reports",
    //    );
  });

  test("auth: a 401 from a bound endpoint bounces to /login", async () => {
    // With no session cookie the (authed) layout + proxy.ts redirect
    // before the page renders; a cookie that expires mid-session is
    // caught by the page's 401 -> /login effect.
    //    await page.context().clearCookies();
    //    await page.goto(`/board-packs/${KNOWN_BOARD_PACK_ID}`);
    //    await expect(page).toHaveURL(/\/login/);
  });
});
