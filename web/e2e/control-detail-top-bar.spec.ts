// Slice 257 — Playwright E2E for the shared-shell chrome on
// `/controls/{id}` (control detail page).
//
// The slice 204 audit fleet's finding F-204-C-5 flagged three chrome
// elements missing on `/controls/{id}`: an audit-period pill, a tenant
// breadcrumb, and a global ⌘K search input. Between slice 257's
// filing and this build, slices 213 + 223 shipped all three in the
// shared authed-shell topbar (`web/components/shell/topbar.tsx`),
// which renders on every authed route — including `/controls/{id}`.
//
// The slice 213 e2e (`audits-header.spec.ts`) proves the in-progress
// pill is shared chrome on `/dashboard` (AC-2). The slice 223 e2e
// (`controls-top-bar.spec.ts`) proves the breadcrumb + search render
// on `/controls` (list) + `/audits`. The slice 235 e2e
// (`evidence-header.spec.ts`) proves the same on `/evidence`. None of
// the four asserts the chrome on `/controls/{id}` (detail)
// specifically. The moment a future slice regresses one of those
// components, the regression risk concentrates on the one detail page
// nobody asserts.
//
// Page-names rollup: the slice 223 `page-names.ts` map keys on the
// FIRST URL segment, so `/controls/<uuid>` rolls up to `Controls` in
// the breadcrumb's right segment. The vitest sibling
// `web/lib/page-names.test.ts` already pins this branch
// (`/controls/abc-123` -> "Controls"); this spec is the integrated
// end-to-end assertion.
//
// This spec closes that gap by exercising all three load-bearing
// chrome elements on `/controls/{id}`:
//
//   1. <InProgressAuditPill /> renders the seeded audit-period name.
//   2. <Breadcrumb /> renders `Demo Tenant > Controls` (the detail
//      route rolls up to the section name via the slice 223
//      page-names.ts table).
//   3. <GlobalSearch /> input renders with the mockup placeholder +
//      the ⌘K kbd hint, ⌘K focuses it, and typing fires a debounced
//      `/api/search` request.
//
// AC mapping (slice 257):
//   - AC-1 / AC-3 — pill + breadcrumb on detail page: assertions
//     (1) + (2) below.
//   - AC-4 — global search on detail page: assertions (3) + (4) + (5).
//   - AC-5 — F-204-C-5 resolved: the next `/controls/{id}` audit run
//     will find the three chrome elements present + asserted.
//
// Run locally:
//   cd web
//   npx playwright install chromium     # once per machine
//   npx playwright test e2e/control-detail-top-bar.spec.ts

import { expect, seeded, test } from "./fixtures";

import { seedFromFixture } from "./seed";

test.describe("control-detail header chrome (slice 257)", () => {
  test.beforeAll(() => {
    // Two fixtures sequenced: audits-header seeds the audit_periods
    // row that drives the in-progress pill copy; controls-top-bar
    // seeds the tenants row that drives the breadcrumb's left
    // segment. Both fixtures' INSERTs are idempotent (ON CONFLICT
    // DO NOTHING) so running them back-to-back is safe. D2 in the
    // decisions log explains why this is cheaper than a third
    // bespoke fixture. Slice 235 D2 established the precedent.
    seedFromFixture("audits-header");
    seedFromFixture("controls-top-bar");
  });

  test("AC-1: in-progress audit pill renders on /controls/{id} (shared chrome)", async ({
    authedPage: page,
  }) => {
    await page.goto(`/controls/${seeded.controlId}`);
    const pill = page.getByTestId("in-progress-audit-pill");
    await expect(pill).toBeVisible();
    // Same period name the audits-header fixture seeds + the slice
    // 213 spec pins on /audits and slice 235 spec pins on /evidence.
    // Asserting on `/controls/{id}` proves the shared-chrome wiring
    // is intact for the detail route.
    await expect(pill).toContainText("SOC 2 Type II · Q2 2026 in progress");
    await expect(pill).toHaveAttribute(
      "aria-label",
      "SOC 2 Type II · Q2 2026 in progress",
    );
  });

  test("AC-3: breadcrumb renders `Demo Tenant > Controls` on /controls/{id}", async ({
    authedPage: page,
  }) => {
    await page.goto(`/controls/${seeded.controlId}`);
    const crumb = page.getByTestId("breadcrumb");
    await expect(crumb).toBeVisible();
    await expect(page.getByTestId("breadcrumb-tenant")).toHaveText(
      "Demo Tenant",
    );
    // The page-names.ts table keys on the first URL segment, so the
    // detail page `/controls/<uuid>` rolls up to `Controls`. This
    // assertion catches regressions in either the table entry or the
    // first-segment derivation. The vitest sibling pins the pure
    // helper's branch; this spec pins the integrated render.
    await expect(page.getByTestId("breadcrumb-page")).toHaveText("Controls");
  });

  test("AC-4: global search input renders on /controls/{id} with placeholder + ⌘K kbd", async ({
    authedPage: page,
  }) => {
    await page.goto(`/controls/${seeded.controlId}`);
    const input = page.getByTestId("global-search-input");
    await expect(input).toBeVisible();
    await expect(input).toHaveAttribute(
      "placeholder",
      "Search controls, evidence, risks…",
    );
  });

  test("AC-4: ⌘K keyboard shortcut focuses the search input on /controls/{id}", async ({
    authedPage: page,
  }) => {
    await page.goto(`/controls/${seeded.controlId}`);
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
    // P0-223-1 assertion and slice 235's evidence-header spec both
    // use the same pattern. We mock the response so the spec is
    // deterministic regardless of the seeded search corpus state.
    await page.route("**/api/search**", async (route) => {
      await route.fulfill({
        status: 200,
        contentType: "application/json",
        body: JSON.stringify({
          hits: [
            {
              id: "00000000-0000-0000-0000-000000000257",
              type: "controls",
              title: "Encryption at rest",
              snippet: "Encryption at rest — prod object stores",
              relevance_score: 0.9,
            },
          ],
          count: 1,
          partial_types: [],
        }),
      });
    });

    await page.goto(`/controls/${seeded.controlId}`);
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
      page.getByTestId("global-search-group-controls"),
    ).toBeVisible();
  });
});
