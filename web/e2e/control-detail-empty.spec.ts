// Slice 152 — Playwright E2E for the control detail 404 empty-state.
//
// Seeded by fixtures/e2e/control-detail-empty.sql: a tenant of the spec's
// own with ZERO controls (the slice 152 fresh-install scenario). The spec
// signs in to it with an admin JWT minted by /v1/test/issue-jwt, as
// combined-views-modules.spec.ts does. #2711 restored the assertions slice
// 152 left commented out behind the slice-082 quarantine.
//
// Run locally:
//   cd web
//   npx playwright install chromium     # once per machine
//   npx playwright test e2e/control-detail-empty.spec.ts
//
// AC coverage (from PRD ISC-26..ISC-30 + slice doc AC-4 / AC-5):
//   * /controls/<bogus-uuid> renders the friendly empty-state
//   * NO generic 404 page is shown
//   * "Back to controls list" CTA is present and clickable
//   * The destructive error Alert is NOT rendered (404 must not
//     misroute to the 5xx branch)
//   * The list page's truly-zero empty-state appears for a tenant with
//     no controls

import type { Page } from "@playwright/test";

import { expect, test } from "./fixtures";
import { seedFromFixture } from "./seed";

// Mirrors the tenant in fixtures/e2e/control-detail-empty.sql.
const TENANT = "00000000-0000-0000-0000-000000002711";
// A syntactically valid UUID that resolves to no control in any tenant.
const BOGUS = "00000000-0000-0000-0000-000000000152";

function atlasBaseURL(): string {
  return process.env.ATLAS_HTTP_URL ?? "http://localhost:8080";
}

async function mintAdminJWT(): Promise<string> {
  const url = `${atlasBaseURL()}/v1/test/issue-jwt`;
  const res = await fetch(url, {
    method: "POST",
    headers: { "Content-Type": "application/json" },
    body: JSON.stringify({
      tenant_id: TENANT,
      roles: ["admin"],
      super_admin: true,
    }),
  });
  if (!res.ok) {
    throw new Error(
      `#2711: ${url} returned ${res.status}: ${await res.text()}`,
    );
  }
  const parsed = (await res.json()) as { token?: string };
  if (!parsed.token) {
    throw new Error(`#2711: ${url} returned 200 but no token field`);
  }
  return parsed.token;
}

async function signInToEmptyTenant(page: Page): Promise<void> {
  const url = new URL(process.env.PLATFORM_BASE_URL ?? "http://localhost:3000");
  await page.context().clearCookies();
  await page.context().addCookies([
    {
      name: "atlas_jwt",
      value: await mintAdminJWT(),
      domain: url.hostname,
      path: "/",
      httpOnly: true,
      secure: url.protocol === "https:",
      sameSite: "Lax",
    },
  ]);
}

test.describe("control detail 404 empty-state (slice 152)", () => {
  test.beforeAll(() => {
    seedFromFixture("control-detail-empty");
  });

  test.beforeEach(async ({ page }) => {
    await signInToEmptyTenant(page);
  });

  test("AC-1: visiting /controls/<bogus-uuid> renders the friendly empty-state", async ({
    page,
  }) => {
    await page.goto(`/controls/${BOGUS}`);
    // The friendly empty-state renders (slice 152 D1-c).
    const empty = page.getByTestId("control-detail-empty");
    await expect(empty).toBeVisible({ timeout: 30_000 });
    await expect(empty).toContainText(
      "no control instantiated in your tenant yet",
    );
    // The destructive Alert is NOT rendered (404 must not misroute to
    // the 5xx branch — vitest covers the classifier, this pins the
    // end-to-end render).
    await expect(page.getByTestId("control-detail-error")).toHaveCount(0);
  });

  test("AC-2: empty-state CTA links back to /controls", async ({ page }) => {
    await page.goto(`/controls/${BOGUS}`);
    const cta = page.getByTestId("control-detail-empty-cta");
    await expect(cta).toBeVisible({ timeout: 30_000 });
    await expect(cta).toHaveText("Back to controls list");
    await expect(cta).toHaveAttribute("href", "/controls");
    await cta.click();
    await expect(page).toHaveURL(/\/controls$/);
  });

  test("AC-3: empty-state names the bogus id honestly in the body copy", async ({
    page,
  }) => {
    // The body names the path id so the operator can correlate it with
    // the URL they clicked (ATLAS-012 swaps in the SCF code when the id
    // is an anchor; a bogus id has no anchor, so the UUID shows).
    // Slice 152 D-152-2 — copy is HONEST about cause, not "may have been
    // deleted".
    await page.goto(`/controls/${BOGUS}`);
    await expect(page.getByTestId("control-detail-empty")).toContainText(
      BOGUS,
      { timeout: 30_000 },
    );
  });

  test("AC-4: NO generic Next.js 404 page is shown for the bogus id path", async ({
    page,
  }) => {
    // The (authed) layout + the page-level empty-state mean Next.js never
    // invokes the not-found.tsx render.
    await page.goto(`/controls/${BOGUS}`);
    await expect(page.getByTestId("control-detail-empty")).toBeVisible({
      timeout: 30_000,
    });
    await expect(page.locator("body")).not.toContainText(
      "This page could not be found",
    );
  });

  test("AC-5: list page renders the truly-zero empty-state for a tenant with no controls", async ({
    page,
  }) => {
    // Slice 152 D1-b, as reshaped by #1741: in the default view "truly
    // empty" means the tenant has no controls, and the list says so
    // rather than showing the filter-narrowed "no match" copy.
    await page.goto("/controls");
    const empty = page.getByTestId("list-empty-state");
    await expect(empty).toBeVisible({ timeout: 30_000 });
    await expect(empty).toContainText("No controls in your tenant yet");
    await expect(empty).not.toContainText("No controls match these filters");
  });
});
