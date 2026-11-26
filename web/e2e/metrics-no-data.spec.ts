// #2064 — Playwright E2E pinning that a metric computed with no inputs
// reads as "No data", never as a pass.
//
// HERMETIC: route-mocks every BFF GET the metrics dashboard and detail
// page hit, so the spec does not depend on the shared docker-compose seed
// (the slice 594 shared-DB -> hermetic-mock convention). The one stored
// observation is what the critical-findings SLA evaluator writes when no
// findings exist: the empty marker plus a reason. Before #2064 it wrote
// 1.0, which both surfaces rendered as "100.0%"; with a target set, the
// badge also went green "on target".
//
// Neutral test strings only (P0-A9 / GitGuardian).

import { expect, test } from "./fixtures";

const METRIC = {
  id: "critical_findings_sla",
  level: "board",
  category: "audit",
  name: "Critical findings SLA",
  description: "Fraction of P0/P1 findings closed inside their SLA window.",
  unit: "percent",
  cadence: "realtime",
  compute_strategy: "computed",
  compute_evaluator: "critical_findings_sla",
  source_slices: ["076"],
};

const REASON = "No critical findings recorded in the last 90 days";

const OBSERVATIONS = {
  observations: [
    {
      id: "00000000-0000-4000-8000-000000002064",
      metric_id: METRIC.id,
      observed_at: new Date().toISOString(),
      numeric_value: "0",
      dimensions: {
        sample: "empty",
        no_data_reason: REASON,
        window_days: "90",
        v1_degraded: "no_severity_band_column",
      },
      source: "computed:critical_findings_sla",
      created_at: new Date().toISOString(),
    },
  ],
  count: 1,
};

// A target is set, so a placeholder value would be graded against it.
const TARGET = {
  metric_id: METRIC.id,
  target_value: "0.95",
  warning_threshold: "0.9",
  critical_threshold: "0.8",
  direction: "higher_is_better",
};

test.describe("metrics with no data (#2064)", () => {
  test.beforeEach(async ({ authedPage }) => {
    await authedPage.route("**/api/metrics?**", (route) =>
      route.fulfill({
        status: 200,
        contentType: "application/json",
        body: JSON.stringify({ metrics: [METRIC], count: 1 }),
      }),
    );
    await authedPage.route(`**/api/metrics/${METRIC.id}`, (route) =>
      route.fulfill({
        status: 200,
        contentType: "application/json",
        body: JSON.stringify({ metric: METRIC, parents: [], children: [] }),
      }),
    );
    await authedPage.route(
      `**/api/metrics/${METRIC.id}/observations**`,
      (route) =>
        route.fulfill({
          status: 200,
          contentType: "application/json",
          body: JSON.stringify(OBSERVATIONS),
        }),
    );
    await authedPage.route(`**/api/metrics/${METRIC.id}/target**`, (route) =>
      route.fulfill({
        status: 200,
        contentType: "application/json",
        body: JSON.stringify(TARGET),
      }),
    );
  });

  test("the SLA metric with no findings renders No data on the metrics page", async ({
    authedPage,
  }) => {
    await authedPage.goto("/dashboards/metrics");

    const value = authedPage.getByTestId(`board-metric-${METRIC.id}-value`);
    await expect(value).toHaveText("No data");
    await expect(value).not.toContainText("%");
    await expect(
      authedPage.getByTestId(`board-metric-${METRIC.id}-no-data-reason`),
    ).toHaveText(REASON);

    const badge = authedPage.getByTestId(`board-metric-${METRIC.id}-badge`);
    await expect(badge).toHaveAttribute("data-threshold-color", "neutral");
    await expect(badge).toHaveText("no data");
  });

  test("the SLA metric with no findings renders No data on its detail page", async ({
    authedPage,
  }) => {
    await authedPage.goto(`/dashboards/metrics/${METRIC.id}`);

    const latest = authedPage.getByTestId("metric-detail-latest");
    await expect(latest).toContainText(`No data — ${REASON}`);
    await expect(latest).not.toContainText("Latest");

    const badge = authedPage.getByTestId("metric-detail-badge");
    await expect(badge).toHaveAttribute("data-threshold-color", "neutral");
    await expect(badge).toHaveText("no data");
  });
});
