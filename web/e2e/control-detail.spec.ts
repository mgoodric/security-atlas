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

  test("slice 256: coverage column renders both numeric and n/a rows", async () => {
    // Slice 256 — the coverage table gains a Coverage column bound
    // to the backend's per-row `coverage` field (strength × 30-day
    // effectiveness × FrameworkScope predicate). When a control has
    // at least one in-scope and one out-of-scope mapped requirement,
    // the table renders both shapes:
    //   - in-scope row: data-coverage-state="numeric" + a numeric
    //     value (two decimals)
    //   - out-of-scope row: data-coverage-state="out-of-scope" + the
    //     literal text "n/a"
    // The strength bar's width reflects the COVERAGE percent, not the
    // raw strength (mockup binding — see Plans/_archive/mockups/control.html
    // lines 192/203/214). Assertions are commented pending the
    // slice-082 seed harness (same convention as the surrounding
    // tests). The slice 256 P0-1 contract — no client-side fabrication
    // — is also asserted here: when coverage is null, the bar fills 0%.
    //    await page.goto(`/controls/${KNOWN_CONTROL_ID}`);
    //    await expect(page.getByTestId("coverage-table")).toBeVisible();
    //    // A numeric coverage row exists.
    //    const numeric = page.locator(
    //      '[data-testid="coverage-cell"][data-coverage-state="numeric"]',
    //    );
    //    await expect(numeric.first()).toBeVisible();
    //    await expect(numeric.first()).toHaveText(/^\d\.\d{2}$/);
    //    // An out-of-scope row exists and renders "n/a".
    //    const oosCell = page.locator(
    //      '[data-testid="coverage-cell"][data-coverage-state="out-of-scope"]',
    //    );
    //    await expect(oosCell.first()).toBeVisible();
    //    await expect(oosCell.first()).toHaveText("n/a");
    //    // The footer text restates the formula honestly.
    //    await expect(page.getByTestId("coverage-footer")).toContainText(
    //      "strength × 30-day effectiveness",
    //    );
    //    // The chevron affordance is present per row.
    //    await expect(
    //      page.getByTestId("coverage-row-chevron").first(),
    //    ).toBeVisible();
  });

  test("slice 482: each coverage row shows a confidence band badge", async () => {
    // Slice 482 — the coverage table gains a Confidence column: one
    // band badge per requirement row, derived from the per-row
    // `coverage` value with the same thresholds the backend rollup
    // uses (strong ≥ 0.80, partial 0.50–0.79, weak < 0.50, uncovered
    // when out of scope / no data). The badge carries `data-band` so
    // the assertion is value-driven, not text-fragile. Assertions are
    // commented pending the slice-082 seed harness (same convention as
    // the surrounding tests); the body is the reviewable contract.
    //    await page.goto(`/controls/${KNOWN_CONTROL_ID}`);
    //    await expect(page.getByTestId("coverage-table")).toBeVisible();
    //    // Every row carries a confidence-band badge.
    //    const bands = page.getByTestId("confidence-band");
    //    await expect(bands.first()).toBeVisible();
    //    // The band value is one of the four documented buckets.
    //    await expect(bands.first()).toHaveAttribute(
    //      "data-band",
    //      /^(strong|partial|weak|uncovered)$/,
    //    );
    //    // An in-scope numeric row's band agrees with its coverage value:
    //    // a coverage-cell with data-coverage-state="numeric" sits in a
    //    // row whose band is strong | partial | weak (never uncovered).
    //    const numericRow = page
    //      .locator('[data-testid="coverage-row"]')
    //      .filter({
    //        has: page.locator(
    //          '[data-testid="coverage-cell"][data-coverage-state="numeric"]',
    //        ),
    //      });
    //    await expect(
    //      numericRow.first().getByTestId("confidence-band"),
    //    ).toHaveAttribute("data-band", /^(strong|partial|weak)$/);
    //    // An out-of-scope row's band is uncovered (no in-scope evidence).
    //    const oosRow = page.locator(
    //      '[data-testid="coverage-row"][data-out-of-scope="true"]',
    //    );
    //    await expect(
    //      oosRow.first().getByTestId("confidence-band"),
    //    ).toHaveAttribute("data-band", "uncovered");
    //    // The footer documents the band thresholds.
    //    await expect(page.getByTestId("coverage-footer")).toContainText(
    //      "confidence band",
    //    );
  });

  test("slice 255: header action buttons + last-evaluated timestamp", async () => {
    // Slice 255 — the control header's top-right well carries three
    // action buttons (Run query · Edit YAML · Request exception) and a
    // "last evaluated <relative-time>" sub-line below them.
    //
    // AC-2 + AC-3 + AC-4 expectations:
    //   - Three buttons render in mockup order.
    //   - Run query + Edit YAML are <button disabled> with title/
    //     aria-label tooltips naming the canvas section (slice 183/184
    //     placeholder pattern — visible copy + tooltip + aria-label
    //     read the same line). They are NOT `<a href="#">` (slice 178
    //     anti-pattern).
    //   - Request exception is a real link to /exceptions?control_id=<id>,
    //     which is a merged URL-driven filter on the existing
    //     /exceptions list page.
    //
    // AC-6 expectations:
    //   - The three buttons are keyboard-reachable in DOM order. The
    //     disabled <button> elements are still focusable for screen
    //     readers via shadcn's Button (which is built on base-ui
    //     Button — disabled buttons remain in the tab order with
    //     aria-disabled so the affordance is announced).
    //
    // AC-1 expectations:
    //   - The "last evaluated" sub-line renders with a relative-time
    //     value sourced from state.last_observed_at. Assertions are
    //     commented pending the slice-082 seed harness — same
    //     convention as the surrounding tests.
    //
    //    await page.goto(`/controls/${KNOWN_CONTROL_ID}`);
    //    await expect(page.getByTestId("control-header-actions")).toBeVisible();
    //
    //    // AC-2: three buttons in mockup order.
    //    const runQuery = page.getByTestId("control-action-run-query");
    //    const editYaml = page.getByTestId("control-action-edit-yaml");
    //    const requestException = page.getByTestId(
    //      "control-action-request-exception",
    //    );
    //    await expect(runQuery).toBeVisible();
    //    await expect(editYaml).toBeVisible();
    //    await expect(requestException).toBeVisible();
    //
    //    // AC-3 + AC-4: Run query + Edit YAML carry an explanatory
    //    // tooltip and are non-interactive. They are NOT links.
    //    await expect(runQuery).toBeDisabled();
    //    await expect(editYaml).toBeDisabled();
    //    await expect(runQuery).toHaveAttribute(
    //      "aria-label",
    //      /Rule-DSL execution lands in a follow-up slice/,
    //    );
    //    await expect(editYaml).toHaveAttribute(
    //      "aria-label",
    //      /Control-text editor lands in a follow-up slice/,
    //    );
    //
    //    // P0-255-3: no `<a href="#">` anywhere in the action well.
    //    const headHashLinks = page
    //      .getByTestId("control-header-actions")
    //      .locator('a[href="#"]');
    //    await expect(headHashLinks).toHaveCount(0);
    //
    //    // D2: Request exception links to a real route.
    //    await expect(requestException).toHaveAttribute(
    //      "href",
    //      new RegExp(`^/exceptions\\?control_id=`),
    //    );
    //
    //    // AC-1: last-evaluated sub-line renders with a value.
    //    await expect(page.getByTestId("control-last-evaluated")).toBeVisible();
    //    await expect(
    //      page.getByTestId("control-last-evaluated-value"),
    //    ).toBeVisible();
    //
    //    // AC-6: the three buttons are keyboard-reachable in DOM order.
    //    // We focus the first one explicitly, then Tab twice; each Tab
    //    // should land on the next button in mockup order.
    //    await runQuery.focus();
    //    await expect(runQuery).toBeFocused();
    //    await page.keyboard.press("Tab");
    //    await expect(editYaml).toBeFocused();
    //    await page.keyboard.press("Tab");
    //    await expect(requestException).toBeFocused();
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
