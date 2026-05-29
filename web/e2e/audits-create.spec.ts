// Slice 149 — Playwright E2E for the /audits/new audit-period-create
// form.
//
// Un-quarantined by slice 351 (AC-4, disposition (a)). The legacy
// `test.skip(!PLAYWRIGHT_RUN_QUARANTINED)` + commented bodies were the
// slice-082-era placeholder. The `/audits/new` page
// (audit-period-form.tsx) ships every asserted testid; no underlying
// product bug. Rewritten as a LIVE mocked spec per the
// `questionnaires.spec.ts` `route.fulfill` convention (P0-4).
//
// Audit-period create is a v1 binary success-test flow (it is the entry
// point to the SOC 2 audit workflow). Coverage matters.
//
// Determinism: navigation + submit are gated on `expect(...)` auto-wait
// and `page.waitForResponse(...)`. No sleeps; no `.count()` snapshots.
//
// Hard rule (P0-A9): neutral test strings only; no vendor-prefixed
// tokens.

import { expect, test } from "./fixtures";

const FRAMEWORK_VERSION_ID = "11111111-1111-1111-1111-111111110002";
const NEW_PERIOD_NAME = "Q3 2026 SOC 2 Type II";
const CREATED_PERIOD_ID = "bbbbbbbb-bbbb-bbbb-bbbb-bbbb000ap099";

test.describe("/audits/new audit-period-create form", () => {
  test("P0-AUD-1: empty-state CTA on /audits routes to /audits/new (not /admin)", async ({
    authedPage: page,
  }) => {
    // True-zero period list so the "Create audit period" CTA renders.
    await page.route("**/api/audits", async (route, req) => {
      if (req.method() !== "GET") {
        await route.fallback();
        return;
      }
      await route.fulfill({
        status: 200,
        contentType: "application/json",
        body: JSON.stringify({ audit_periods: [], count: 0 }),
      });
    });

    const listResp = page.waitForResponse(
      (r) => r.url().includes("/api/audits") && r.status() === 200,
      { timeout: 30_000 },
    );
    await page.goto("/audits");
    await listResp;

    const cta = page.getByTestId("list-empty-state-cta");
    await expect(cta).toBeVisible({ timeout: 30_000 });
    await expect(cta).toContainText(/create audit period/i);
    await cta.click();

    await expect(page).toHaveURL(/\/audits\/new$/, { timeout: 30_000 });
    // The slice-149 fix: the CTA must NOT bounce to /admin (the original
    // operator-reported bug).
    await expect(page).not.toHaveURL(/\/admin/);
  });

  test("submits an audit period and routes back to /audits with the new row", async ({
    authedPage: page,
  }) => {
    let created = false;
    await page.route("**/api/audits", async (route, req) => {
      if (req.method() === "GET") {
        const periods = created
          ? [
              {
                id: CREATED_PERIOD_ID,
                name: NEW_PERIOD_NAME,
                framework_version_id: FRAMEWORK_VERSION_ID,
                period_start: "2026-07-01T00:00:00Z",
                period_end: "2026-09-30T00:00:00Z",
                status: "open",
                created_by: "e2e",
                created_at: "2026-05-29T00:00:00Z",
                updated_at: "2026-05-29T00:00:00Z",
              },
            ]
          : [];
        await route.fulfill({
          status: 200,
          contentType: "application/json",
          body: JSON.stringify({
            audit_periods: periods,
            count: periods.length,
          }),
        });
        return;
      }
      // POST = create. Returns the slice-149 AuditPeriodCreated shape.
      created = true;
      await route.fulfill({
        status: 201,
        contentType: "application/json",
        body: JSON.stringify({
          id: CREATED_PERIOD_ID,
          name: NEW_PERIOD_NAME,
          status: "open",
        }),
      });
    });

    await page.goto("/audits/new");
    await expect(page.getByTestId("audits-create-form")).toBeVisible({
      timeout: 30_000,
    });

    await page.getByTestId("audits-create-name").fill(NEW_PERIOD_NAME);
    await page
      .getByTestId("audits-create-framework-version-id")
      .fill(FRAMEWORK_VERSION_ID);
    // Native date inputs take YYYY-MM-DD; the form appends T00:00:00Z.
    await page.getByTestId("audits-create-period-start").fill("2026-07-01");
    await page.getByTestId("audits-create-period-end").fill("2026-09-30");

    const createResp = page.waitForResponse(
      (r) =>
        r.url().includes("/api/audits") &&
        r.request().method() === "POST" &&
        r.status() === 201,
      { timeout: 30_000 },
    );
    await page.getByTestId("audits-create-submit").click();
    await createResp;

    await expect(page).toHaveURL(/\/audits(\?|$)/, { timeout: 30_000 });
    await expect(
      page
        .getByTestId("audits-row-name")
        .filter({ hasText: NEW_PERIOD_NAME })
        .first(),
    ).toBeVisible({ timeout: 30_000 });
  });

  test("upstream 4xx surfaces inline without clearing form input", async ({
    authedPage: page,
  }) => {
    // The audit-period form has NO client-side validation gate (unlike
    // risks-create) — it posts and surfaces the server error inline. We
    // return a 400 to assert the inline-error + preserved-input UX.
    await page.route("**/api/audits", async (route, req) => {
      if (req.method() === "GET") {
        await route.fulfill({
          status: 200,
          contentType: "application/json",
          body: JSON.stringify({ audit_periods: [], count: 0 }),
        });
        return;
      }
      await route.fulfill({
        status: 400,
        contentType: "application/json",
        body: JSON.stringify({ error: "period_start must be <= period_end" }),
      });
    });

    await page.goto("/audits/new");
    await expect(page.getByTestId("audits-create-form")).toBeVisible({
      timeout: 30_000,
    });

    await page.getByTestId("audits-create-name").fill(NEW_PERIOD_NAME);
    await page
      .getByTestId("audits-create-framework-version-id")
      .fill(FRAMEWORK_VERSION_ID);
    // Intentionally inverted range to trigger the server 400.
    await page.getByTestId("audits-create-period-start").fill("2026-09-30");
    await page.getByTestId("audits-create-period-end").fill("2026-07-01");

    const createResp = page.waitForResponse(
      (r) =>
        r.url().includes("/api/audits") &&
        r.request().method() === "POST" &&
        r.status() === 400,
      { timeout: 30_000 },
    );
    await page.getByTestId("audits-create-submit").click();
    await createResp;

    await expect(page.getByTestId("audits-create-error")).toBeVisible({
      timeout: 30_000,
    });
    // Input preserved — the operator can fix the range and resubmit.
    await expect(page.getByTestId("audits-create-name")).toHaveValue(
      NEW_PERIOD_NAME,
    );
    await expect(page).toHaveURL(/\/audits\/new$/);
  });
});
