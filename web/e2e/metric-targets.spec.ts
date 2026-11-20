// #2341 — Playwright E2E: setting a metric target from its detail page.
//
// HERMETIC, like charts-and-tiles.spec.ts: every BFF call the page makes
// for the metric is route-mocked, so the spec does not depend on the
// docker-compose seed and never writes a target into the shared database.
// The target route is stateful: GET answers 404 until the dialog's PUT,
// then returns what was sent, so the assertions read the round-trip.
//
//   1. an admin sees Set target, the dialog refuses misordered and
//      out-of-range percentages, and saving 80 / 70 / 60 % (below the 92%
//      value) sends 0.8 / 0.7 / 0.6 and turns the badge green, never red;
//      reopened, the dialog shows the percentages again;
//   2. a viewer sees no Set target, and the board card's "No target set"
//      is text, not a link to the form;
//   3. a grc_engineer sees Set target (#2572).
//
// Neutral test strings only (P0-A9 / GitGuardian).

import type { Page } from "@playwright/test";

import { describeAxe, seriousAxeViolations } from "./axe-gate";
import { expect, test } from "./fixtures";

const DAY = 24 * 60 * 60 * 1000;

const METRIC = {
  id: "e2e_target_metric",
  level: "board",
  category: "program",
  name: "Target form metric",
  description: "A higher-is-better percentage for the target-form spec.",
  unit: "percent",
  cadence: "daily",
  compute_strategy: "computed",
  compute_evaluator: "e2e_target_metric",
  source_slices: ["2341"],
};

function observation(daysAgo: number, value: string) {
  const at = new Date(Date.now() - daysAgo * DAY).toISOString();
  return {
    id: `00000000-0000-4000-8000-0000000023${String(daysAgo).padStart(2, "0")}`,
    metric_id: METRIC.id,
    observed_at: at,
    numeric_value: value,
    dimensions: {},
    source: "computed:e2e_target_metric",
    created_at: at,
  };
}

const OBSERVATIONS = {
  observations: [observation(0, "0.92"), observation(30, "0.9")],
  count: 2,
};

// The viewer's /api/me, served from a fixture. Fetching the real profile
// and rewriting it raced test teardown (#2589).
const VIEWER_ME = {
  user_id: "00000000-0000-4000-8000-000000002341",
  tenant_id: "e5e5e5e5-0000-4000-8000-000000002341",
  display_name: "Vera Viewer",
  email: "viewer@example.test",
  idp_subject: "",
  tenant_role: "viewer",
  time_zone: null,
  theme: null,
  is_admin: false,
  is_tenant_admin: false,
  is_approver: false,
  has_tenant_role: true,
  owner_roles: ["viewer"],
  roles: ["viewer"],
};

// #2572: a grc_engineer's /api/me. is_approver, not is_tenant_admin.
const GRC_ENGINEER_ME = {
  ...VIEWER_ME,
  display_name: "Gail GRC",
  email: "grc@example.test",
  tenant_role: "grc_engineer",
  is_approver: true,
  owner_roles: ["grc_engineer"],
  roles: ["grc_engineer"],
};

function json(body: unknown, status = 200) {
  return {
    status,
    contentType: "application/json",
    body: JSON.stringify(body),
  };
}

// mockMetric wires the metric's routes and returns the PUT bodies seen.
async function mockMetric(page: Page): Promise<unknown[]> {
  const puts: unknown[] = [];
  let saved: Record<string, unknown> | null = null;
  await page.route("**/api/metrics?**", (route) =>
    route.fulfill(json({ metrics: [METRIC], count: 1 })),
  );
  await page.route(`**/api/metrics/${METRIC.id}`, (route) =>
    route.fulfill(json({ metric: METRIC, parents: [], children: [] })),
  );
  await page.route(`**/api/metrics/${METRIC.id}/observations**`, (route) =>
    route.fulfill(json(OBSERVATIONS)),
  );
  await page.route(`**/api/metrics/${METRIC.id}/target**`, (route) => {
    const req = route.request();
    if (req.method() === "PUT") {
      const body = req.postDataJSON() as Record<string, unknown>;
      puts.push(body);
      // The platform echoes NUMERIC columns as strings.
      saved = {
        metric_id: METRIC.id,
        direction: body.direction,
        target_value: String(body.target_value),
        warning_threshold: String(body.warning_threshold),
        critical_threshold: String(body.critical_threshold),
      };
      return route.fulfill(json(saved));
    }
    return saved
      ? route.fulfill(json(saved))
      : route.fulfill(json({ error: "no target set" }, 404));
  });
  return puts;
}

