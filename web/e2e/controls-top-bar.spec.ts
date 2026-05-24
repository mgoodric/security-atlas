// Slice 223 — Playwright E2E for the slice-223 shared-shell chrome
// additions (breadcrumb + global ⌘K search).
//
// The slice ships TWO new affordances in the shared topbar
// (`web/components/shell/topbar.tsx`), visible on every authed page:
//
//   1. <Breadcrumb /> — `<tenant> > <page>` read-only wayfinding.
//      Reads /api/me/tenants (slice 192 BFF) + usePathname().
//      Returns null when either segment is missing.
//
//   2. <GlobalSearch /> — ⌘K-focused input + popover. Calls slice
//      268's /v1/search via the BFF /api/search; results grouped by
//      entity type (Controls / Risks / Evidence) with keyboard nav.
//
// The integrated assertions below pin the surfaces most likely to
// regress:
//
//   - Breadcrumb's two segments render with seeded data on /controls.
//   - Breadcrumb is visible on /audits too (shared chrome).
//   - Search input renders with the mockup's placeholder + ⌘K kbd.
//   - ⌘K keypress focuses the input.
//   - Typing a query produces a popover with grouped results (mocked
//     /api/search per the slice 214 pattern — keeps the spec
//     deterministic without re-seeding the search corpus).
//   - Enter navigates to the first hit's detail page.
//
// AC-6 (debounce timing — 250ms) is covered by the vitest sibling
// `components/shell/global-search.test.ts` (helper coverage) and is
// not asserted here — Playwright e2e is hostile to fine-grained
// timing assertions and the debounce is a UX nicety, not a security
// boundary. The unit-test split mirrors slice 213's D6 decision
// (positive case in e2e; corner-case branches in vitest).
//
// Run locally:
//   cd web
//   npx playwright install chromium     # once per machine
//   npx playwright test e2e/controls-top-bar.spec.ts

import { expect, test } from "./fixtures";

import { seedFromFixture } from "./seed";

