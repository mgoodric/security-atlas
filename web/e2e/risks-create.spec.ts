// Slice 105 — Playwright E2E for the /risks/new risk-create form.
//
// Un-quarantined by slice 351 (AC-4, disposition (a)). The legacy
// `test.skip(!PLAYWRIGHT_RUN_QUARANTINED)` guard + commented bodies were
// a slice-082-era placeholder ("seed harness not landed yet"). The
// harness DID land (slice 082 + 201) and the `/risks/new` page
// (risk-form.tsx) ships every asserted testid. There is no underlying
// product bug — so per anti-criterion P0-4 this is rewritten as a LIVE
// mocked spec following the `questionnaires.spec.ts` `route.fulfill`
// convention.
//
// HONESTY CORRECTION vs the old commented bodies (the project's
// UI-honesty value): the old `submits a risk` test relied on the
// default treatment (`mitigate`) NOT requiring a linked control, and
// the old `upstream 4xx` test expected an empty title to bounce off the
// SERVER. Both are now stale:
//
//   - `mitigate` requires >=1 linked control CLIENT-SIDE
//     (validateRiskForm), so a bare "fill title + submit" with the
//     default treatment is blocked client-side. This spec selects
//     treatment `accept` for the happy path (no link rule).
//   - Empty title is gated CLIENT-SIDE — it renders
//     `risks-create-title-error` inline and never contacts the server.
//     This spec asserts the ACTUAL client-side behaviour, not the
//     obsolete server-bounce.
//
// Determinism: navigation + submit are gated on `expect(...)` auto-wait
// and `page.waitForResponse(...)`; no sleeps, no `.count()` snapshots.
//
// Hard rule (P0-A9): neutral test strings only; no vendor-prefixed
// tokens.

import { expect, test } from "./fixtures";

const NEW_RISK_TITLE = "E2E test risk — unauthorized data export";
const CREATED_RISK_ID = "00000000-0000-0000-0000-0000000r1s01";

