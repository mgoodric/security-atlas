// Slice 218 — Playwright E2E for the board-pack detail view.
//
// Seeded by fixtures/e2e/board-pack-detail.sql: one draft pack in the
// demo tenant whose period_end (2026-03-31) is a calendar-quarter end, so
// the breadcrumb's trailing segment reads `Q1 2026`. #2711 restored the
// assertions slice 218 left commented out behind the slice-082
// quarantine.
//
// Run locally:
//   cd web
//   npx playwright install chromium     # once per machine
//   npx playwright test e2e/board-pack-detail.spec.ts

import { expect, test } from "./fixtures";
import { seedFromFixture } from "./seed";

// Mirrors the id in fixtures/e2e/board-pack-detail.sql.
const PACK_ID = "21800000-0000-4000-8000-000000000218";

test.describe("board-pack detail view", () => {
  test.beforeAll(() => {
    seedFromFixture("board-pack-detail");
  });

  test("slice 218 / AC-1: breadcrumb chain renders with two segments", async ({
    authedPage: page,
  }) => {
    // Slice 218 closes the F-218 HONESTY-GAP — the sticky export bar
    // carries a 2-segment breadcrumb (`Board packs` → period label) at
    // the left edge, replacing the slice-043 `← All packs` link. The
    // mockup's third segment, a fabricated `Sentinel Labs` tenant name,
    // is deliberately dropped.
    await page.goto(`/board-packs/${PACK_ID}`);
    const breadcrumb = page.getByTestId("pack-breadcrumb");
    await expect(breadcrumb).toBeVisible({ timeout: 30_000 });
    // aria-label exposes it to screen readers as a breadcrumb nav.
    await expect(breadcrumb).toHaveAttribute("aria-label", "breadcrumb");
    // First segment: "Board packs" linking to /board-packs.
    const parent = page.getByTestId("pack-breadcrumb-segment-parent");
    await expect(parent).toHaveText("Board packs");
    await expect(parent).toHaveAttribute("href", "/board-packs");
    // Second segment: the period label, plain text (no href).
    const current = page.getByTestId("pack-breadcrumb-segment-current");
    await expect(current).toHaveAttribute("aria-current", "page");
    await expect(current).not.toHaveAttribute("href");
    await expect(current).toHaveText("Q1 2026");
    await expect(page.getByTestId("pack-breadcrumb-sep")).toHaveCount(1);
  });

  test("slice 218 / AC-3: parent link routes to /board-packs", async ({
    authedPage: page,
  }) => {
    await page.goto(`/board-packs/${PACK_ID}`);
    await page.getByTestId("pack-breadcrumb-segment-parent").click();
    await expect(page).toHaveURL(/\/board-packs(\?.*)?$/);
  });

  test("slice 218 / AC-2: legacy `← All packs` link is gone", async ({
    authedPage: page,
  }) => {
    // Slice 218 D2 REMOVED the slice-043 left-edge back link rather than
    // coexist with the breadcrumb; keeping both would be the redundancy
    // AC-2 warns against.
    await page.goto(`/board-packs/${PACK_ID}`);
    await expect(page.getByTestId("pack-breadcrumb")).toBeVisible({
      timeout: 30_000,
    });
    await expect(page.getByText(/^← All packs$/)).toHaveCount(0);
  });

  test("slice 218 / P0-218-2: no fabricated tenant-name segment", async ({
    authedPage: page,
  }) => {
    // The mockup at Plans/_archive/mockups/board-pack.html lines 27-30
    // opens its chain with `Sentinel Labs`. The live breadcrumb never
    // carries that tenant string nor the dead-anchor "Board reports"
    // parent.
    await page.goto(`/board-packs/${PACK_ID}`);
    const breadcrumb = page.getByTestId("pack-breadcrumb");
    await expect(breadcrumb).toBeVisible({ timeout: 30_000 });
    await expect(breadcrumb).not.toContainText("Sentinel Labs");
    await expect(breadcrumb).not.toContainText("Board reports");
  });

  test("auth: with no session the detail route bounces to /login", async ({
    authedPage: page,
  }) => {
    // With no session cookie the (authed) layout + proxy.ts redirect
    // before the page renders.
    await page.context().clearCookies();
    await page.goto(`/board-packs/${PACK_ID}`);
    await expect(page).toHaveURL(/\/login/);
  });
});
