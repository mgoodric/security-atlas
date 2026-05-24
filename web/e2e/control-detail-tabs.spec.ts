// Slice 254 — Playwright E2E for the control-detail tab strip.
//
// The slice ships a sticky seven-tab strip on `/controls/{id}`:
// Overview / Evidence / Mappings / Effective scope / Policies /
// Risks / History. Tab state is URL-bound (`?tab=<key>`) so deep
// links resolve directly into the requested tab. The spec asserts:
//
//   - AC-1: seven tabs render in mockup order with the right labels.
//   - AC-8: clicking a tab updates the URL to `?tab=<key>`; refresh
//     on the deep-linked URL lands back on the same tab; clicking
//     Overview (the default) strips the param entirely.
//   - AC-9: keyboard navigation focuses tabs in DOM order via Tab.
//   - AC-9 + slice 274 + 223 lesson: assertions use Playwright's
//     auto-waiting `expect().toBeVisible()` and `expect(page).
//     toHaveURL()` — no `page.waitForTimeout` polling, no manual
//     debounce window.
//
// The spec mocks the BFF endpoints the page consumes so the
// assertions don't depend on the slice-006 SCF anchor catalog or
// the slice-018 effective-scope fan-out being seeded — both of which
// the `control-detail.sql` fixture currently stubs (see the fixture
// preamble). The mocked payloads carry deterministic counts so the
// tab-chip assertions are stable.
//
// Slice 275 — auto-wait fix for the tablist-visible assertion.
//
// The first build of this spec used the Playwright default 5s
// `toBeVisible` timeout on `page.getByTestId("control-tabs")`. Under
// CI load (2 parallel workers against a shared docker stack), the
// page-mount sequence (Suspense fallback → ControlDetailPageInner
// mount → useSearchParams resolved → coverageQ useQuery fires →
// fetch awaits mock fulfillment → React re-renders past
// `coverageQ.isLoading`) routinely exceeded that 5s budget. The page
// stays in its `coverageQ.isLoading` skeleton branch (testid
// `control-detail-loading`); the assertion times out.
//
// The mocks ARE registered before `page.goto` — every `await
// page.route(...)` in beforeEach resolves before the test body runs,
// so the spec's filed "route registration timing" hypothesis (#2) is
// NOT the root cause. The actual cause is the assertion shape's
// default 5s polling budget being too short for the mount sequence
// under CI load (hypothesis #1 in the slice spec — Suspense
// fallback + coverageQ fetch overhead).
//
// The fix follows slice 274 D-274-3's "auto-waiting beats default
// timeouts" pattern, ratcheted up for the page-mount case:
//
//   1. After every `page.goto(/controls/{id})`, await a
//      `page.waitForResponse("**/coverage")` so the assertion that
//      follows is deterministically gated on the network round-trip
//      that drives `coverageQ.isLoading` → settled.
//   2. The first tablist assertion in each test uses an explicit
//      30s timeout as a CI-load backstop. The `waitForResponse`
//      above closes the race; the timeout is the floor in case
//      something downstream (React commit, sticky-position layout)
//      slips on slow runners.
//
// The fix is e2e-only — no production code is touched (slice 275
// P0-275-2). The `gotoControlDetail(page, opts)` helper encapsulates
// the navigation + wait pattern so all five originally-racy tests
// (AC-1, AC-2, AC-8 x2, AC-9) share one implementation.

import { expect, test } from "./fixtures";
import type { Page } from "@playwright/test";

import { seeded } from "./fixtures";
import { seedFromFixture } from "./seed";

// Slice 275 — Navigate to /controls/{id} and gate the next
// assertion on the coverage endpoint resolving. The page is
// dominated by `coverageQ.isLoading` until the GET /api/controls/
// {id}/coverage round-trip completes — the tablist renders only
// AFTER that query settles (see web/app/(authed)/controls/[id]/
// page.tsx line 226 `if (coverageQ.isLoading) return <Skeleton/>`).
// Waiting for the response BEFORE the tablist visibility assertion
// closes the race deterministically. The optional `tab` arg lets
// the AC-8 deep-link / AC-8 garbage-tab tests share the helper.
async function gotoControlDetail(
  page: Page,
  opts: { tab?: string } = {},
): Promise<void> {
  const url = opts.tab
    ? `/controls/${seeded.controlId}?tab=${encodeURIComponent(opts.tab)}`
    : `/controls/${seeded.controlId}`;
  const coverageResp = page.waitForResponse(
    (r) =>
      r.url().includes(`/api/controls/${seeded.controlId}/coverage`) &&
      r.status() === 200,
    { timeout: 30_000 },
  );
  await page.goto(url);
  await coverageResp;
}