test.describe("/risks/new risk-create form", () => {
  test("empty-state CTA on /risks routes to /risks/new", async ({
    authedPage: page,
  }) => {
    // True-zero ledger so the "Add first risk" CTA renders (not the
    // filter-empty "Clear filters" CTA).
    await page.route("**/api/risks", async (route, req) => {
      if (req.method() !== "GET") {
        await route.fallback();
        return;
      }
      await route.fulfill({
        status: 200,
        contentType: "application/json",
        body: JSON.stringify({ risks: [], count: 0 }),
      });
    });

    const listResp = page.waitForResponse(
      (r) => r.url().includes("/api/risks") && r.status() === 200,
      { timeout: 30_000 },
    );
    await page.goto("/risks");
    await listResp;

    const cta = page.getByTestId("list-empty-state-cta");
    await expect(cta).toBeVisible({ timeout: 30_000 });
    await expect(cta).toContainText(/add first risk/i);
    await cta.click();
    await expect(page).toHaveURL(/\/risks\/new$/, { timeout: 30_000 });
  });

  test("submits a risk (treatment=accept) and routes back to /risks with the new row", async ({
    authedPage: page,
  }) => {
    // List GET: empty before create, holds the new row after.
    let created = false;
    await page.route("**/api/risks", async (route, req) => {
      if (req.method() === "GET") {
        const risks = created
          ? [
              {
                id: CREATED_RISK_ID,
                title: NEW_RISK_TITLE,
                category: "operational",
                treatment: "accept",
                treatment_owner: "e2e-owner",
                methodology: "nist_800_30",
                inherent_score: { likelihood: 3, impact: 3, severity: 9 },
                status: "open",
              },
            ]
          : [];
        await route.fulfill({
          status: 200,
          contentType: "application/json",
          body: JSON.stringify({ risks, count: risks.length }),
        });
        return;
      }
      // POST = create. The BFF returns { risk } on 201 (slice 105
      // contract — see web/app/(authed)/risks/new/actions.ts).
      created = true;
      await route.fulfill({
        status: 201,
        contentType: "application/json",
        body: JSON.stringify({
          risk: {
            id: CREATED_RISK_ID,
            title: NEW_RISK_TITLE,
            category: "operational",
            treatment: "accept",
            treatment_owner: "e2e-owner",
            methodology: "nist_800_30",
            inherent_score: { likelihood: 3, impact: 3, severity: 9 },
            status: "open",
          },
        }),
      });
    });

    await page.goto("/risks/new");
    await expect(page.getByTestId("risks-create-form")).toBeVisible({
      timeout: 30_000,
    });

    await page.getByTestId("risks-create-title").fill(NEW_RISK_TITLE);
    await page.getByTestId("risks-create-treatment-owner").fill("e2e-owner");
    // treatment=accept avoids the mitigate-requires-linked-control
    // client-side validation rule (validateRiskForm).
    await page.getByTestId("risks-create-treatment").selectOption("accept");

    const createResp = page.waitForResponse(
      (r) =>
        r.url().includes("/api/risks") &&
        r.request().method() === "POST" &&
        r.status() === 201,
      { timeout: 30_000 },
    );
    await page.getByTestId("risks-create-submit").click();
    await createResp;

    // AC: routed back to /risks and the new row appears (cache
    // invalidation + re-fetch).
    await expect(page).toHaveURL(/\/risks(\?|$)/, { timeout: 30_000 });
    await expect(
      page
        .getByTestId("risks-row-title")
        .filter({ hasText: NEW_RISK_TITLE })
        .first(),
    ).toBeVisible({ timeout: 30_000 });
  });

  test("AC-1: a fresh tenant (zero controls) creates a risk through the default flow without linking a control", async ({
    authedPage: page,
  }) => {
    // Slice 663 regression guard. The form opens on treatment "avoid"
    // (the fresh-tenant-safe default), so a brand-new operator can fill
    // only the required fields and submit — no unsatisfiable
    // mitigate-requires-control gate. We do NOT touch the treatment
    // dropdown: the default flow itself must succeed.
    let created = false;
    let postedTreatment: string | undefined;

    // Empty tenant: zero active controls.
    await page.route("**/api/controls-list", async (route) => {
      await route.fulfill({
        status: 200,
        contentType: "application/json",
        body: JSON.stringify({ controls: [], count: 0 }),
      });
    });

    await page.route("**/api/risks", async (route, req) => {
      if (req.method() === "GET") {
        const risks = created
          ? [
              {
                id: CREATED_RISK_ID,
                title: NEW_RISK_TITLE,
                category: "operational",
                treatment: "avoid",
                treatment_owner: "e2e-owner",
                methodology: "nist_800_30",
                inherent_score: { likelihood: 3, impact: 3, severity: 9 },
                status: "open",
              },
            ]
          : [];
        await route.fulfill({
          status: 200,
          contentType: "application/json",
          body: JSON.stringify({ risks, count: risks.length }),
        });
        return;
      }
      const body = JSON.parse(req.postData() ?? "{}") as { treatment?: string };
      postedTreatment = body.treatment;
      created = true;
      await route.fulfill({
        status: 201,
        contentType: "application/json",
        body: JSON.stringify({
          risk: {
            id: CREATED_RISK_ID,
            title: NEW_RISK_TITLE,
            category: "operational",
            treatment: "avoid",
            treatment_owner: "e2e-owner",
            methodology: "nist_800_30",
            inherent_score: { likelihood: 3, impact: 3, severity: 9 },
            status: "open",
          },
        }),
      });
    });

    await page.goto("/risks/new");
    await expect(page.getByTestId("risks-create-form")).toBeVisible({
      timeout: 30_000,
    });

    // The control picker is NOT rendered on the default flow (treatment
    // is "avoid"), so there is no unsatisfiable required field.
    await expect(
      page.getByTestId("risks-create-control-multi-select"),
    ).toHaveCount(0);

    // Fill only the required fields and submit the default flow.
    await page.getByTestId("risks-create-title").fill(NEW_RISK_TITLE);
    await page.getByTestId("risks-create-treatment-owner").fill("e2e-owner");

    const createResp = page.waitForResponse(
      (r) =>
        r.url().includes("/api/risks") &&
        r.request().method() === "POST" &&
        r.status() === 201,
      { timeout: 30_000 },
    );
    await page.getByTestId("risks-create-submit").click();
    await createResp;

    // The default-flow submit posts treatment=avoid (the slice 663
    // fresh-tenant-safe default) and routes back with the new row.
    expect(postedTreatment).toBe("avoid");
    await expect(page).toHaveURL(/\/risks(\?|$)/, { timeout: 30_000 });
    await expect(
      page
        .getByTestId("risks-row-title")
        .filter({ hasText: NEW_RISK_TITLE })
        .first(),
    ).toBeVisible({ timeout: 30_000 });
  });

  test("empty title is gated client-side: no server round-trip, input preserved", async ({
    authedPage: page,
  }) => {
    // HONEST behaviour (verified locally): the title <input> carries the
    // native HTML5 `required` attribute, so the browser blocks the form
    // submit BEFORE React's `handleSubmit` runs when title is empty.
    // That means: no POST /api/risks fires, the page stays on
    // /risks/new, and the user's other input is preserved. (The
    // React-level `risks-create-title-error` testid only renders when
    // native validation is bypassed — it is the belt to native
    // validation's suspenders; this spec asserts the gate that actually
    // fires first.) If a POST DID fire on an empty title, that would be
    // the bug this spec guards against.
    let postFired = false;
    await page.route("**/api/risks", async (route, req) => {
      if (req.method() === "POST") {
        postFired = true;
      }
      await route.fulfill({
        status: 200,
        contentType: "application/json",
        body: JSON.stringify({ risks: [], count: 0 }),
      });
    });

    await page.goto("/risks/new");
    await expect(page.getByTestId("risks-create-form")).toBeVisible({
      timeout: 30_000,
    });

    // Leave title empty; fill owner + pick a no-link treatment. Submit.
    await page.getByTestId("risks-create-treatment-owner").fill("e2e-owner");
    await page.getByTestId("risks-create-treatment").selectOption("accept");
    await page.getByTestId("risks-create-submit").click();

    // The title input is flagged invalid by native validation; the form
    // did not submit. Assert the form did not navigate and no POST went
    // out. The title input reports the native invalid state.
    const titleInvalid = await page
      .getByTestId("risks-create-title")
      .evaluate((el) => (el as HTMLInputElement).validity.valueMissing);
    expect(titleInvalid).toBe(true);

    // Form state preserved (owner not cleared) — honest UX.
    await expect(page.getByTestId("risks-create-treatment-owner")).toHaveValue(
      "e2e-owner",
    );
    // Stayed on the create form; no server round-trip happened.
    await expect(page).toHaveURL(/\/risks\/new$/);
    expect(postFired).toBe(false);
  });
});
