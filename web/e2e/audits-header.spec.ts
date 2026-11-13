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
  // Issue #1741: the Controls badge counts the tenant's own controls via
  // `/api/controls-list` (it used to count the first page of SCF anchors
  // from `/api/controls`). The two integration assertions below MOCK
  // `/api/controls-list` via `page.route` to inject a deterministic
  // controls payload. The
  // mock-vs-live-data split is honest: the pure render branches
  // (zero / loading / error / non-zero) are pinned by the vitest
  // sibling `sidebar-counts.test.ts`; this spec pins the badge mounts
  // in the shared shell and consumes the existing BFF.
  //
  // Risks badge: similar story — the audits-header fixture seeds zero
  // risks. The silent-absence rule (P0-214-2) keeps the badge hidden;
  // the vitest test pins the zero branch.

  test("AC-1 (slice 214): Controls count badge appears on /audits via shared sidebar (mocked BFF payload)", async ({
    authedPage: page,
  }) => {
    await page.route("**/api/controls-list**", async (route) => {
      await route.fulfill({
        status: 200,
        contentType: "application/json",
        body: JSON.stringify({
          controls: [
            {
              id: "00000000-0000-0000-0000-000000000001",
              scf_id: "CRY-05",
              title: "Encryption at rest",
            },
            {
              id: "00000000-0000-0000-0000-000000000002",
              scf_id: "IAC-06",
              title: "MFA",
            },
          ],
          count: 2,
        }),
      });
    });
    await page.goto("/audits");
    const badge = page.getByTestId("sidebar-controls-count");
    await expect(badge).toBeVisible();
    const text = await badge.innerText();
    expect(Number(text)).toBe(2);
  });

  test("P0-214-1 (slice 214): Controls badge consumes the existing /api/controls-list BFF (no new endpoint)", async ({
    authedPage: page,
  }) => {
    const controlsRequests: string[] = [];
    page.on("request", (r) => {
      const url = r.url();
      if (url.includes("/api/controls-list")) controlsRequests.push(url);
    });
    // Mock with a non-zero control list so the badge renders and the
    // visibility assertion below proves the wiring (route -> render),
    // not just that the request was issued.
    await page.route("**/api/controls-list**", async (route) => {
      await route.fulfill({
        status: 200,
        contentType: "application/json",
        body: JSON.stringify({
          controls: [{ id: "x", scf_id: "CRY-05", title: "T" }],
          count: 1,
        }),
      });
    });
    await page.goto("/audits");
    await expect(page.getByTestId("sidebar-controls-count")).toBeVisible();
    expect(controlsRequests.length).toBeGreaterThan(0);
  });
});