test.describe("metric targets (#2341)", () => {
  // A route handler still in flight when the test ends fails the whole
  // run outside any test, where retries cannot reach it (#2589).
  test.afterEach(async ({ page }) => {
    await page.unrouteAll({ behavior: "ignoreErrors" });
  });

  test("an admin sets a target below the value and the badge is not red", async ({
    authedPage: page,
  }) => {
    const puts = await mockMetric(page);
    await page.goto(`/dashboards/metrics/${METRIC.id}`);

    const badge = page.getByTestId("metric-detail-badge");
    await expect(badge).toHaveAttribute("data-threshold-color", "neutral");

    const setTarget = page.getByTestId("metric-target-set");
    await expect(setTarget).toHaveText("Set target");
    await setTarget.click();
    const dialog = page.getByTestId("metric-target-dialog");
    await expect(dialog).toBeVisible();
    const findings = await seriousAxeViolations(page);
    expect(findings, describeAxe(findings)).toEqual([]);

    // A percent metric takes percentages, refused outside 0..100 and out
    // of order, and saves them as the fractions its observations use.
    await dialog.getByLabel("Direction").selectOption("higher_is_better");
    await dialog.getByLabel("Target (%)").fill("80");
    await dialog.getByLabel("Warning threshold (%)").fill("90");
    await dialog.getByLabel("Critical threshold (%)").fill("60");
    await expect(dialog.getByTestId("metric-target-problem")).toContainText(
      "critical ≤ warning ≤ target",
    );
    await expect(
      dialog.getByRole("button", { name: "Save target" }),
    ).toBeDisabled();
    await dialog.getByLabel("Warning threshold (%)").fill("170");
    await expect(dialog.getByTestId("metric-target-problem")).toHaveText(
      "Warning must be between 0 and 100.",
    );

    await dialog.getByLabel("Warning threshold (%)").fill("70");
    await dialog.getByRole("button", { name: "Save target" }).click();
    await expect(dialog).toBeHidden();

    expect(puts).toEqual([
      {
        direction: "higher_is_better",
        target_value: 0.8,
        warning_threshold: 0.7,
        critical_threshold: 0.6,
      },
    ]);
    await expect(badge).toHaveAttribute("data-threshold-color", "green");
    await expect(badge).not.toHaveAttribute("data-threshold-color", "red");
    await expect(page.getByTestId("metric-detail-target")).toContainText(
      "80.0%",
    );
    await expect(setTarget).toHaveText("Edit target");

    // Reopened, the saved fractions show as percentages again.
    await setTarget.click();
    await expect(dialog.getByLabel("Target (%)")).toHaveValue("80");
    await expect(dialog.getByLabel("Warning threshold (%)")).toHaveValue("70");
    await expect(dialog.getByLabel("Critical threshold (%)")).toHaveValue("60");
  });

  test("a viewer cannot set a target and is not linked to the form", async ({
    authedPage: page,
  }) => {
    await mockMetric(page);
    await page.route("**/api/me", (route) => {
      if (route.request().method() !== "GET") return route.fallback();
      return route.fulfill(json(VIEWER_ME));
    });
    await page.route("**/api/admin/me", (route) =>
      route.fulfill(json({ is_admin: false })),
    );

    await page.goto(`/dashboards/metrics/${METRIC.id}`);
    await expect(page.getByTestId("metric-detail-target")).toContainText(
      "No target set",
    );
    await expect(page.getByTestId("metric-target-set")).toHaveCount(0);

    await page.goto("/dashboards/metrics");
    const noTarget = page.getByTestId(`board-metric-${METRIC.id}-no-target`);
    await expect(noTarget).toHaveText("No target set");
    await expect(noTarget).not.toHaveAttribute("href");
  });

  // #2572: a grc_engineer (an approver, not a tenant admin) may set a
  // metric target, as it sets a framework posture target.
  test("a grc_engineer is offered Set target", async ({ authedPage: page }) => {
    await mockMetric(page);
    await page.route("**/api/me", (route) => {
      if (route.request().method() !== "GET") return route.fallback();
      return route.fulfill(json(GRC_ENGINEER_ME));
    });
    await page.route("**/api/admin/me", (route) =>
      route.fulfill(json({ is_admin: false })),
    );

    await page.goto(`/dashboards/metrics/${METRIC.id}`);
    await expect(page.getByTestId("metric-target-set")).toBeVisible();
  });
});
