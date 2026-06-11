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
  test.beforeEach(async ({ authedPage }) => {
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
    // Dashboard surface: the header subtitle renders the live freshness %.
    await authedPage.goto("/dashboard");
    const subtitle = authedPage.getByTestId("dashboard-header-subtitle");
    await expect(subtitle).toBeVisible();
    await expect(subtitle).toContainText("100% within window");

    // Metrics surface: the evidence-freshness board KPI reads the SAME
    // live source, so its headline value is the same 100% — NOT a stale
    // 0.0% snapshot (the contradiction the audit found).
    await authedPage.goto("/dashboards/metrics");
    const kpiValue = authedPage.getByTestId(
      "board-metric-evidence_freshness_pct-value",
    );
    await expect(kpiValue).toBeVisible();
    await expect(kpiValue).toHaveText("100.0%");
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
