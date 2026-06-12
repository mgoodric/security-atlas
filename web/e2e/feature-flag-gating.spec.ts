// Slice 660 — Playwright e2e for feature-flag nav + route gating.
//
// Two surfaces:
//
//   1. NAV OMISSION (default state). The e2e backend seeds `oscal.export`
//      and `board.reporting` OFF (the Seed defaults — both "pending GA").
//      `getAuthedNav()` resolves the enabled-modules server-side and drops
//      the "Vendor Claims" + "Board Packs" nav entries. We assert the
//      desktop sidebar omits both. This runs against the real backend
//      (the server-side enabled-modules read cannot be page.route-mocked —
//      it happens in Node, not the browser — so we lean on the seed
//      default, which is exactly the pre-GA state the gate protects).
//
//   2. CLEAN DISABLED STATE (direct navigation). A user who deep-links to a
//      gated route must see a calm "module not enabled" panel, not a raw
//      error. The page's data query is a BROWSER request, so we
//      route-mock it to the platform's gate response (404 + {"error":
//      "feature disabled"}) — the hermetic-mock pattern from
//      feedback_e2e_shared_db_hermetic_mock — and assert the disabled
//      panel renders.
//
// The flag-ON nav case (entries reappear when the flag is on) is covered
// at the Go integration tier (the route actually serves) and the vitest
// tier (gateNavItems renders the item when the flag is true); reproducing
// it here would require toggling a backend flag, which the route-mock
// path cannot reach (server-side read).

import { expect, test } from "./fixtures";

test.describe("slice 660 — feature-flag nav gating", () => {
  test("default state omits Vendor Claims + Board Packs from the sidebar", async ({
    authedPage: page,
  }) => {
    await page.goto("/dashboard");
    const sidebar = page.getByTestId("sidebar-desktop");
    await expect(sidebar).toBeVisible();
    // The two pre-GA modules are gated OFF by Seed default -> hidden.
    await expect(
      sidebar.getByRole("link", { name: "Board Packs" }),
    ).toHaveCount(0);
    await expect(
      sidebar.getByRole("link", { name: "Vendor Claims" }),
    ).toHaveCount(0);
    // A non-gated entry still renders (sanity: the nav itself works).
    await expect(
      sidebar.getByRole("link", { name: "Dashboard" }),
    ).toBeVisible();
  });
});

test.describe("slice 660 — gated route clean disabled state", () => {
  test("Board Packs direct-nav with a gated API renders the disabled panel", async ({
    authedPage: page,
  }) => {
    // The platform gate returns 404 + {"error":"feature disabled"} when
    // board.reporting is off. Route-mock the browser-side BFF call so the
    // spec is hermetic (does not depend on the shared seed's flag state).
    await page.route("**/api/board-packs", (route) =>
      route.fulfill({
        status: 404,
        contentType: "application/json",
        headers: { "X-Feature-Disabled": "board.reporting" },
        body: JSON.stringify({ error: "feature disabled" }),
      }),
    );
    await page.goto("/board-packs");
    const panel = page.getByTestId("feature-disabled-state");
    await expect(panel).toBeVisible();
    await expect(panel).toContainText("not enabled");
    // No raw error surface leaks through.
    await expect(page.getByText("feature disabled")).toHaveCount(0);
  });

  test("Vendor Claims direct-nav with a gated API renders the disabled panel", async ({
    authedPage: page,
  }) => {
    await page.route("**/api/oscal/component-definitions", (route) =>
      route.fulfill({
        status: 404,
        contentType: "application/json",
        headers: { "X-Feature-Disabled": "oscal.export" },
        body: JSON.stringify({ error: "feature disabled" }),
      }),
    );
    await page.goto("/oscal/component-definitions");
    const panel = page.getByTestId("feature-disabled-state");
    await expect(panel).toBeVisible();
    await expect(panel).toContainText("Vendor Claims is not enabled");
  });
});
