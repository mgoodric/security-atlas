// #2095 — Playwright E2E: the metric detail chart has labelled, dated
// axes, and tiles colour only against a target.
//
// HERMETIC: route-mocks every BFF GET the pages hit for the asserted
// panels (the slice 594 shared-DB -> hermetic-mock convention), so the
// spec does not depend on the docker-compose seed.
//
// Neutral test strings only (P0-A9 / GitGuardian).

import { expect, test } from "./fixtures";

const DAY = 24 * 60 * 60 * 1000;

const METRIC = {
  id: "e2e_chart_metric",
  level: "board",
  category: "program",
  name: "Chart axes metric",
  description: "A higher-is-better percentage for the chart spec.",
  unit: "percent",
  cadence: "daily",
  compute_strategy: "computed",
  compute_evaluator: "e2e_chart_metric",
  source_slices: ["2095"],
};

function observation(daysAgo: number, value: string) {
  const at = new Date(Date.now() - daysAgo * DAY).toISOString();
  return {
    id: `00000000-0000-4000-8000-0000000020${String(daysAgo).padStart(2, "0")}`,
    metric_id: METRIC.id,
    observed_at: at,
    numeric_value: value,
    dimensions: {},
    source: "computed:e2e_chart_metric",
    created_at: at,
  };
}

// Newest first, as the API returns them. Rising from 85% to 92%.
const OBSERVATIONS = {
  observations: [
    observation(0, "0.92"),
    observation(30, "0.9"),
    observation(60, "0.85"),
  ],
  count: 3,
};

// The target sits below the current value.
const TARGET = {
  metric_id: METRIC.id,
  target_value: "0.8",
  warning_threshold: "0.7",
  critical_threshold: "0.6",
  direction: "higher_is_better",
};

const POSTURE = {
  frameworks: [
    {
      framework_id: "e2e-framework",
      framework_version: "E2E Framework 1.0",
      framework_version_id: "00000000-0000-4000-8000-000000002095",
      scope_set: true,
      // Low coverage falling: the old tile painted both red.
      coverage_pct: 42.5,
      covered_by_exception_pct: 0,
      freshness_composite: 80,
      trend_delta_90d: -3.5,
    },
  ],
  count: 1,
};

function json(body: unknown) {
  return {
    status: 200,
    contentType: "application/json",
    body: JSON.stringify(body),
  };
}

test.describe("charts and tiles (#2095)", () => {
  test.beforeEach(async ({ authedPage }) => {
    await authedPage.route("**/api/metrics?**", (route) =>
      route.fulfill(json({ metrics: [METRIC], count: 1 })),
    );
    await authedPage.route(`**/api/metrics/${METRIC.id}`, (route) =>
      route.fulfill(json({ metric: METRIC, parents: [], children: [] })),
    );
    await authedPage.route(
      `**/api/metrics/${METRIC.id}/observations**`,
      (route) => route.fulfill(json(OBSERVATIONS)),
    );
    await authedPage.route(`**/api/metrics/${METRIC.id}/target**`, (route) =>
      route.fulfill(json(TARGET)),
    );
    await authedPage.route("**/api/dashboard/framework-posture", (route) =>
      route.fulfill(json(POSTURE)),
    );
  });

  test("the metric detail chart shows labelled axes with dates", async ({
    authedPage,
  }) => {
    await authedPage.goto(`/dashboards/metrics/${METRIC.id}`);

    const chart = authedPage.getByTestId("metric-detail-chart-svg");
    await expect(chart).toBeVisible();
    await expect(
      authedPage.getByTestId("metric-detail-chart-svg-x-label"),
    ).toHaveText("Observed");
    await expect(
      authedPage.getByTestId("metric-detail-chart-svg-y-label"),
    ).toHaveText("Percent");

    const xTicks = authedPage.getByTestId("metric-detail-chart-svg-x-tick");
    await expect(xTicks).toHaveCount(4);
    for (const text of await xTicks.allTextContents()) {
      expect(text).toMatch(/^[A-Z][a-z]{2} \d{1,2}$/);
    }

    // The value axis is fitted to the data, in the metric's unit.
    const yTicks = await authedPage
      .getByTestId("metric-detail-chart-svg-y-tick")
      .allTextContents();
    expect(yTicks.length).toBeGreaterThanOrEqual(2);
    for (const text of yTicks) expect(text).toMatch(/%$/);
    expect(yTicks).toContain("90.0%");

    await expect(
      authedPage.getByTestId("metric-detail-chart-svg-target-line"),
    ).toBeAttached();
  });

  test("a posture tile with no target is neutral and says No target set", async ({
    authedPage,
  }) => {
    await authedPage.goto("/dashboard");

    const tile = authedPage
      .getByTestId("framework-tile")
      .filter({ hasText: "E2E Framework 1.0" });
    await expect(tile).toBeVisible();

    const noTarget = tile.getByTestId("framework-tile-no-target");
    await expect(noTarget).toHaveText("No target set");
    // Plain text: there is nowhere to set a posture target yet (#2341).
    await expect(tile.locator("a", { hasText: "No target set" })).toHaveCount(
      0,
    );

    const coverage = tile.getByTestId("framework-tile-coverage");
    await expect(coverage).toHaveAttribute("data-tone", "neutral");
    await expect(coverage).not.toHaveClass(/destructive|emerald|amber/);

    const trend = tile.getByTestId("framework-tile-trend");
    await expect(trend).toHaveAttribute("data-tone", "neutral");
    await expect(trend).not.toHaveClass(/destructive|emerald/);
    await expect(trend).toContainText("-3.5");
    await expect(trend).toHaveAttribute("aria-label", /no target set/);
  });

  test("a metric tile with a target below its value is not red", async ({
    authedPage,
  }) => {
    await authedPage.goto("/dashboards/metrics");

    const badge = authedPage.getByTestId(`board-metric-${METRIC.id}-badge`);
    await expect(badge).toHaveAttribute("data-threshold-color", "green");
    await expect(badge).not.toHaveAttribute("data-threshold-color", "red");

    const delta = authedPage.getByTestId(`board-metric-${METRIC.id}-delta`);
    await expect(delta).toHaveAttribute("data-tone", "improving");
    await expect(delta).not.toHaveClass(/destructive/);
    await expect(delta).toContainText("+7.0 pts");

    await expect(
      authedPage.getByTestId(`board-metric-${METRIC.id}-no-target`),
    ).toHaveCount(0);
  });

  test("a metric tile with no target is neutral and links to its target", async ({
    authedPage,
  }) => {
    await authedPage.route(`**/api/metrics/${METRIC.id}/target**`, (route) =>
      route.fulfill({
        status: 404,
        contentType: "application/json",
        body: JSON.stringify({ error: "not found" }),
      }),
    );
    await authedPage.goto("/dashboards/metrics");

    const badge = authedPage.getByTestId(`board-metric-${METRIC.id}-badge`);
    await expect(badge).toHaveAttribute("data-threshold-color", "neutral");

    const delta = authedPage.getByTestId(`board-metric-${METRIC.id}-delta`);
    await expect(delta).toHaveAttribute("data-tone", "neutral");

    const noTarget = authedPage.getByTestId(
      `board-metric-${METRIC.id}-no-target`,
    );
    await expect(noTarget).toHaveText("No target set");
    await expect(noTarget).toHaveAttribute(
      "href",
      `/dashboards/metrics/${METRIC.id}#metric-detail-target`,
    );
  });
});
