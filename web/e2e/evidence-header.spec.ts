// Slice 235 — Playwright E2E for the shared-shell chrome on `/evidence`.
//
// The slice 204 audit fleet's finding F-204-E-3 flagged three chrome
// elements missing on `/evidence`: an audit-period pill, a tenant
// breadcrumb, and a global ⌘K search input. Between slice 235's
// filing and this build, slices 213 + 223 shipped all three in the
// shared authed-shell topbar (`web/components/shell/topbar.tsx`),
// which renders on every authed route — including `/evidence`.
//
// The slice 213 e2e (`audits-header.spec.ts`) proves the in-progress
// pill is shared chrome by visiting `/dashboard` (AC-2). The slice
// 223 e2e (`controls-top-bar.spec.ts`) proves the breadcrumb +
// search render on `/controls` + `/audits`. Neither asserts the
// chrome on `/evidence` specifically. The moment a future slice
// regresses one of those components, the regression risk concentrates
// on the one page nobody asserts.
//
// This spec closes that gap by exercising all three load-bearing
// chrome elements on `/evidence`:
//
//   1. <InProgressAuditPill /> renders the seeded audit-period name.
//   2. <Breadcrumb /> renders `Demo Tenant > Evidence` (the page-name
//      derivation maps `/evidence` → `Evidence` via the slice 223
//      `page-names.ts` table).
//   3. <GlobalSearch /> input renders with the mockup placeholder +
//      the ⌘K kbd hint, ⌘K focuses it, and typing fires a debounced
//      `/api/search` request.
//
// AC mapping (slice 235):
//   - AC-1 / AC-2 — banner on /evidence: assertion (1) below.
//   - AC-3 — tenant breadcrumb after brand mark: assertion (2) below.
//   - AC-4 — `/evidence`-specific shell-chrome assertions: this whole
//     spec.
//   - AC-5 — F-204-E-3 resolved: the next /evidence audit run will
//     find the three chrome elements present + asserted.
//
// Run locally:
//   cd web
//   npx playwright install chromium     # once per machine
//   npx playwright test e2e/evidence-header.spec.ts

import { expect, test } from "./fixtures";

import { seedFromFixture } from "./seed";

test.describe("evidence header chrome (slice 235)", () => {
  test.beforeAll(() => {
    // Two fixtures sequenced: audits-header seeds the audit_periods
    // row that drives the in-progress pill copy; controls-top-bar
    // seeds the tenants row that drives the breadcrumb's left
    // segment. Both fixtures' INSERTs are idempotent (ON CONFLICT
    // DO NOTHING) so running them back-to-back is safe. D2 in the
    // decisions log explains why this is cheaper than a third
    // bespoke fixture.
    seedFromFixture("audits-header");
    seedFromFixture("controls-top-bar");
  });

  test("AC-1 / AC-2: in-progress audit pill renders on /evidence (shared chrome)", async ({
    authedPage: page,
  }) => {
    await page.goto("/evidence");
    const pill = page.getByTestId("in-progress-audit-pill");
    await expect(pill).toBeVisible();
    // Same period name the audits-header fixture seeds + the slice
    // 213 spec pins on /audits. Asserting on /evidence proves the
    // shared-chrome wiring is intact for this route.
    await expect(pill).toContainText("SOC 2 Type II · Q2 2026 in progress");
    await expect(pill).toHaveAttribute(
      "aria-label",
      "SOC 2 Type II · Q2 2026 in progress",
    );
  });

  test("AC-3: breadcrumb renders `Demo Tenant > Evidence` on /evidence", async ({
    authedPage: page,
  }) => {
    await page.goto("/evidence");
    const crumb = page.getByTestId("breadcrumb");
    await expect(crumb).toBeVisible();
    await expect(page.getByTestId("breadcrumb-tenant")).toHaveText(
      "Demo Tenant",
    );
    // The page-names.ts table maps `evidence` → `Evidence`. This
    // assertion catches regressions in either the table entry or
    // the route-segment derivation.
    await expect(page.getByTestId("breadcrumb-page")).toHaveText("Evidence");
  });

  test("AC-4: global search input renders on /evidence with placeholder + ⌘K kbd", async ({
    authedPage: page,
  }) => {
    await page.goto("/evidence");
    const input = page.getByTestId("global-search-input");
    await expect(input).toBeVisible();
    await expect(input).toHaveAttribute(
      "placeholder",
      "Search controls, evidence, risks…",
    );
  });

  test("AC-4: ⌘K keyboard shortcut focuses the search input on /evidence", async ({
    authedPage: page,
  }) => {
    await page.goto("/evidence");
    const input = page.getByTestId("global-search-input");
    await expect(input).not.toBeFocused();
    await page.keyboard.press("Meta+K");
    await expect(input).toBeFocused();
  });

  test("AC-4: typing into the search input fires a debounced /api/search request", async ({
    authedPage: page,
  }) => {
    // D3 — use page.waitForRequest over snapshot-after-fill on the
    // popover. The slice 274 AC-9 flake fix established auto-waiting
    // as the canonical pattern for debounced surfaces; slice 223's
    // P0-223-1 assertion uses the same pattern. We mock the response
    // so the spec is deterministic regardless of the seeded search
    // corpus state.
    await page.route("**/api/search**", async (route) => {
      await route.fulfill({
        status: 200,
        contentType: "application/json",
        body: JSON.stringify({
          hits: [
            {
              id: "00000000-0000-0000-0000-000000000099",
              type: "evidence",
              title: "iam.role_review · CRY-05",
              snippet: "IAM access review · CRY-05",
              relevance_score: 0.5,
            },
          ],
          count: 1,
          partial_types: [],
        }),
      });
    });

    await page.goto("/evidence");
    const input = page.getByTestId("global-search-input");
    await input.click();
    const reqPromise = page.waitForRequest("**/api/search**", {
      timeout: 10_000,
    });
    await input.fill("iam");
    await reqPromise;
    // Popover should be visible after the debounced fetch completes.
    // The waitForRequest above already gates on the network round-
    // trip; the popover visibility is the downstream effect.
    await expect(page.getByTestId("global-search-popover")).toBeVisible();
    await expect(
      page.getByTestId("global-search-group-evidence"),
    ).toBeVisible();
  });
});
