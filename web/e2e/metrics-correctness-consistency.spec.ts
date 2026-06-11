// Slice 677 — Playwright E2E pinning the three metric-correctness fixes
// (ATLAS-020 / 021 / 023).
//
// HERMETIC: route-mocks every BFF GET the dashboard + metrics surfaces
// hit so the spec does NOT depend on the shared docker-compose seed (per
// the slice 594 shared-DB → hermetic-mock convention). One fixed
// FreshnessReport is served to BOTH the dashboard widget and the metrics
// board KPI, so the spec can assert they render the SAME number — the
// reconciliation that closes ATLAS-020.
//
// Pins:
//   - ATLAS-020: evidence-freshness % is identical on the dashboard
//     widget (header subtitle) and the metrics-view board KPI card.
//   - ATLAS-021: the dashboard freshness panel labels its population as
//     "controls" (not "evidence records"), so it no longer reads as a
//     contradiction of the evidence ledger's record total.
//   - ATLAS-023: a board metric with NO target renders a NEUTRAL badge
//     ("no target"), never green "on target".
//
// Neutral test strings only (P0-A9 / GitGuardian).

import { expect, test } from "./fixtures";

// pctFromText pulls the integer percentage out of a rendered string using
// the supplied capture-group regex. The two surfaces format differently —
// the dashboard subtitle renders "evidence freshness 100% within window";
// the metrics KPI renders "100.0%" — so the regex differs per call site,
// but both reduce to the same integer percentage for the parity compare.
// Throws on no match so a silent loading/empty state can't pass as "0".
function pctFromText(text: string, re: RegExp): number {
  const m = text.match(re);
  if (!m) {
    throw new Error(`no percentage in rendered text: ${JSON.stringify(text)}`);
  }
  return Math.round(Number(m[1]));
}

// One canonical freshness report shared by both surfaces: 50 controls,
// 0 stale -> 100% within window. (The audit saw the dashboard at 100%
// and the metrics view at 0.0%; post-fix both read from this one report.)
const FRESHNESS_REPORT = {
  bucket: "class",
  buckets: [{ freshness_class: "monthly", total: 50, fresh: 50, stale: 0 }],
  total: 50,
  total_stale: 0,
};

// A board catalog with exactly the evidence-freshness KPI. No target is
// configured (the 404-as-null path), so its badge must be neutral.
const BOARD_METRICS = {
  metrics: [
    {
      id: "evidence_freshness_pct",
      level: "board",
      category: "posture",
      name: "Evidence freshness",
      description: "Fraction of controls whose latest evidence is in-window.",
      unit: "percent",
      cadence: "realtime",
      compute_strategy: "computed",
      source_slices: ["016"],
    },
  ],
  count: 1,
};

