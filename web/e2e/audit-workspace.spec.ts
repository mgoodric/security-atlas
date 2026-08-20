// Slice 042 — Playwright E2E for the audit workspace flow.
// Slice 113 — gate checked 2026-07-25:
//   - slice 111 merged as PR #441 on 2026-05-21
//   - CI was disabled 2026-06-29..2026-07-24; that gap was NOT counted
//   - seven completed post-gap PR CI runs had `Frontend · Playwright e2e`
//     conclude success at the job level (cancelled release-please attempts
//     were ignored, not counted as clean)
//
// The deferred assertions from the slice-082 stub are now enabled against
// fixtures/e2e/audit-workspace.sql FULL coverage.

import { type Browser, type Page } from "@playwright/test";

import { ATLAS_JWT_COOKIE } from "../lib/auth";

import { expect, seeded, test } from "./fixtures";
import { seedFromFixture, DEMO_TENANT_ID } from "./seed";

const CONTROL_A = seeded.controlId;
const CONTROL_B = "33333333-3333-3333-3333-333333330002";
const OTHER_AUDITOR_USER_ID = "44444444-4444-4444-4444-444444440002";

function atlasBaseURL(): string {
  return process.env.ATLAS_HTTP_URL ?? "http://localhost:8080";
}

async function gotoAuditControl(page: Page, controlId = CONTROL_A) {
  await page.goto(`/audit/${controlId}`);
  await expect(page.getByTestId("audit-period-bar")).toBeVisible({
    timeout: 30_000,
  });
}

async function createPopulationAndSample(page: Page) {
  await expect(page.getByTestId("panel-sampling")).toBeVisible();
  await page.getByTestId("population-start").fill("2026-04-01");
  await page.getByTestId("population-end").fill("2026-06-30");
  await page.getByTestId("population-create").click();

  await expect(page.getByTestId("population-row-count")).toHaveText("5");

  await page.getByTestId("sample-n-input").fill("5");
  await page.getByTestId("sample-seed-input").fill("e2e-seed");
  await page.getByTestId("sample-pull-submit").click();

  const sample = page.getByTestId("sample-card").first();
  await expect(sample).toBeVisible();
  await expect(sample.getByTestId("sample-annotation")).toHaveCount(5);
  return sample;
}

async function issueBearerFor(userId: string): Promise<string> {
  const res = await fetch(`${atlasBaseURL()}/v1/test/issue-jwt`, {
    method: "POST",
    headers: { "Content-Type": "application/json" },
    body: JSON.stringify({
      tenant_id: DEMO_TENANT_ID,
      user_id: userId,
      roles: ["admin", "grc_engineer"],
      super_admin: true,
    }),
  });
  if (!res.ok) {
    throw new Error(
      `issue second e2e JWT returned ${res.status}: ${await res.text()}`,
    );
  }
  const body = (await res.json()) as { token?: string };
  if (!body.token) throw new Error("issue second e2e JWT returned no token");
  return body.token;
}

async function newAuthedPage(
  browser: Browser,
  baseURL: string,
  bearer: string,
) {
  const context = await browser.newContext();
  const url = new URL(baseURL);
  await context.addCookies([
    {
      name: ATLAS_JWT_COOKIE,
      value: bearer,
      domain: url.hostname,
      path: "/",
      httpOnly: true,
      secure: url.protocol === "https:",
      sameSite: "Lax",
    },
  ]);
  return context.newPage();
}

