// #2118 — Playwright for auditor assignment on an audit period.
//
// Against the real platform, no route mocks: an admin opens the period's
// page, assigns an auditor from the Auditors picker, and the auditor,
// signed in, finds the period in the audit workspace (/audit). The admin
// then removes them, and the auditor's next visit finds no period.
//
// The fixture (fixtures/e2e/period-auditors.sql) seeds an open period and
// an auditor user of this spec's own and drops the user's assignments, so
// each run starts unassigned and no other spec's period or user changes.
//
// The auditor signs in with a JWT minted by /v1/test/issue-jwt for their
// user id, the settings-theme.spec.ts pattern. A /login session carries
// the subject "user:<uuid>", which the OPA roles lookup does not resolve
// yet, so a real-login auditor gets no auditor role until #2239 lands.
//
// Hard rule (P0-A9 / GitGuardian): every id is a neutral test string.

import type { Browser, Page } from "@playwright/test";

import { ATLAS_JWT_COOKIE } from "../lib/auth";
import { expect, test } from "./fixtures";
import { DEMO_TENANT_ID, seedFromFixture } from "./seed";

const PERIOD_ID = "bbbbbbbb-bbbb-bbbb-bbbb-bbbbbbbb2118";
const PERIOD_NAME = "Auditor assignment e2e (2118)";
const AUDITOR_ID = "44444444-4444-4444-4444-444444442118";
const AUDITOR_LABEL = "Period Auditor E2E (period-auditor-e2e@example.invalid)";

function atlasBaseURL(): string {
  return process.env.ATLAS_HTTP_URL ?? "http://localhost:8080";
}

async function mintAuditorJWT(): Promise<string> {
  const url = `${atlasBaseURL()}/v1/test/issue-jwt`;
  const res = await fetch(url, {
    method: "POST",
    headers: { "Content-Type": "application/json" },
    body: JSON.stringify({
      tenant_id: DEMO_TENANT_ID,
      user_id: AUDITOR_ID,
      roles: ["auditor"],
      super_admin: false,
    }),
  });
  if (!res.ok) {
    throw new Error(
      `#2118: ${url} returned ${res.status}: ${await res.text()}`,
    );
  }
  const parsed = (await res.json()) as { token?: string };
  if (!parsed.token) {
    throw new Error(`#2118: ${url} returned 200 but no token field`);
  }
  return parsed.token;
}

async function auditorPage(
  browser: Browser,
  baseURL: string | undefined,
  bearer: string,
): Promise<Page> {
  const url = new URL(baseURL ?? "http://localhost:3000");
  const context = await browser.newContext({ baseURL: url.origin });
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

test.describe.configure({ mode: "serial" });

let auditorBearer = "";

test.beforeAll(async () => {
  seedFromFixture("period-auditors");
  auditorBearer = await mintAuditorJWT();
});

test("an admin assigns an auditor, who then reaches the period in the audit workspace", async ({
  authedPage,
  browser,
  baseURL,
}) => {
  const auditor = await auditorPage(browser, baseURL, auditorBearer);

  // Unassigned, the auditor has no period.
  await auditor.goto("/audit");
  await expect(auditor.getByTestId("audit-no-period")).toBeVisible();

  // The admin assigns them from the period's page.
  await authedPage.goto(`/audits/${PERIOD_ID}`);
  const card = authedPage.getByTestId("audit-period-auditors");
  await expect(card).toBeVisible();
  await expect(card.getByTestId("audit-period-auditors-empty")).toBeVisible();
  await card
    .getByLabel("Assign an auditor")
    .selectOption({ label: AUDITOR_LABEL });
  await card.getByTestId("audit-period-auditor-assign").click();
  const row = card.locator(
    `[data-testid="audit-period-auditor"][data-user-id="${AUDITOR_ID}"]`,
  );
  await expect(row).toContainText("Period Auditor E2E");

  // Signed in as the auditor, the workspace opens on the period.
  await auditor.goto("/audit");
  await expect(auditor.getByTestId("audit-period-bar")).toContainText(
    PERIOD_NAME,
  );

  // The admin removes them; the auditor's next visit finds no period.
  await row.getByTestId("audit-period-auditor-remove").click();
  await expect(row).toHaveCount(0);
  await auditor.goto("/audit");
  await expect(auditor.getByTestId("audit-no-period")).toBeVisible();

  await auditor.context().close();
});

test("the auditor cannot see or change the period's auditors", async ({
  browser,
  baseURL,
}) => {
  const auditor = await auditorPage(browser, baseURL, auditorBearer);
  await auditor.goto(`/audits/${PERIOD_ID}`);
  await expect(
    auditor.getByTestId("audit-period-auditors-refused"),
  ).toBeVisible();
  await expect(auditor.getByTestId("audit-period-auditor-picker")).toHaveCount(
    0,
  );
  await auditor.context().close();
});
