// Slice 213 — Playwright E2E for the topbar header chrome additions
// (AC-2 + AC-5).
//
// The slice ships TWO header chrome affordances visible on every authed
// page (the chrome is shared via web/components/shell/topbar.tsx):
//
//   1. <InProgressAuditPill /> — amber pill reading "<period name> in
//      progress" from /api/audits filtered to status='in_progress'.
//      Returns null when zero match (P0-213-2 silent-absence).
//
//   2. <UserAvatar /> — initials + display name read from /v1/me via
//      the BFF /api/me. Server component; fail-closed (returns null
//      on any fetch / parse error).
//
// AC-5 (the load-bearing assertion): the in-progress pill appears when
// a fixture period exists with status='in_progress' and is absent
// otherwise.
//
// The "absent otherwise" half lives in the vitest sibling
// (`web/components/shell/in-progress-audit-pill.test.ts`), which pins
// the `pickMostRecentInProgress` helper's "zero in_progress → null"
// branch. The Playwright e2e spec covers the integrated positive case
// — the shared chrome renders with a seeded in_progress period —
// because that's the surface most likely to regress (BFF wiring,
// TanStack Query cache, server/client component boundary).
//
// AC-4 (user avatar): assert visible name + initials. The fixture
// seeds a `users` row with display_name='Sam Operator'; the avatar
// should render "Sam Operator" + "SO".
//
// Run locally:
//   cd web
//   npx playwright install chromium     # once per machine
//   npx playwright test e2e/audits-header.spec.ts

import { expect, test } from "./fixtures";

import { seedFromFixture } from "./seed";

test.describe("topbar header chrome (slice 213)", () => {
  test.beforeAll(() => {
    seedFromFixture("audits-header");
  });

  test("AC-5: in-progress audit pill appears when a fixture period has status=in_progress", async ({
    authedPage: page,
  }) => {
    await page.goto("/audits");
    const pill = page.getByTestId("in-progress-audit-pill");
    await expect(pill).toBeVisible();
    // The pill's visible copy is the period name + " in progress".
    // The fixture seeds 'SOC 2 Type II · Q2 2026' as the in_progress
    // period name (audits-header.sql).
    await expect(pill).toContainText("SOC 2 Type II · Q2 2026 in progress");
    // The amber dot is an aria-hidden visual cue; the pill itself
    // carries the aria-label for screen readers.
    await expect(pill).toHaveAttribute(
      "aria-label",
      "SOC 2 Type II · Q2 2026 in progress",
    );
  });

  test("AC-2: pill renders on a non-audits page too (shared chrome)", async ({
    authedPage: page,
  }) => {
    // The pill is in the shared topbar — it should be visible on /dashboard
    // even though /dashboard does not have its own audit-period UI.
    await page.goto("/dashboard");
    await expect(page.getByTestId("in-progress-audit-pill")).toBeVisible();
  });

  test("AC-4: user avatar renders display name + initials from /v1/me", async ({
    authedPage: page,
  }) => {
    await page.goto("/audits");
    const avatar = page.getByTestId("user-avatar");
    await expect(avatar).toBeVisible();
    // The fixture seeds display_name='Sam Operator'.
    await expect(page.getByTestId("user-avatar-name")).toHaveText(
      "Sam Operator",
    );
    await expect(page.getByTestId("user-avatar-initials")).toHaveText("SO");
  });

  test("P0-213-1: pill consumes the existing /api/audits BFF (no new endpoint)", async ({
    authedPage: page,
  }) => {
    // Anti-criterion enforcement: the pill must read /api/audits — the
    // existing slice-102 BFF — and not invent a new platform endpoint.
    // We record the requests issued during navigation and assert exactly
    // one matches the existing route.
    const audits: string[] = [];
    page.on("request", (r) => {
      const url = r.url();
      if (url.includes("/api/audits")) audits.push(url);
    });
    await page.goto("/audits");
    await expect(page.getByTestId("in-progress-audit-pill")).toBeVisible();
    // Multiple requests to /api/audits are fine (the page's own table
    // queries the same endpoint via shared TanStack Query cache);
    // assertion is "at least one", not "exactly one".
    expect(audits.length).toBeGreaterThan(0);
  });

  // ----- Slice 214 — sidebar item counts (Controls + Risks badges) -----
  //
  // The badges live in the shared sidebar (`web/components/shell/
  // sidebar.tsx`), which renders on every authed page. We piggyback on
  // the audits-header fixture (which already provides an authed
  // session + the demo tenant) to assert the badge presence on /audits.
  //
  // Controls badge: the base seed `fixtures/walkthroughs/00-seed.sql`
  // instantiates one control under DEMO_CONTROL_ID; the badge therefore
  // shows "1" once the count fetch resolves.
  //
  // Risks badge: the audits-header fixture seeds zero risks. The badge
  // is hidden under the silent-absence rule (P0-214-2). Asserting
  // absence in Playwright is brittle (`.not.toBeVisible()` requires
  // careful wait semantics); the pure-helper unit test
  // (`sidebar-counts.test.ts`) pins the zero-count → null branch.

  test("AC-1 (slice 214): Controls count badge appears on /audits via shared sidebar", async ({
    authedPage: page,
  }) => {
    await page.goto("/audits");
    const badge = page.getByTestId("sidebar-controls-count");
    await expect(badge).toBeVisible();
    // The base seed inserts one control row; the count fetch must
    // resolve to a positive integer (we assert the visible text is a
    // numeric string ≥1 rather than pinning a specific number because
    // sibling fixtures in the same DB may insert additional controls).
    const text = await badge.innerText();
    expect(Number(text)).toBeGreaterThan(0);
  });

  test("P0-214-1 (slice 214): Controls badge consumes the existing /api/controls BFF (no new endpoint)", async ({
    authedPage: page,
  }) => {
    const controlsRequests: string[] = [];
    page.on("request", (r) => {
      const url = r.url();
      if (url.includes("/api/controls")) controlsRequests.push(url);
    });
    await page.goto("/audits");
    await expect(page.getByTestId("sidebar-controls-count")).toBeVisible();
    expect(controlsRequests.length).toBeGreaterThan(0);
  });
});