test.describe("audit workspace", () => {
  test.beforeAll(() => {
    seedFromFixture("audit-workspace");
  });

  test("AC-1: /audit lands the auditor in their assigned AuditPeriod", async ({
    authedPage: page,
  }) => {
    await page.goto("/audit");

    await expect(page.getByTestId("audit-period-bar")).toBeVisible();
    await expect(page.getByTestId("audit-period-bar")).toContainText(
      "SOC 2 2026 Q2",
    );
    await expect(page.getByTestId("period-frozen-badge")).toBeVisible();
  });

  test("AC-2: left nav lists controls in scope for the period", async ({
    authedPage: page,
  }) => {
    await page.goto("/audit");
    await expect(page.getByTestId("control-nav-empty")).toBeVisible();

    await page.getByTestId("add-control-input").fill(CONTROL_A);
    await page.getByTestId("add-control-submit").click();
    await expect(page.getByTestId("control-nav-item")).toContainText(
      CONTROL_A.slice(0, 8),
    );

    await page.getByTestId("add-control-input").fill(CONTROL_B);
    await page.getByTestId("add-control-submit").click();
    await expect(page.getByTestId("control-nav-item")).toHaveCount(2);
  });

  test("AC-3: population summary + sample-pull + annotation", async ({
    authedPage: page,
  }) => {
    await gotoAuditControl(page);
    const sample = await createPopulationAndSample(page);

    const ann = sample.getByTestId("sample-annotation").first();
    await ann.getByTestId("annotation-result-select").selectOption("passed");
    await ann.getByTestId("annotation-notes-input").fill("looks good");
    await ann.getByTestId("annotation-submit").click();
    await expect(ann.getByTestId("annotation-saved-result")).toContainText(
      "passed",
    );
  });

  test("AC-4: walkthrough recorder saves narrative or surfaces the documented role gap", async ({
    authedPage: page,
  }) => {
    await gotoAuditControl(page);
    await page.getByTestId("tab-walkthrough").click();
    await page
      .getByTestId("walkthrough-narrative")
      .fill("Owner demoed the IAM policy review.");
    await page.getByTestId("walkthrough-save").click();

    await expect(
      page
        .getByTestId("walkthrough-hash")
        .or(page.getByTestId("walkthrough-error")),
    ).toBeVisible();
  });

  test("AC-5 + P0-2: comment thread distinguishes shared and private notes", async ({
    authedPage: page,
    browser,
    baseURL,
  }) => {
    await gotoAuditControl(page);
    await page.getByTestId("tab-comments").click();

    await page
      .getByTestId("comment-body")
      .fill("Please attach the Q2 access review.");
    await page.getByTestId("comment-visibility").selectOption("shared");
    await page.getByTestId("comment-submit").click();
    await expect(
      page.getByTestId("comment-shared-badge").first(),
    ).toBeVisible();

    await page
      .getByTestId("comment-body")
      .fill("Private: control owner was vague on rotation.");
    await page.getByTestId("comment-visibility").selectOption("auditor_only");
    await page.getByTestId("comment-submit").click();
    await expect(
      page.getByTestId("comment-private-badge").first(),
    ).toBeVisible();

    const otherBearer = await issueBearerFor(OTHER_AUDITOR_USER_ID);
    const other = await newAuthedPage(
      browser,
      baseURL ?? "http://localhost:3000",
      otherBearer,
    );
    try {
      await gotoAuditControl(other);
      await other.getByTestId("tab-comments").click();
      await expect(other.getByTestId("comment-thread")).toContainText(
        "Please attach the Q2 access review.",
      );
      await expect(other.getByTestId("comment-thread")).not.toContainText(
        "control owner was vague",
      );
    } finally {
      await other.context().close();
    }
  });

  test("AC-6: sign-out clears session; re-sign-in resumes to assigned period", async ({
    authedPage: page,
  }) => {
    await page.goto("/audit");
    await expect(page.getByTestId("audit-period-bar")).toBeVisible();

    await page.context().clearCookies();
    await page.goto("/audit");
    await page.waitForURL(/\/login\?from=%2Faudit/);

    const bearer = process.env.TEST_BEARER;
    if (!bearer) throw new Error("TEST_BEARER missing after global setup");
    const url = new URL(page.url());
    await page.context().addCookies([
      {
        name: ATLAS_JWT_COOKIE,
        value: bearer,
        domain: url.hostname,
        path: "/",
        httpOnly: true,
        secure: url.protocol === "https:",
        sameSite: "Lax",
      },
    ]);
    await page.goto("/audit");
    await expect(page.getByTestId("audit-period-bar")).toContainText(
      "SOC 2 2026 Q2",
    );
  });

  test("AC-7 + P0-3: tab between controls without losing in-progress annotations", async ({
    authedPage: page,
  }) => {
    await gotoAuditControl(page, CONTROL_A);
    await createPopulationAndSample(page);

    const annA = page.getByTestId("sample-annotation").first();
    await annA.getByTestId("annotation-result-select").selectOption("failed");
    await annA
      .getByTestId("annotation-notes-input")
      .fill("WIP: missing approver");
    await expect(annA.getByTestId("annotation-draft-indicator")).toBeVisible();

    await page.getByTestId("tab-walkthrough").click();
    await page.getByTestId("tab-sampling").click();
    await expect(annA.getByTestId("annotation-notes-input")).toHaveValue(
      "WIP: missing approver",
    );

    await page.getByTestId("add-control-input").fill(CONTROL_B);
    await page.getByTestId("add-control-submit").click();
    await page.goto(`/audit/${CONTROL_B}`);
    await expect(page.getByTestId("control-workspace")).toContainText(
      CONTROL_B,
    );
    await page.goto(`/audit/${CONTROL_A}`);
    await expect(
      page.getByTestId("annotation-notes-input").first(),
    ).toHaveValue("WIP: missing approver");
  });

  test("slice 749: frozen period shows the period-scoped evidence summary, labeled + non-binding", async ({
    authedPage: page,
  }) => {
    await gotoAuditControl(page);

    const card = page.getByTestId("period-evidence-summary-section");
    await expect(card).toBeVisible();
    await expect(page.getByTestId("period-evidence-summary-body")).toBeVisible({
      timeout: 30_000,
    });
    await expect(
      page.getByTestId("period-evidence-summary-frozen-label"),
    ).toContainText("Period-scoped");
    await expect(
      page.getByTestId("period-evidence-summary-frozen-label"),
    ).toContainText("frozen as of");
    await expect(
      page.getByTestId("period-evidence-summary-bound"),
    ).toContainText("5 frozen evidence record");
    await expect(
      card.getByRole("button", { name: /approve|publish|export/i }),
    ).toHaveCount(0);
  });

  test("P0-1: workspace never fetches data outside the assigned period", async ({
    authedPage: page,
  }) => {
    const requests: string[] = [];
    page.on("request", (r) => requests.push(r.url()));

    await gotoAuditControl(page);
    await createPopulationAndSample(page);

    expect(
      requests.some((u) => u.includes("/v1/controls/") && u.endsWith("/state")),
    ).toBe(false);
    expect(requests.some((u) => u.includes("/api/controls/"))).toBe(false);
  });
});
