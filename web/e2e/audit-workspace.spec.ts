// #2157 — Playwright for an assigned auditor's workflow.
//
// Against the real platform, no route mocks. An admin assigns the auditor
// to a period from the period's page (#2118). Signed in, the auditor finds
// the period in the audit workspace, then works it through the platform
// API: reads the period's population, draws a sample from it, and reads
// the evidence the population contains. The authz middleware decides each
// call from the audit period the route belongs to (the #2157 resource-attrs
// hydrator), so everything outside the assignment is refused: a cell the
// population does not select, the unassigned period's population and
// evidence, and a tenant-wide evidence list. The admin then removes the
// auditor, and the same reads are refused.
//
// The fixture (fixtures/e2e/audit-workspace.sql) seeds two periods, their
// attached populations, two scope cells and the evidence, plus an auditor
// user of this spec's own, and drops the user's assignments, so each run
// starts unassigned.
//
// This replaces slice 042's spec, whose assertions were all commented-out
// pseudo-steps against seed data that never existed.
//
// Hard rule (P0-A9 / GitGuardian): every id is a neutral test string.

import type { Browser, Page } from "@playwright/test";

import { ATLAS_JWT_COOKIE } from "../lib/auth";
import { expect, test } from "./fixtures";
import { DEMO_TENANT_ID, seedFromFixture } from "./seed";

const PERIOD_ID = "bbbbbbbb-bbbb-bbbb-bbbb-bbbbbbbb2157";
const PERIOD_NAME = "Audit workspace e2e (2157)";
const AUDITOR_ID = "44444444-4444-4444-4444-444444442157";
const AUDITOR_LABEL =
  "Audit Workspace E2E (audit-workspace-e2e@example.invalid)";

const POPULATION = "77777777-7777-7777-7777-77777777a157";
const UNASSIGNED_POPULATION = "77777777-7777-7777-7777-77777777b157";
const EVIDENCE_IN_SCOPE = "66666666-6666-6666-6666-66666666a157";
const EVIDENCE_OTHER_CELL = "66666666-6666-6666-6666-66666666b157";
const EVIDENCE_UNASSIGNED = "66666666-6666-6666-6666-66666666c157";

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
      user_id: `user:${AUDITOR_ID}`,
      roles: ["auditor"],
      super_admin: false,
    }),
  });
  if (!res.ok) {
    throw new Error(
      `#2157: ${url} returned ${res.status}: ${await res.text()}`,
    );
  }
  const parsed = (await res.json()) as { token?: string };
  if (!parsed.token) {
    throw new Error(`#2157: ${url} returned 200 but no token field`);
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

// One platform call as the auditor; returns the status and parsed body.
async function asAuditor(
  bearer: string,
  method: string,
  path: string,
  body?: unknown,
): Promise<{ status: number; json: Record<string, unknown> }> {
  const res = await fetch(`${atlasBaseURL()}${path}`, {
    method,
    headers: {
      Authorization: `Bearer ${bearer}`,
      ...(body === undefined ? {} : { "Content-Type": "application/json" }),
    },
    body: body === undefined ? undefined : JSON.stringify(body),
  });
  const text = await res.text();
  let json: Record<string, unknown> = {};
  try {
    json = JSON.parse(text) as Record<string, unknown>;
  } catch {
    // Non-JSON body: the status is what the spec asserts.
  }
  return { status: res.status, json };
}

// The reads that follow the assignment: path -> status while assigned.
const READS: Array<[string, number]> = [
  [`/v1/populations/${POPULATION}`, 200],
  [`/v1/populations/${UNASSIGNED_POPULATION}`, 403],
  [`/v1/evidence/${EVIDENCE_IN_SCOPE}`, 200],
  [`/v1/evidence/${EVIDENCE_OTHER_CELL}`, 403],
  [`/v1/evidence/${EVIDENCE_UNASSIGNED}`, 403],
  ["/v1/evidence", 403],
];

test.describe.configure({ mode: "serial" });

let auditorBearer = "";

test.beforeAll(async () => {
  seedFromFixture("audit-workspace");
  auditorBearer = await mintAuditorJWT();
});

test("an assigned auditor works the period's population and evidence, and nothing else", async ({
  authedPage,
  browser,
  baseURL,
}) => {
  // Unassigned, every period-scoped read is refused.
  for (const [path] of READS) {
    const { status } = await asAuditor(auditorBearer, "GET", path);
    expect(status, `unassigned GET ${path}`).toBe(403);
  }

  // The admin assigns the auditor from the period's page.
  await authedPage.goto(`/audits/${PERIOD_ID}`);
  const card = authedPage.getByTestId("audit-period-auditors");
  await expect(card).toBeVisible();
  await card
    .getByLabel("Assign an auditor")
    .selectOption({ label: AUDITOR_LABEL });
  await card.getByTestId("audit-period-auditor-assign").click();
  const row = card.locator(
    `[data-testid="audit-period-auditor"][data-user-id="${AUDITOR_ID}"]`,
  );
  await expect(row).toContainText("Audit Workspace E2E");

  // Signed in, the auditor's workspace opens on the period.
  const auditor = await auditorPage(browser, baseURL, auditorBearer);
  await auditor.goto("/audit");
  await expect(auditor.getByTestId("audit-period-bar")).toContainText(
    PERIOD_NAME,
  );
  await auditor.context().close();

  // The period's reads, and only those.
  for (const [path, want] of READS) {
    const { status } = await asAuditor(auditorBearer, "GET", path);
    expect(status, `assigned GET ${path}`).toBe(want);
  }

  // A sample drawn from the period's population holds only the in-scope
  // record; the unassigned period's population cannot be drawn from.
  const drawn = await asAuditor(auditorBearer, "POST", "/v1/samples", {
    population_id: POPULATION,
    n: 1,
    seed: "e2e-2157",
  });
  expect(drawn.status, "draw in the assigned period").toBe(201);
  const sample = drawn.json.sample as {
    id: string;
    evidence_record_ids: string[];
  };
  expect(sample.evidence_record_ids).toEqual([EVIDENCE_IN_SCOPE]);
  const read = await asAuditor(
    auditorBearer,
    "GET",
    `/v1/samples/${sample.id}`,
  );
  expect(read.status, "read own sample").toBe(200);
  const refused = await asAuditor(auditorBearer, "POST", "/v1/samples", {
    population_id: UNASSIGNED_POPULATION,
    n: 1,
    seed: "e2e-2157",
  });
  expect(refused.status, "draw in the unassigned period").toBe(403);

  // Removed, the auditor loses the period's reads on the next request.
  await row.getByTestId("audit-period-auditor-remove").click();
  await expect(row).toHaveCount(0);
  for (const path of [
    `/v1/populations/${POPULATION}`,
    `/v1/evidence/${EVIDENCE_IN_SCOPE}`,
    `/v1/samples/${sample.id}`,
  ]) {
    const { status } = await asAuditor(auditorBearer, "GET", path);
    expect(status, `removed GET ${path}`).toBe(403);
  }
});