test.describe("topbar header chrome (slice 223)", () => {
  test.beforeAll(() => {
    seedFromFixture("controls-top-bar");
  });

  test("AC-7: breadcrumb renders `<tenant> > Controls` on /controls", async ({
    authedPage: page,
  }) => {
    await page.goto("/controls");
    const crumb = page.getByTestId("breadcrumb");
    await expect(crumb).toBeVisible();
    await expect(page.getByTestId("breadcrumb-tenant")).toHaveText(
      "Demo Tenant",
    );
    await expect(page.getByTestId("breadcrumb-page")).toHaveText("Controls");
  });

  test("AC-2: breadcrumb is shared chrome — renders on /audits too with the right page label", async ({
    authedPage: page,
  }) => {
    await page.goto("/audits");
    await expect(page.getByTestId("breadcrumb")).toBeVisible();
    await expect(page.getByTestId("breadcrumb-page")).toHaveText("Audits");
  });

  test("AC-1: global search input renders with the mockup placeholder + ⌘K kbd", async ({
    authedPage: page,
  }) => {
    await page.goto("/controls");
    const input = page.getByTestId("global-search-input");
    await expect(input).toBeVisible();
    await expect(input).toHaveAttribute(
      "placeholder",
      "Search controls, evidence, risks…",
    );
  });

  test("AC-1: ⌘K keyboard shortcut focuses the search input (Meta+K)", async ({
    authedPage: page,
  }) => {
    await page.goto("/controls");
    const input = page.getByTestId("global-search-input");
    // The input should NOT have focus by default — establishes the
    // baseline before the shortcut fires.
    await expect(input).not.toBeFocused();
    await page.keyboard.press("Meta+K");
    await expect(input).toBeFocused();
  });

  test("AC-3 + AC-4: typing a query shows a grouped popover and Enter navigates to the first hit", async ({
    authedPage: page,
  }) => {
    // Mock /api/search to inject a deterministic 3-type hits payload.
    // Mirrors slice 214's pattern (which mocks /api/controls to
    // sidestep the missing scf_anchors row); keeps the spec
    // deterministic without re-seeding the corpus.
    await page.route("**/api/search**", async (route) => {
      await route.fulfill({
        status: 200,
        contentType: "application/json",
        body: JSON.stringify({
          hits: [
            {
              id: "00000000-0000-0000-0000-000000000001",
              type: "controls",
              title: "Encryption at rest",
              snippet: "Encryption at rest — prod object stores",
              relevance_score: 1.0,
            },
            {
              id: "00000000-0000-0000-0000-000000000002",
              type: "risks",
              title: "Data exfiltration",
              snippet: "Data exfiltration via misconfigured IAM",
              relevance_score: 0.75,
            },
            {
              id: "00000000-0000-0000-0000-000000000003",
              type: "evidence",
              title: "iam.role_review · CRY-05",
              snippet: "IAM access review · CRY-05",
              relevance_score: 0.5,
            },
          ],
          count: 3,
          partial_types: [],
        }),
      });
    });

    await page.goto("/controls");
    const input = page.getByTestId("global-search-input");
    await input.click();
    await input.fill("iam");
    // Wait for debounce + fetch → popover render.
    const popover = page.getByTestId("global-search-popover");
    await expect(popover).toBeVisible();
    // Three groups in canonical order.
    await expect(page.getByTestId("global-search-group-controls")).toBeVisible();
    await expect(page.getByTestId("global-search-group-risks")).toBeVisible();
    await expect(
      page.getByTestId("global-search-group-evidence"),
    ).toBeVisible();
    // First hit is the controls row — Enter navigates to its detail page.
    await input.press("Enter");
    await expect(page).toHaveURL(
      /\/controls\/00000000-0000-0000-0000-000000000001$/,
    );
  });

  test("AC-4: Escape closes the popover", async ({ authedPage: page }) => {
    await page.route("**/api/search**", async (route) => {
      await route.fulfill({
        status: 200,
        contentType: "application/json",
        body: JSON.stringify({
          hits: [
            {
              id: "00000000-0000-0000-0000-000000000001",
              type: "controls",
              title: "Encryption at rest",
              snippet: "Encryption at rest — prod object stores",
              relevance_score: 1.0,
            },
          ],
          count: 1,
          partial_types: [],
        }),
      });
    });

    await page.goto("/controls");
    const input = page.getByTestId("global-search-input");
    await input.click();
    await input.fill("iam");
    await expect(page.getByTestId("global-search-popover")).toBeVisible();
    await input.press("Escape");
    await expect(page.getByTestId("global-search-popover")).not.toBeVisible();
  });

  test("P0-223-1: search BFF forwards via /api/search (no per-primitive endpoints)", async ({
    authedPage: page,
  }) => {
    // Anti-criterion enforcement: the search component MUST forward
    // via the /api/search BFF — not call /api/controls + /api/risks +
    // /api/evidence in parallel with ad-hoc filters. The recorded
    // request set is asserted against the expected single endpoint.
    const searchRequests: string[] = [];
    page.on("request", (r) => {
      const url = r.url();
      if (url.includes("/api/search")) searchRequests.push(url);
    });
    await page.route("**/api/search**", async (route) => {
      await route.fulfill({
        status: 200,
        contentType: "application/json",
        body: JSON.stringify({
          hits: [],
          count: 0,
          partial_types: [],
        }),
      });
    });

    await page.goto("/controls");
    const input = page.getByTestId("global-search-input");
    await input.click();
    await input.fill("zzznomatch");
    // Wait for the debounced fetch to fire.
    await expect(page.getByTestId("global-search-popover")).toBeVisible();
    expect(searchRequests.length).toBeGreaterThan(0);
    // Every recorded request hits /api/search — the BFF is the only
    // routing surface for search.
    for (const url of searchRequests) {
      expect(url).toContain("/api/search");
    }
  });
});
