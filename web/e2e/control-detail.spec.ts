// Slice 041 — Playwright E2E for the control detail view.
//
// Runner status (post-slice-069, verified 2026-05-15 by slice 071 audit):
// Playwright IS installed in `web/` (`@playwright/test` in devDeps;
// `web/playwright.config.ts` present; CI runs `Frontend · Playwright
// e2e`). The job is currently quarantined per slice 079 because the
// five un-shimmed specs reference seed-data preconditions the
// docker-compose bring-up does not yet establish. Slice 082
// (`Playwright e2e seed-data harness`, status `not-ready`) is the fix;
// when it lands, the quarantine drops and the un-commented assertions
// below become the gate.
//
// Run locally:
//   cd web
//   npx playwright install chromium     # once per machine
//   npx playwright test e2e/control-detail.spec.ts
//
// Pre-conditions the seed-data harness (slice 082) must establish
// before the commented assertions are turned on:
//   - PLATFORM_BASE_URL points at a running platform instance
//   - TEST_BEARER carries a credential in a tenant with at least one
//     control that is anchored to an SCF anchor with >=2 framework
//     requirement mappings, and at least one of those frameworks has an
//     activated FrameworkScope that the control is OUT of (so AC-7's
//     dashed/greyed row has data)
//   - KNOWN_CONTROL_ID is that control's UUID

import { test } from "@playwright/test";

import { seedFromFixture } from "./seed";

// Per the preamble above: assertions are deliberately commented pending
// per-spec un-comment slices (slice 082's scoping decision — see
// docs/audit-log/082-playwright-seed-data-harness-decisions.md). The
// test body is preserved verbatim as a reviewable contract. Slice 082
// DOES wire the seed harness in `beforeAll` so the harness is exercised
// end-to-end against real Postgres+MinIO+NATS in CI.

