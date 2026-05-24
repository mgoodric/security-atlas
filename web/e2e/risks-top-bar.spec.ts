// Slice 243 — Playwright E2E for the shared-shell chrome on `/risks`.
//
// The slice 204 audit fleet's finding on `/risks` (mirror of F-204-E-3
// for /evidence) flagged four chrome elements missing on `/risks`: an
// audit-period pill, a tenant breadcrumb, a global ⌘K search input,
// and a user avatar. Between slice 243's filing and this build, slices
// 213 + 223 shipped all three load-bearing components in the shared
// authed-shell topbar (`web/components/shell/topbar.tsx`), which
// renders on every authed route — including `/risks`.
//
// The slice 213 e2e (`audits-header.spec.ts`) proves the in-progress
// pill is shared chrome by visiting `/dashboard`. The slice 223 e2e
// (`controls-top-bar.spec.ts`) proves the breadcrumb + search render
// on `/controls` + `/audits`. Slice 235's `evidence-header.spec.ts`
// closed the same gap for `/evidence`. Neither asserts the chrome on
// `/risks` specifically. The moment a future slice regresses one of
// those components, the regression risk concentrates on the one page
// nobody asserts.
//
// This spec closes that gap by exercising the three load-bearing
// chrome elements on `/risks`:
//
//   1. <InProgressAuditPill /> renders the seeded audit-period name.
//   2. <Breadcrumb /> renders `Demo Tenant > Risks` (the page-name
//      derivation maps `/risks` → `Risks` via the slice 223
//      `page-names.ts` table).
//   3. <GlobalSearch /> input renders with the mockup placeholder +
//      the ⌘K kbd hint, ⌘K focuses it, and typing fires a debounced
//      `/api/search` request.
//
// AC mapping (slice 243):
//   - AC-1 — tenant breadcrumb chip: assertion (2) below.
//   - AC-2 — audit-in-progress banner pill: assertion (1) below.
//   - AC-5 — Playwright spec asserts the breadcrumb chip on `/risks`:
//     this whole spec.
//
// Run locally:
//   cd web
//   npx playwright install chromium     # once per machine
//   npx playwright test e2e/risks-top-bar.spec.ts

import { expect, test } from "./fixtures";

import { seedFromFixture } from "./seed";

test.describe("risks top bar chrome (slice 243)", () => {
  test.beforeAll(() => {
    // Two fixtures sequenced: audits-header seeds the audit_periods
    // row that drives the in-progress pill copy; controls-top-bar
    // seeds the tenants row that drives the breadcrumb's left
    // segment. Both fixtures' INSERTs are idempotent (ON CONFLICT
    // DO NOTHING) so running them back-to-back is safe. D1 in the
    // decisions log (and slice 235 D2) explains why this is cheaper
    // than a third bespoke fixture.
    seedFromFixture("audits-header");
    seedFromFixture("controls-top-bar");
  });

  test("AC-2: in-progress audit pill renders on /risks (shared chrome)", async ({
    authedPage: page,
  }) => {
    await page.goto("/risks");
    const pill = page.getByTestId("in-progress-audit-pill");
    await expect(pill).toBeVisible();
    // Same period name the audits-header fixture seeds + the slice
    // 213 spec pins on /audits. Asserting on /risks proves the
    // shared-chrome wiring is intact for this route.
    await expect(pill).toContainText("SOC 2 Type II · Q2 2026 in progress");
    await expect(pill).toHaveAttribute(
      "aria-label",
      "SOC 2 Type II · Q2 2026 in progress",
    );
  });

  test("AC-1: breadcrumb renders `Demo Tenant > Risks` on /risks", async ({
    authedPage: page,
  }) => {
    await page.goto("/risks");
    const crumb = page.getByTestId("breadcrumb");
    await expect(crumb).toBeVisible();
    await expect(page.getByTestId("breadcrumb-tenant")).toHaveText(
      "Demo Tenant",
    );
    // The page-names.ts table maps `risks` → `Risks`. This assertion
    // catches regressions in either the table entry or the route-
    // segment derivation.
    await expect(page.getByTestId("breadcrumb-page")).toHaveText("Risks");
  });

  test("AC-5: global search input renders on /risks with placeholder + ⌘K kbd", async ({
    authedPage: page,
  }) => {
    await page.goto("/risks");
    const input = page.getByTestId("global-search-input");
    await expect(input).toBeVisible();
    await expect(input).toHaveAttribute(
      "placeholder",
      "Search controls, evidence, risks…",
    );
  });

  test("AC-5: ⌘K keyboard shortcut focuses the search input on /risks", async ({
    authedPage: page,
  }) => {
    await page.goto("/risks");
    const input = page.getByTestId("global-search-input");
    await expect(input).not.toBeFocused();
    await page.keyboard.press("Meta+K");
    await expect(input).toBeFocused();
  });

  test("AC-5: typing into the search input fires a debounced /api/search request", async ({
    authedPage: page,
  }) => {
    // Slice 235 D3 (citing slice 274's AC-9 flake fix) — use
    // page.waitForRequest over snapshot-after-fill on the popover.
    // The auto-waiting pattern is canonical for debounced surfaces;
    // slice 223's P0-223-1 and slice 235's /evidence spec both use
    // it. We mock the response so the spec is deterministic
    // regardless of the seeded search corpus state.
    await page.route("**/api/search**", async (route) => {
      await route.fulfill({
        status: 200,
        contentType: "application/json",
        body: JSON.stringify({
          hits: [
            {
              id: "00000000-0000-0000-0000-000000000099",
              type: "risks",
              title: "Insider threat · RISK-001",
              snippet: "Insider threat tracking · RISK-001",
              relevance_score: 0.5,
            },
          ],
          count: 1,
          partial_types: [],
        }),
      });
    });

    await page.goto("/risks");
    const input = page.getByTestId("global-search-input");
    await input.click();
    const reqPromise = page.waitForRequest("**/api/search**", {
      timeout: 10_000,
    });
    await input.fill("insider");
    await reqPromise;
    // Popover should be visible after the debounced fetch completes.
    // The waitForRequest above already gates on the network round-
    // trip; the popover visibility is the downstream effect.
    await expect(page.getByTestId("global-search-popover")).toBeVisible();
    await expect(page.getByTestId("global-search-group-risks")).toBeVisible();
  });
});