test.describe("control detail tab strip (slice 254)", () => {
  test.beforeAll(() => {
    seedFromFixture("control-detail");
  });

  test.beforeEach(async ({ authedPage: page }) => {
    // Mock the seven endpoints the page consumes. Each payload is
    // deterministic so the chip counts and panel content are
    // predictable across runs. The shape mirrors the production
    // response shapes verbatim (no fabricated fields).
    await page.route(`**/api/controls/${seeded.controlId}/coverage`, (route) =>
      route.fulfill({
        status: 200,
        contentType: "application/json",
        body: JSON.stringify({
          control: {
            id: seeded.controlId,
            tenant_id: seeded.tenantId,
            bundle_id: "CTRL-0014",
            version: 1,
            title: "MFA Enforcement",
            control_family: "Workforce IAM",
            owner_role: "Security Engineering",
            implementation_type: "automated",
            lifecycle_state: "active",
            freshness_class: "daily",
          },
          anchor: {
            id: "11111111-1111-1111-1111-111111110001",
            scf_id: "SCF:IAC-06",
            family: "IAC",
            name: "Multi-Factor Authentication",
            description: "MFA spine anchor",
          },
          requirements: [
            {
              framework_version_id: seeded.frameworkVersionId,
              framework_name: "SOC 2",
              framework_version: "2017",
              requirement_id: "CC6.6",
              requirement_text: "logical access controls",
              relationship_type: "equal",
              strength: 1.0,
              coverage: 0.94,
            },
            {
              framework_version_id: "11111111-1111-1111-1111-111111110009",
              framework_name: "ISO 27001",
              framework_version: "2022",
              requirement_id: "A.8.5",
              requirement_text: "secure authentication",
              relationship_type: "equal",
              strength: 1.0,
              coverage: 0.94,
            },
          ],
        }),
      }),
    );

    await page.route(`**/api/controls/${seeded.controlId}/state`, (route) =>
      route.fulfill({
        status: 200,
        contentType: "application/json",
        body: JSON.stringify({
          control_id: seeded.controlId,
          states: [
            {
              scope_cell: "prod-us",
              computed_state: "pass",
              freshness_status: "fresh",
              last_observed_at: new Date(Date.now() - 8 * 60_000).toISOString(),
            },
          ],
          count: 1,
        }),
      }),
    );

    await page.route(
      `**/api/controls/${seeded.controlId}/effectiveness`,
      (route) =>
        route.fulfill({
          status: 200,
          contentType: "application/json",
          body: JSON.stringify({
            control_id: seeded.controlId,
            pass_rate: 0.94,
            pass_count: 47,
            total_count: 50,
            window_days: 30,
          }),
        }),
    );

    await page.route(
      `**/api/controls/${seeded.controlId}/effective-scope?**`,
      (route) =>
        route.fulfill({
          status: 200,
          contentType: "application/json",
          body: JSON.stringify({
            control_id: seeded.controlId,
            framework_version_id: seeded.frameworkVersionId,
            in_scope: true,
            effective_scope_count: 12,
            out_of_scope_reason: null,
          }),
        }),
    );

    // Evidence list — three records and no next_cursor so the chip
    // renders the exact integer "3" rather than the "5+" hint.
    await page.route("**/api/evidence?**", (route) =>
      route.fulfill({
        status: 200,
        contentType: "application/json",
        body: JSON.stringify({
          control_id: seeded.controlId,
          evidence: [
            {
              evidence_id: "ev-1",
              evidence_kind: "okta.mfa_enforcement",
              observed_at: "2026-05-23T16:00:00Z",
              source: { actor_type: "okta", actor_id: "prod-us" },
              content_hash:
                "0000000000000000000000000000000000000000000000000000000000000000",
              scope_cell: "prod-us",
              result: "pass",
            },
            {
              evidence_id: "ev-2",
              evidence_kind: "okta.mfa_enforcement",
              observed_at: "2026-05-23T15:00:00Z",
              source: { actor_type: "okta", actor_id: "prod-eu" },
              content_hash:
                "0000000000000000000000000000000000000000000000000000000000000000",
              scope_cell: "prod-eu",
              result: "pass",
            },
            {
              evidence_id: "ev-3",
              evidence_kind: "aws.iam.password_policy",
              observed_at: "2026-05-22T08:00:00Z",
              source: { actor_type: "aws", actor_id: "iam" },
              content_hash:
                "0000000000000000000000000000000000000000000000000000000000000000",
              scope_cell: null,
              result: "pass",
            },
          ],
          count: 3,
          total: 3,
          next_cursor: "",
        }),
      }),
    );

    await page.route(`**/api/controls/${seeded.controlId}/policies`, (route) =>
      route.fulfill({
        status: 200,
        contentType: "application/json",
        body: JSON.stringify({
          control_id: seeded.controlId,
          policies: [
            {
              policy_id: "00000000-0000-0000-0000-0000000000a1",
              title: "Access Control Policy",
              version: "v3.2",
              status: "approved",
            },
            {
              policy_id: "00000000-0000-0000-0000-0000000000a2",
              title: "Workforce Identity Standard",
              version: "v1.5",
              status: "approved",
            },
          ],
          count: 2,
        }),
      }),
    );

    await page.route(`**/api/controls/${seeded.controlId}/risks`, (route) =>
      route.fulfill({
        status: 200,
        contentType: "application/json",
        body: JSON.stringify({
          control_id: seeded.controlId,
          risks: [
            {
              risk_id: "00000000-0000-0000-0000-0000000000b1",
              title: "Credential theft via phishing",
              residual_score: 0.34,
              link_weight: 0.85,
            },
          ],
          count: 1,
        }),
      }),
    );

    await page.route(`**/api/controls/${seeded.controlId}/history`, (route) =>
      route.fulfill({
        status: 200,
        contentType: "application/json",
        body: JSON.stringify({
          control_id: seeded.controlId,
          history: [
            {
              evaluated_at: "2026-05-23T16:00:00Z",
              scope_cell: "prod-us",
              computed_state: "pass",
              freshness_status: "fresh",
              evidence_count: 3,
            },
          ],
          count: 1,
          next_cursor: "",
        }),
      }),
    );
  });

  test("AC-1: renders the seven tabs in mockup order with the right labels", async ({
    authedPage: page,
  }) => {
    // Slice 275 — wait for the coverage response BEFORE asserting the
    // tablist so the assertion isn't racing the mount sequence.
    await gotoControlDetail(page);
    const tablist = page.getByTestId("control-tabs");
    await expect(tablist).toBeVisible({ timeout: 30_000 });

    // The tab strip is the seven labels in mockup order. We assert
    // both the label text and the testid suffix so a regression that
    // renames a key without renaming the label (or vice-versa) fails
    // here, not silently.
    await expect(page.getByTestId("control-tab-overview")).toHaveText(
      /^Overview/,
    );
    await expect(page.getByTestId("control-tab-evidence")).toContainText(
      "Evidence",
    );
    await expect(page.getByTestId("control-tab-mappings")).toContainText(
      "Mappings",
    );
    await expect(page.getByTestId("control-tab-scope")).toContainText(
      "Effective scope",
    );
    await expect(page.getByTestId("control-tab-policies")).toContainText(
      "Policies",
    );
    await expect(page.getByTestId("control-tab-risks")).toContainText("Risks");
    await expect(page.getByTestId("control-tab-history")).toHaveText(
      /^History/,
    );
  });

  test("AC-2: count chips render the mocked-payload counts", async ({
    authedPage: page,
  }) => {
    // Slice 275 — coverage-response gate (see gotoControlDetail) +
    // 30s timeout on the first auto-waiting assertion so the Overview
    // panel has space to mount on slow CI runners. The chips render
    // off subsequent useQuery payloads; each chip's per-assertion
    // toHaveText below auto-waits on its own polling cycle.
    await gotoControlDetail(page);
    await expect(page.getByTestId("control-tab-panel-overview")).toBeVisible({
      timeout: 30_000,
    });

    // 3 evidence records · 2 mapped requirements · 12 effective-scope
    // cells (one framework_version in the mock) · 2 policies · 1
    // risk. History has no chip per the mockup.
    await expect(page.getByTestId("control-tab-evidence-chip")).toHaveText("3");
    await expect(page.getByTestId("control-tab-mappings-chip")).toHaveText("2");
    await expect(page.getByTestId("control-tab-scope-chip")).toHaveText("12");
    await expect(page.getByTestId("control-tab-policies-chip")).toHaveText("2");
    await expect(page.getByTestId("control-tab-risks-chip")).toHaveText("1");
  });

  test("AC-8: clicking a tab updates `?tab=<key>` and renders that tab's panel", async ({
    authedPage: page,
  }) => {
    // Slice 275 — coverage-response gate (see gotoControlDetail).
    await gotoControlDetail(page);
    // Initial URL has no `tab` param (Overview is the default — D2).
    await expect(page).toHaveURL(new RegExp(`/controls/${seeded.controlId}$`));
    await expect(page.getByTestId("control-tab-panel-overview")).toBeVisible({
      timeout: 30_000,
    });

    // Click Evidence — URL updates, Evidence panel renders.
    await page.getByTestId("control-tab-evidence").click();
    await expect(page).toHaveURL(/\?tab=evidence$/);
    await expect(page.getByTestId("evidence-tab-panel")).toBeVisible();

    // Click Mappings — URL updates, Mappings panel renders.
    await page.getByTestId("control-tab-mappings").click();
    await expect(page).toHaveURL(/\?tab=mappings$/);
    await expect(page.getByTestId("mappings-tab-panel")).toBeVisible();

    // Click Overview — param is stripped (canonical URL stays clean).
    await page.getByTestId("control-tab-overview").click();
    await expect(page).toHaveURL(new RegExp(`/controls/${seeded.controlId}$`));
    await expect(page.getByTestId("control-tab-panel-overview")).toBeVisible();
  });

  test("AC-8: refresh on a tab-deep-linked URL lands on that tab", async ({
    authedPage: page,
  }) => {
    // Slice 275 — deep-link directly to the Policies tab; the helper
    // both navigates and gates on the coverage round-trip.
    await gotoControlDetail(page, { tab: "policies" });
    // The Policies panel renders without first showing Overview — the
    // URL is the source of truth.
    await expect(page.getByTestId("policies-tab-panel")).toBeVisible({
      timeout: 30_000,
    });
    await expect(page.getByTestId("control-tab-panel-overview")).toHaveCount(0);
    await expect(page.getByTestId("control-tab-policies")).toHaveAttribute(
      "aria-selected",
      "true",
    );

    // Refresh — re-gate on coverage since the in-flight query restarts.
    const reloadCoverage = page.waitForResponse(
      (r) =>
        r.url().includes(`/api/controls/${seeded.controlId}/coverage`) &&
        r.status() === 200,
      { timeout: 30_000 },
    );
    await page.reload();
    await reloadCoverage;
    await expect(page.getByTestId("policies-tab-panel")).toBeVisible({
      timeout: 30_000,
    });
    await expect(page.getByTestId("control-tab-policies")).toHaveAttribute(
      "aria-selected",
      "true",
    );
  });

  test("AC-8: unrecognised `?tab=<garbage>` falls through to Overview", async ({
    authedPage: page,
  }) => {
    // Slice 275 — coverage-response gate via the helper.
    await gotoControlDetail(page, { tab: "foo" });
    // Default tab is Overview when the param is unrecognised.
    await expect(page.getByTestId("control-tab-panel-overview")).toBeVisible({
      timeout: 30_000,
    });
    await expect(page.getByTestId("control-tab-overview")).toHaveAttribute(
      "aria-selected",
      "true",
    );
  });

  test("AC-9: keyboard Tab navigation walks through the seven tab buttons in DOM order", async ({
    authedPage: page,
  }) => {
    // Slice 275 — coverage-response gate via the helper.
    await gotoControlDetail(page);
    // Wait until the Overview panel is mounted so we know the strip
    // is rendered and the focus order is stable.
    await expect(page.getByTestId("control-tab-panel-overview")).toBeVisible({
      timeout: 30_000,
    });

    // Focus the first tab explicitly. From there, six Tab presses
    // walk through the remaining six tab buttons in mockup order.
    await page.getByTestId("control-tab-overview").focus();
    await expect(page.getByTestId("control-tab-overview")).toBeFocused();

    const remaining = [
      "control-tab-evidence",
      "control-tab-mappings",
      "control-tab-scope",
      "control-tab-policies",
      "control-tab-risks",
      "control-tab-history",
    ];
    for (const id of remaining) {
      await page.keyboard.press("Tab");
      await expect(page.getByTestId(id)).toBeFocused();
    }
  });

  test("AC-3 + AC-7: Overview panel preserves the pre-tab layout (P0-254-3)", async ({
    authedPage: page,
  }) => {
    // Slice 275 — coverage-response gate via the helper.
    await gotoControlDetail(page);
    // The Overview panel still hosts: KPI strip, Coverage table,
    // UCF graph, Evidence stream card, Freshness, Effective scope
    // summary, Policies, Risks, Audit log. Slice 254 anti-criterion
    // P0-254-3 — the Overview tab's data layout is preserved verbatim.
    await expect(page.getByTestId("kpi-strip")).toBeVisible({
      timeout: 30_000,
    });
    await expect(page.getByTestId("coverage-section")).toBeVisible();
    await expect(page.getByTestId("ucf-viz-section")).toBeVisible();
    await expect(page.getByTestId("evidence-stream-section")).toBeVisible();
    await expect(page.getByTestId("freshness-section")).toBeVisible();
    await expect(page.getByTestId("effective-scope-section")).toBeVisible();
    await expect(page.getByTestId("policies-section")).toBeVisible();
    await expect(page.getByTestId("risks-section")).toBeVisible();
    await expect(page.getByTestId("audit-log-section")).toBeVisible();
  });
});