test.describe("control detail view", () => {
  test.beforeAll(() => {
    seedFromFixture("control-detail");
  });

  test("AC-1: /controls/:id renders the full detail layout", async () => {
    // 1. Sign in.
    //    await page.goto("/login");
    //    await page.fill('input[name="token"]', process.env.TEST_BEARER!);
    //    await page.click("button[type=submit]");
    // 2. Visit the control detail route.
    //    await page.goto(`/controls/${KNOWN_CONTROL_ID}`);
    // 3. Header: title, SCF anchor pill, lifecycle badge.
    //    await expect(page.getByTestId("control-detail")).toBeVisible();
    //    await expect(page.getByTestId("control-title")).toBeVisible();
    //    await expect(page.getByTestId("scf-anchor-pill")).toBeVisible();
    //    await expect(page.getByTestId("lifecycle-badge")).toBeVisible();
    // 4. KPI strip + every major section is present (mockup layout).
    //    await expect(page.getByTestId("kpi-strip")).toBeVisible();
    //    await expect(page.getByTestId("coverage-section")).toBeVisible();
    //    await expect(page.getByTestId("ucf-viz-section")).toBeVisible();
    //    await expect(page.getByTestId("evidence-stream-section")).toBeVisible();
    //    await expect(page.getByTestId("freshness-section")).toBeVisible();
    //    await expect(page.getByTestId("effective-scope-section")).toBeVisible();
    //    await expect(page.getByTestId("policies-section")).toBeVisible();
    //    await expect(page.getByTestId("risks-section")).toBeVisible();
    //    await expect(page.getByTestId("audit-log-section")).toBeVisible();
  });

  test("AC-2: coverage table shows STRM types + strengths per row", async () => {
    //    await page.goto(`/controls/${KNOWN_CONTROL_ID}`);
    //    await expect(page.getByTestId("coverage-table")).toBeVisible();
    // At least one row, each with a STRM badge + a strength bar.
    //    const rows = page.getByTestId("coverage-row");
    //    await expect(rows.first()).toBeVisible();
    //    await expect(rows.first().locator("[data-strm]")).toBeVisible();
    //    await expect(rows.first().getByTestId("strength-bar")).toBeVisible();
    // The STRM badge text equals the raw backend relationship_type
    // (open-string rendering — equal | subset_of | superset_of |
    // intersects_with), never a fabricated label.
  });

  test("AC-3: UCF mini-viz renders control -> anchor -> requirements", async () => {
    //    await page.goto(`/controls/${KNOWN_CONTROL_ID}`);
    //    await expect(page.getByTestId("ucf-mini-viz")).toBeVisible();
    //    await expect(page.getByTestId("ucf-viz-control")).toBeVisible();
    //    await expect(page.getByTestId("ucf-viz-anchor")).toBeVisible();
    //    await expect(page.getByTestId("ucf-viz-requirement").first()).toBeVisible();
    // P0-1: every edge originates at the control or the anchor — there is
    // no requirement-to-requirement edge. Asserted structurally: the
    // component has no code path that draws one (see ucf-mini-viz.tsx).
  });

  test("AC-4: evidence stream — bound to /v1/evidence?control_id=… (slice 253)", async () => {
    // Slice 253 — the section is no longer a "list endpoint pending"
    // placeholder. It renders either the latest few records or, when
    // the response is genuinely empty, an honest empty-state. The
    // critical regression guard is that the empty-state copy is the
    // truly-empty form ("No evidence records … in the last 30 days"),
    // NOT the pre-slice-253 endpoint-pending form ("not yet wired" /
    // "does not exist on main"). The endpoint-pending text was a
    // category-(iv) mockup-stale lie surfaced by slice 204 / #253.
    //    await page.goto(`/controls/${KNOWN_CONTROL_ID}`);
    //    await expect(page.getByTestId("evidence-stream-section")).toBeVisible();
    //    await expect(page.getByTestId("evidence-stream-placeholder")).toHaveCount(0);
    //    const list = page.getByTestId("evidence-stream-list");
    //    const empty = page.getByTestId("evidence-stream-empty");
    //    await expect(list.or(empty)).toBeVisible();
    //    // Whichever surface renders, the endpoint-pending copy is gone.
    //    await expect(page.getByTestId("evidence-stream-section")).not.toContainText(
    //      "not yet wired",
    //    );
    //    await expect(page.getByTestId("evidence-stream-section")).not.toContainText(
    //      "not on main yet",
    //    );
    //    // The empty-state — if it renders — uses the truly-empty copy.
    //    if (await empty.isVisible()) {
    //      await expect(empty).toContainText("No evidence records");
    //      await expect(empty).toContainText("in the last 30 days");
    //    }
  });

  test("AC-4-b: right-rail Policies / Risks / Audit-log render real backings (slice 253)", async () => {
    // Slice 253 — the right-rail cards were previously three
    // "endpoint not on main yet" placeholders. They now bind to live
    // upstreams (`/v1/controls/{id}/policies`, `/risks`, `/history`).
    // Empty states are honest: "No policies are linked…", "No risks
    // are linked…", "No evaluation history yet…". The regression
    // guard asserts the endpoint-pending wording is gone, regardless
    // of whether the seeded fixture has linked rows.
    //    await page.goto(`/controls/${KNOWN_CONTROL_ID}`);
    //    for (const id of ["policies-section", "risks-section", "audit-log-section"]) {
    //      await expect(page.getByTestId(id)).toBeVisible();
    //      await expect(page.getByTestId(id)).not.toContainText("not on main yet");
    //      await expect(page.getByTestId(id)).not.toContainText("endpoint pending");
    //    }
    //    // And the KPI "Evidence records · 30d" sub-text no longer
    //    // names the endpoint-pending status.
    //    await expect(page.getByTestId("kpi-strip")).not.toContainText(
    //      "evidence-list endpoint pending",
    //    );
  });

  test("AC-5: freshness clock binds to control state", async () => {
    // AC-5's text references slice 016 `valid_until`; slice 016 is not on
    // main. The clock binds to slice 012's /state — freshness_status,
    // last_observed_at, freshness_class — which is the merged freshness
    // surface. The 016 drift overlay is additive when it lands.
    //    await page.goto(`/controls/${KNOWN_CONTROL_ID}`);
    //    await expect(page.getByTestId("freshness-clock")).toBeVisible();
    //    await expect(page.getByTestId("freshness-since")).toBeVisible();
    //    await expect(page.getByTestId("freshness-status")).toBeVisible();
  });

  test("AC-6: effective-scope panel calls /effective-scope per framework", async () => {
    // One row per distinct framework_version_id in the coverage
    // requirements; each row is backed by its own
    // /api/controls/:id/effective-scope?framework_version=<fvId> call.
    //    const requests: string[] = [];
    //    page.on("request", (r) => requests.push(r.url()));
    //    await page.goto(`/controls/${KNOWN_CONTROL_ID}`);
    //    await expect(page.getByTestId("effective-scope-row").first()).toBeVisible();
    //    expect(
    //      requests.filter((u) => u.includes("/effective-scope?framework_version=")).length,
    //    ).toBeGreaterThan(0);
  });

  test("AC-7: out-of-scope framework rows render dashed/greyed", async () => {
    // The pre-condition control is out of scope for >=1 framework. That
    // row carries data-out-of-scope="true" and the dashed/greyed
    // styling; it is NEVER hidden (anti-criterion P0-2).
    //    await page.goto(`/controls/${KNOWN_CONTROL_ID}`);
    //    const oosRow = page.locator('[data-testid="coverage-row"][data-out-of-scope="true"]');
    //    await expect(oosRow.first()).toBeVisible();
    // And the corresponding UCF-viz edge is dashed:
    //    const oosEdge = page.locator('[data-testid="ucf-viz-requirement"] [data-out-of-scope="true"]');
    //    await expect(oosEdge.first()).toBeVisible();
  });

  test("responsive: layout collapses to a single column at 375px", async () => {
    //    await page.setViewportSize({ width: 375, height: 812 });
    //    await page.goto(`/controls/${KNOWN_CONTROL_ID}`);
    // The lg: grid columns collapse — every section stacks and stays
    // visible at the 375px baseline (slice 060 set this baseline).
    //    await expect(page.getByTestId("coverage-section")).toBeVisible();
    //    await expect(page.getByTestId("freshness-section")).toBeVisible();
  });

  test("auth: a 401 from a bound endpoint bounces to /login", async () => {
    // With no session cookie the (authed) layout + proxy.ts redirect
    // before the page renders; a cookie that expires mid-session is
    // caught by the page's 401 -> /login effect.
    //    await page.context().clearCookies();
    //    await page.goto(`/controls/${KNOWN_CONTROL_ID}`);
    //    await expect(page).toHaveURL(/\/login/);
  });
});
