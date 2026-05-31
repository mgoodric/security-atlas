// Slice 073 — Playwright E2E for the first-time login UX (AC-12).
//
// Two assertions, both driven by page.route() mocking of
// /api/install-state (the slice-123 BFF in front of the platform's
// /v1/install-state public endpoint):
//
//   (a) When the mocked endpoint returns first_install=true, the login
//       page renders the FirstInstallGuidance card with the three-bullet
//       discovery list.
//   (b) When the mocked endpoint returns first_install=false, the
//       guidance card is absent.
//
// We use page.route() rather than requiring a real fresh-install fixture
// because the seed-data harness gap from slice 069's AC-5 PARTIAL still
// applies. The route mock pattern mirrors slice 069 + dashboard.spec.ts
// AC-7 (route.abort / continue with delay).
//
// Slice 123 — mock target moved from `**/v1/install-state` to
// `**/api/install-state`. The slice-073 implementation fetched
// `/v1/install-state` from the login page's Server Component (Node-side
// fetch), but `page.route()` only sees BROWSER network traffic, so the
// mock never fired and the spec hit the real atlas response in CI
// (returning first_install=false, hiding the card, timing out
// `toBeVisible`). Slice 123 routes the read through a BFF
// (`/api/install-state`) called from a client island
// (`<FirstInstallCard>`), so the mock now intercepts as intended.
//
// Hard rule (P0-A9 inherited from slice 069's fixtures): no
// vendor-prefixed tokens in test strings. The mocked install-state body
// is metadata only — no token plaintext ever appears here.
//
// Slice 394 — the two happy-path install-state mocks now load their body
// from the recorded contract golden (`web/lib/contracts/install-state.golden.json`)
// via `fulfillFromGolden`, so the e2e mock cannot drift from the
// provider's recorded `{first_install}` wire shape (slice 334 P-1 / ADR-0007).
// The third test (the 503 fallback) is the AC-3 escape hatch: there is no
// recorded body for an error, so it stays a hand-written `status: 503`
// fulfill — see `fulfill-from-golden.ts` + decisions log D3.

import { expect, test } from "@playwright/test";

import { fulfillFromGolden } from "./test-utils/fulfill-from-golden";

test.describe("first-time login UX", () => {
  test("renders the first-install guidance when /v1/install-state reports fresh", async ({
    page,
  }) => {
    await page.route("**/api/install-state", (route) =>
      // Golden variant: a fresh install (no tenant yet). `first_install: true`.
      fulfillFromGolden(route, "install-state", "fresh_install_without_tenant"),
    );

    await page.goto("/login");

    const guidance = page.getByTestId("first-install-card");
    await expect(guidance).toBeVisible();
    await expect(guidance).toContainText("First time signing in?");
    await expect(guidance).toContainText("docker-compose:");
    await expect(guidance).toContainText("Helm:");
    await expect(guidance).toContainText("Bare binary:");
    // The grep-line shape is in the discovery commands; assert at least
    // one BOOTSTRAP_TOKEN mention.
    await expect(guidance).toContainText("BOOTSTRAP_TOKEN");
    // The existing token form is preserved (P0-A5).
    await expect(page.getByLabel("Bearer token")).toBeVisible();
  });

  test("hides the first-install guidance when /v1/install-state reports not-fresh", async ({
    page,
  }) => {
    await page.route("**/api/install-state", (route) =>
      // Golden variant: a completed first install. `first_install: false`.
      fulfillFromGolden(route, "install-state", "post_first_install"),
    );

    await page.goto("/login");

    await expect(page.getByTestId("first-install-card")).toHaveCount(0);
    // The existing copy renders unchanged (P0-A5 regression guard).
    await expect(page.getByLabel("Bearer token")).toBeVisible();
  });

  test("falls back to not-fresh when the metadata endpoint 503s", async ({
    page,
  }) => {
    // P0-A5: a metadata failure must NOT block the production sign-in
    // path. The login page renders the existing copy verbatim and the
    // guidance card is absent.
    //
    // Slice 394 AC-3 escape hatch: there is no recorded golden body for an
    // error response (the goldens are 200-shape happy-path bodies), so an
    // error variant stays a hand-written `route.fulfill` with the status
    // under test. This is the documented, intentional non-golden path —
    // see `fulfill-from-golden.ts` header + decisions log D3.
    await page.route("**/api/install-state", async (route) => {
      await route.fulfill({ status: 503, body: "" });
    });

    await page.goto("/login");

    await expect(page.getByTestId("first-install-card")).toHaveCount(0);
    await expect(page.getByLabel("Bearer token")).toBeVisible();
  });
});
