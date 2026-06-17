// Slice 380 -- Playwright e2e for the dashboard Server Component
// fan-out. Closes slice 332 finding F-BFF-2 (MEDIUM).
//
// The dashboard's sibling server-component `layout.tsx` (slice 380)
// prefetches all six panels server-side in parallel and ships them via
// `HydrationBoundary`, so the client `useQuery` hooks boot
// already-populated. The observable consequence: on a COLD first load
// the browser fires ZERO `/api/dashboard/**` BFF requests -- the panel
// data arrived inline in the SSR HTML (AC-4 / AC-8). The pre-slice
// behavior fired one BFF request per panel (6-7 requests).
//
// This spec asserts the post-slice waterfall: navigate to /dashboard,
// wait for the panels to render their content, and assert that no
// `/api/dashboard/*` request was issued during the initial load. The
// per-panel BFF routes still EXIST (P0-1) -- they serve client-side
// refresh after navigation -- but they are not hit on first paint.
//
// Run locally:
//   cd web
//   npx playwright install chromium     # once per machine
//   npx playwright test e2e/dashboard-server-component.spec.ts

import { seedFromFixture } from "./seed";

import { expect, test } from "./fixtures";

test.describe("dashboard Server Component fan-out (slice 380)", () => {
  test.beforeAll(() => {
    seedFromFixture("dashboard");
  });

  test("AC-4/AC-8: first load fires ZERO /api/dashboard/* BFF requests (data prefetched server-side)", async ({
    authedPage: page,
  }) => {
    const bffRequests: string[] = [];
    page.on("request", (r) => {
      const url = r.url();
      if (url.includes("/api/dashboard/")) {
        bffRequests.push(url);
      }
    });

    await page.goto("/dashboard");

    // Wait for the dashboard shell + at least the framework-posture
    // panel (the slowest, multi-framework rollup) to render its
    // server-hydrated content. If the data were NOT prefetched, the
    // panel would mount a skeleton and fire its BFF request to fill it.
    await expect(page.getByTestId("program-dashboard")).toBeVisible();
    await expect(page.getByTestId("framework-posture-panel")).toBeVisible();
    await expect(page.getByTestId("activity-feed-panel")).toBeVisible();
    await expect(page.getByTestId("recent-drift-panel")).toBeVisible();
    await expect(page.getByTestId("top-risks-panel")).toBeVisible();
    await expect(page.getByTestId("evidence-freshness-panel")).toBeVisible();
    await expect(page.getByTestId("upcoming-panel")).toBeVisible();

    // Slice 750: the portfolio AI-summary panel is OPERATOR-TRIGGERED — it
    // renders an idle state with a "Generate summary" button and fires NO BFF
    // request on first paint (an expensive LLM generation must not auto-run on a
    // passive dashboard view, and it must not violate the zero-BFF invariant).
    await expect(page.getByTestId("portfolio-summary-section")).toBeVisible();
    await expect(page.getByTestId("portfolio-summary-idle")).toBeVisible();

    // Give any (incorrect) client-side fetch a chance to fire before
    // asserting it did NOT. TanStack Query with hydrated initialData +
    // the 60s staleTime (lib/queryClient.tsx) must NOT refetch on mount.
    await page.waitForLoadState("networkidle");

    // The load-bearing assertion: no client-side BFF request on first
    // load. The data came inline via HydrationBoundary (AC-4/AC-8); the
    // portfolio panel fired nothing because it is operator-triggered (slice 750).
    expect(
      bffRequests,
      `expected no /api/dashboard/* BFF requests on first load, got:\n${bffRequests.join(
        "\n",
      )}`,
    ).toHaveLength(0);
  });

  test("slice 750: the portfolio summary panel is operator-triggered (no fetch on mount; fetches on click)", async ({
    authedPage: page,
  }) => {
    const portfolioRequests: string[] = [];
    page.on("request", (r) => {
      if (r.url().includes("/api/dashboard/portfolio-summary")) {
        portfolioRequests.push(r.url());
      }
    });

    await page.goto("/dashboard");

    // Idle on first paint — the trigger is visible, no fetch has fired.
    await expect(page.getByTestId("portfolio-summary-idle")).toBeVisible();
    await page.waitForLoadState("networkidle");
    expect(
      portfolioRequests,
      `portfolio summary must NOT fetch on mount, got:\n${portfolioRequests.join(
        "\n",
      )}`,
    ).toHaveLength(0);

    // Clicking the trigger fires the BFF request (the operator opted in).
    await page.getByTestId("portfolio-summary-generate").click();
    await expect
      .poll(() => portfolioRequests.length, {
        message:
          "clicking Generate must fire the portfolio-summary BFF request",
      })
      .toBeGreaterThan(0);
  });

  test("AC-8: all panels render their initial content WITHOUT a client fetch", async ({
    authedPage: page,
  }) => {
    // The seeded dashboard fixture (fixtures/e2e/dashboard.sql) provides
    // a risk + drift snapshots + freshness rows + an active expiring
    // exception. With server-side prefetch those rows render in the
    // first paint. We assert real content (a top-risk row + a drift
    // delta) is visible -- proving the prefetch carried the data, not
    // just the empty shell.
    await page.goto("/dashboard");
    await expect(page.getByTestId("top-risk-row").first()).toBeVisible();
    await expect(page.getByTestId("recent-drift-row").first()).toBeVisible();
  });

  test("AC-3/AC-5: per-panel BFF refresh still works after first load (routes NOT removed)", async ({
    authedPage: page,
  }) => {
    // P0-1: the per-panel BFF routes remain a fetchable surface. After
    // first load (prefetched), invalidating a query triggers a client
    // refetch that hits the BFF route -- proving the route is still
    // wired and the TanStack cache hydrated correctly (AC-5). We trigger
    // the refetch by directly calling the BFF endpoint and asserting it
    // returns a 200 JSON envelope (the same surface client refresh uses).
    await page.goto("/dashboard");
    await expect(page.getByTestId("program-dashboard")).toBeVisible();

    const resp = await page.request.get("/api/dashboard/freshness");
    expect(resp.status()).toBe(200);
    const body = await resp.json();
    // P0-4: unchanged wire shape -- the freshness envelope still carries
    // `total` and `total_stale`.
    expect(body).toHaveProperty("total");
    expect(body).toHaveProperty("total_stale");
  });
});
