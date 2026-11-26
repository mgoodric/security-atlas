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

import type { Risk } from "@/lib/api/risks";

import { expect, test } from "./fixtures";
import { pickFirstUser } from "./owner-picker";
import { seedFromFixture } from "./seed";

// #2531: the owner picker lists real users, so seed the one it picks.
test.beforeAll(() => {
  seedFromFixture("owner-picker");
});

const NEW_RISK_TITLE = "E2E test risk — unauthorized data export";
const CREATED_RISK_ID = "00000000-0000-0000-0000-0000000r1s01";

test.describe("/risks/new risk-create form", () => {
  test("empty-state CTA on /risks routes to /risks/new", async ({
    authedPage: page,
  }) => {
    // True-zero ledger so the "Add first risk" CTA renders (not the
    // filter-empty "Clear filters" CTA). The register sends its page as a
    // query string (#2132), so the route matches one.
    await page.route(/\/api\/risks(\?.*)?$/, async (route, req) => {
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
    let postedAcceptanceStatus: string | undefined;
    let postedJustification: string | undefined;
    let postedProposedUntil: string | undefined;
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
      const posted = JSON.parse(req.postData() ?? "{}") as {
        acceptance_status?: string;
        justification?: string;
        proposed_until?: string;
      };
      postedAcceptanceStatus = posted.acceptance_status;
      postedJustification = posted.justification;
      postedProposedUntil = posted.proposed_until;
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
    await pickFirstUser(page, "risks-create-owner");
    // treatment=accept avoids the mitigate-requires-linked-control
    // client-side validation rule (validateRiskForm). The form has no
    // accepter / expiry inputs, so it posts a proposed acceptance (#1746),
    // with the proposal's justification and proposed expiry (#2391).
    await page.getByTestId("risks-create-treatment").selectOption("accept");
    await page
      .getByTestId("risks-create-justification")
      .fill("Compensated by the edge filter.");
    await page.getByTestId("risks-create-proposed-until").fill("2099-03-31");

    const createResp = page.waitForResponse(
      (r) =>
        r.url().includes("/api/risks") &&
        r.request().method() === "POST" &&
        r.status() === 201,
      { timeout: 30_000 },
    );
    await page.getByTestId("risks-create-submit").click();
    await createResp;
    expect(postedAcceptanceStatus).toBe("proposed");
    expect(postedJustification).toBe("Compensated by the edge filter.");
    expect(postedProposedUntil).toBe("2099-03-31");

    // #2089 create convention: land on the new risk with a toast.
    await expect(page).toHaveURL(new RegExp(`/risks/${CREATED_RISK_ID}$`), {
      timeout: 30_000,
    });
    await expect(page.getByTestId("toast")).toHaveText(/Risk created/);
  });

  test("AC-1: a fresh tenant (zero controls) creates a risk through the default flow without linking a control", async ({
    authedPage: page,
  }) => {
    // Slice 663 regression guard, revised by #1746. The form opens on
    // treatment "untreated" (no decision yet, no required fields), so a
    // brand-new operator can fill
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
                treatment: "untreated",
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
            treatment: "untreated",
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

    // The form opens on "untreated", never a real treatment decision.
    await expect(page.getByTestId("risks-create-treatment")).toHaveValue(
      "untreated",
    );
    // The control picker is NOT rendered on the default flow (treatment
    // is "untreated"), so there is no unsatisfiable required field.
    await expect(
      page.getByTestId("risks-create-control-multi-select"),
    ).toHaveCount(0);

    // Fill only the required fields and submit the default flow.
    await page.getByTestId("risks-create-title").fill(NEW_RISK_TITLE);
    await pickFirstUser(page, "risks-create-owner");

    const createResp = page.waitForResponse(
      (r) =>
        r.url().includes("/api/risks") &&
        r.request().method() === "POST" &&
        r.status() === 201,
      { timeout: 30_000 },
    );
    await page.getByTestId("risks-create-submit").click();
    await createResp;

    // The default-flow submit posts treatment=untreated (#1746) and
    // lands on the new risk (#2089).
    expect(postedTreatment).toBe("untreated");
    await expect(page).toHaveURL(new RegExp(`/risks/${CREATED_RISK_ID}$`), {
      timeout: 30_000,
    });
  });

  test("empty title is gated client-side: no server round-trip, input preserved", async ({
    authedPage: page,
  }) => {
    // #2089: the form is noValidate, so the browser shows no native
    // bubble; validateRiskForm renders `risks-create-title-error` inline
    // beside the field, linked by aria-describedby. No POST /api/risks
    // fires, the page stays on /risks/new, and the user's other input is
    // preserved. If a POST DID fire on an empty title, that would be the
    // bug this spec guards against.
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

    // Leave title empty; pick an owner + a no-link treatment. Submit.
    const owner = await pickFirstUser(page, "risks-create-owner");
    await page.getByTestId("risks-create-treatment").selectOption("accept");
    await page.getByTestId("risks-create-submit").click();

    // The inline error renders beside the title and is wired to it.
    const titleError = page.getByTestId("risks-create-title-error");
    await expect(titleError).toHaveText(/required/i);
    const title = page.getByTestId("risks-create-title");
    await expect(title).toHaveAttribute("aria-invalid", "true");
    await expect(title).toHaveAttribute(
      "aria-describedby",
      (await titleError.getAttribute("id"))!,
    );
    // No native bubble: the form opts out of constraint validation, so
    // the browser never blocked the submit itself.
    expect(
      await page
        .getByTestId("risks-create-form")
        .evaluate((f) => (f as HTMLFormElement).noValidate),
    ).toBe(true);

    // Form state preserved (owner not cleared) — honest UX.
    await expect(page.getByTestId("risks-create-owner")).toHaveValue(owner);
    // Stayed on the create form; no server round-trip happened.
    await expect(page).toHaveURL(/\/risks\/new$/);
    expect(postFired).toBe(false);
  });

  test("#2511: Score later posts no inherent score and the risk reads as Unscored", async ({
    authedPage: page,
  }) => {
    // The whole wire shape the platform returns, typed so a field the
    // detail page reads cannot be left out of the mock.
    const unscored: Risk = {
      id: CREATED_RISK_ID,
      title: NEW_RISK_TITLE,
      description: "",
      category: "operational",
      methodology: "nist_800_30",
      inherent_score: {},
      treatment: "untreated",
      treatment_owner: "e2e-owner",
      residual_score: {},
      review_due_at: "2027-01-01T00:00:00Z",
      accepter: "",
      instrument_reference: "",
      linked_control_ids: [],
      created_at: "2026-11-17T00:00:00Z",
      updated_at: "2026-11-17T00:00:00Z",
      themes: [],
      severity: 0,
      status: "open",
    };
    let posted: Record<string, unknown> | undefined;
    await page.route("**/api/risks", async (route, req) => {
      if (req.method() !== "POST") {
        await route.fallback();
        return;
      }
      posted = JSON.parse(req.postData() ?? "{}") as Record<string, unknown>;
      await route.fulfill({
        status: 201,
        contentType: "application/json",
        body: JSON.stringify({ risk: unscored }),
      });
    });
    await page.route(`**/api/risks/${CREATED_RISK_ID}`, async (route, req) => {
      if (req.method() !== "GET") {
        await route.fallback();
        return;
      }
      await route.fulfill({
        status: 200,
        contentType: "application/json",
        body: JSON.stringify({ risk: unscored }),
      });
    });

    await page.goto("/risks/new");
    await expect(page.getByTestId("risks-create-form")).toBeVisible({
      timeout: 30_000,
    });
    await page.getByTestId("risks-create-title").fill(NEW_RISK_TITLE);
    await pickFirstUser(page, "risks-create-owner");

    await page.getByTestId("risks-create-score-later").check();
    await expect(page.getByTestId("risks-create-inherent-score")).toHaveCount(
      0,
    );

    const createResp = page.waitForResponse(
      (r) =>
        r.url().includes("/api/risks") &&
        r.request().method() === "POST" &&
        r.status() === 201,
      { timeout: 30_000 },
    );
    await page.getByTestId("risks-create-submit").click();
    await createResp;
    expect(posted).toBeDefined();
    expect(posted).not.toHaveProperty("inherent_score");

    await expect(page).toHaveURL(new RegExp(`/risks/${CREATED_RISK_ID}$`), {
      timeout: 30_000,
    });
    await expect(page.getByTestId("risk-detail-unscored")).toHaveText(
      "Unscored",
      { timeout: 30_000 },
    );
    await expect(page.getByTestId("risk-detail-residual")).toHaveText(
      "Unscored",
    );
  });
});