test.describe("metrics correctness consistency (slice 677)", () => {
  test.beforeEach(async ({ authedPage, baseURL }) => {
    // Slice 380: the /dashboard route prefetches its six panels (incl.
    // freshness) SERVER-SIDE in `dashboard/layout.tsx` (`Promise.all` ->
    // HydrationBoundary), so the client `useQuery` hooks boot already-
    // populated and fire NO `/api/dashboard/*` request on first load.
    // A Playwright `authedPage.route(...)` mock is a BROWSER-side intercept
    // and cannot reach that SSR fetch — so without this cookie the
    // dashboard renders the REAL seeded freshness, not our mock. The
    // test-only `e2e_no_prefetch` cookie (honored only under
    // ATLAS_TEST_MODE=1, per `dashboard-prefetch.ts serverPrefetchBypassed`)
    // tells the layout to SKIP the SSR prefetch so the page falls back to
    // its pure client-side `useQuery` path, where our route mocks apply.
    // This is the same seam `e2e/dashboard.spec.ts` uses. (The
    // `/dashboards/metrics` surface is a pure client component with NO
    // prefetch layout, so its mocks already applied — but the cookie is
    // harmless there.)
    await authedPage.context().addCookies([
      {
        name: "e2e_no_prefetch",
        value: "1",
        url: baseURL ?? "http://localhost:3000",
      },
    ]);

    // Shared across dashboard + metrics: the freshness read model.
    await authedPage.route("**/api/dashboard/freshness**", (route) =>
      route.fulfill({
        status: 200,
        contentType: "application/json",
        body: JSON.stringify(FRESHNESS_REPORT),
      }),
    );
    // The metrics board catalog (level=board).
    await authedPage.route("**/api/metrics?**", (route) =>
      route.fulfill({
        status: 200,
        contentType: "application/json",
        body: JSON.stringify(BOARD_METRICS),
      }),
    );
    // No target configured -> the BFF returns null (404 upstream).
    await authedPage.route(
      "**/api/metrics/evidence_freshness_pct/target**",
      (route) =>
        route.fulfill({
          status: 200,
          contentType: "application/json",
          body: "null",
        }),
    );
    // No stored observations -> the headline value must come from live
    // freshness, NOT the (empty) observation series. This is the crux of
    // the ATLAS-020 fix: even with zero stored observations the card
    // shows the live 100%, matching the dashboard.
    await authedPage.route(
      "**/api/metrics/evidence_freshness_pct/observations**",
      (route) =>
        route.fulfill({
          status: 200,
          contentType: "application/json",
          body: JSON.stringify({ observations: [], count: 0 }),
        }),
    );
  });

  test("ATLAS-020: freshness % is identical on the dashboard widget and the metrics KPI", async ({
    authedPage,
  }) => {
    // The REAL job of this test is PARITY: the dashboard freshness widget
    // and the metrics-view board KPI must render the SAME number for one
    // tenant (the audit found 100% vs 0.0%). We assert equality of the two
    // extracted percentages rather than hardcoding a literal — so the test
    // stays green whether the value is the mocked 100% (route mock applied)
    // or, if the mock boundary ever shifts again, the real seeded value.
    // A hardcoded "100%" or "50%" would be as brittle as the bug we fixed.
    // With the mock + the no-prefetch cookie in place the value is the
    // mocked 100%, asserted as the lower bound below.

    // Dashboard surface: the header subtitle renders the live freshness %.
    await authedPage.goto("/dashboard");
    const subtitle = authedPage.getByTestId("dashboard-header-subtitle");
    await expect(subtitle).toBeVisible();
    // Wait for a concrete "<n>% within window" before scraping (guards
    // against asserting on the loading/empty state).
    await expect(subtitle).toContainText(/\d+% within window/);
    const dashboardPct = pctFromText(
      (await subtitle.textContent()) ?? "",
      /(\d+)% within window/,
    );

    // Metrics surface: the evidence-freshness board KPI reads the SAME
    // live source, so its headline value must be the same number — NOT a
    // stale 0.0% snapshot (the contradiction the audit found).
    await authedPage.goto("/dashboards/metrics");
    const kpiValue = authedPage.getByTestId(
      "board-metric-evidence_freshness_pct-value",
    );
    await expect(kpiValue).toBeVisible();
    await expect(kpiValue).toHaveText(/\d+(\.\d+)?%/);
    const kpiPct = pctFromText((await kpiValue.textContent()) ?? "", /(\d+)/);

    // PARITY (the assertion that closes ATLAS-020): both surfaces agree.
    expect(kpiPct).toBe(dashboardPct);
    // And with the mock applied that shared value is 100 (the live read
    // model in the mock is all-fresh) — a positive check that the surfaces
    // are reading the mocked live source, not a stale 0.0% snapshot.
    expect(dashboardPct).toBe(100);
  });

  test("ATLAS-021: the freshness panel labels its population as controls, not records", async ({
    authedPage,
  }) => {
    await authedPage.goto("/dashboard");
    const staleTotal = authedPage.getByTestId("evidence-freshness-stale-total");
    await expect(staleTotal).toBeVisible();
    // Population is controls; it explicitly disclaims being ledger records.
    await expect(staleTotal).toContainText("controls");
    await expect(staleTotal).not.toContainText("evidence records are past");
  });

  test("ATLAS-023: a no-target board metric renders a NEUTRAL badge, never green", async ({
    authedPage,
  }) => {
    await authedPage.goto("/dashboards/metrics");
    const badge = authedPage.getByTestId(
      "board-metric-evidence_freshness_pct-badge",
    );
    await expect(badge).toBeVisible();
    // The badge color is neutral (muted) — NOT green "on target".
    await expect(badge).toHaveAttribute("data-threshold-color", "neutral");
    await expect(badge).toContainText("no target");
    await expect(badge).not.toContainText("on target");
  });
});
