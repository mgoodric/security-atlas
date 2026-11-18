// #2433 — the approval rule for a framework scope, end to end against the
// real platform, in an organisation with Allow self-approval off.
//
//   1. The requester, a tenant admin, creates a scope and submits it for
//      review (through the platform API: the wizard is
//      framework-scope-wizard.spec.ts's subject). On the scope page its own
//      Approve is refused, with the platform's own words, and the scope
//      stays in review.
//   2. A second person, also a tenant admin, approves it from the same
//      page; it is not flagged self-approved. The Activate panel passes
//      the axe gate while open (the route sweep only sees the page
//      closed), and Confirm makes it the current scope.
//
// framework-scope-wizard.spec.ts covers the refusal with a mocked BFF,
// because the demo tenant leaves self-approval on; here the platform
// refuses for real. Both people sign in with tenant-role JWTs (no
// super_admin), so the approve route's tenant-role gate (#2428) is on the
// path too.
//
// The tenant, its two members and the framework version are the spec's
// own (fixtures/e2e/framework-scope-self-approval.sql), reset each run.
//
// Hard rule (P0-A9 / GitGuardian): every id is a neutral test string.

import type { Page } from "@playwright/test";

import { ATLAS_JWT_COOKIE } from "../lib/auth";
import { expectNoSeriousAxe, seriousAxeViolations } from "./axe-gate";
import { expect, test } from "./fixtures";
import { seedFromFixture } from "./seed";

const TENANT = "00000000-0000-0000-0000-000000002433";
const REQUESTER = "44444444-4444-4444-4444-444444442433";
const SECOND = "44444444-4444-4444-4444-444444442443";
const FV = "e2e24330-0000-0000-0000-0000000f0002";
const NAME = "Whole company (self-approval-2433)";

// The platform's refusal (internal/frameworkscope ErrSelfApproval).
const SELF_APPROVAL =
  "frameworkscope: the submitter cannot approve their own scope; a second approver must approve it";

function atlasBaseURL(): string {
  return process.env.ATLAS_HTTP_URL ?? "http://localhost:8080";
}

async function mintTenantAdminJWT(user: string): Promise<string> {
  const url = `${atlasBaseURL()}/v1/test/issue-jwt`;
  const res = await fetch(url, {
    method: "POST",
    headers: { "Content-Type": "application/json" },
    body: JSON.stringify({
      tenant_id: TENANT,
      user_id: `user:${user}`,
      roles: ["admin"],
      super_admin: false,
    }),
  });
  if (!res.ok) {
    throw new Error(
      `#2433: ${url} returned ${res.status}: ${await res.text()}`,
    );
  }
  const parsed = (await res.json()) as { token?: string };
  if (!parsed.token) throw new Error(`#2433: ${url} returned no token`);
  return parsed.token;
}

/** Calls the platform as `bearer`; throws on a non-2xx. */
async function platform(
  bearer: string,
  method: string,
  path: string,
  body?: unknown,
): Promise<Record<string, unknown>> {
  const res = await fetch(`${atlasBaseURL()}${path}`, {
    method,
    headers: {
      Authorization: `Bearer ${bearer}`,
      "Content-Type": "application/json",
    },
    body: body === undefined ? undefined : JSON.stringify(body),
  });
  if (!res.ok) {
    throw new Error(
      `#2433: ${method} ${path} returned ${res.status}: ${await res.text()}`,
    );
  }
  return (await res.json()) as Record<string, unknown>;
}

async function signInAs(page: Page, bearer: string): Promise<void> {
  const url = new URL(process.env.PLATFORM_BASE_URL ?? "http://localhost:3000");
  await page.context().clearCookies();
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
}

function scopeRow(page: Page) {
  return page.getByRole("row").filter({ hasText: NAME });
}

test.describe("#2433 — framework-scope self-approval off, real platform", () => {
  test.describe.configure({ mode: "serial" });

  let requester = "";
  let second = "";
  test.beforeAll(async () => {
    seedFromFixture("framework-scope-self-approval");
    requester = await mintTenantAdminJWT(REQUESTER);
    second = await mintTenantAdminJWT(SECOND);
    const created = await platform(requester, "POST", "/v1/framework-scopes", {
      framework_version_id: FV,
      name: NAME,
      predicate: { op: "true" },
    });
    const scope = created.framework_scope as { id: string };
    await platform(
      requester,
      "PATCH",
      `/v1/framework-scopes/${scope.id}/submit`,
    );
  });

  test("the requester's own approve is refused with the platform's words", async ({
    page,
  }) => {
    await signInAs(page, requester);
    await page.goto(`/framework-scopes/${FV}`);
    const row = scopeRow(page);
    await expect(row).toContainText("review", { timeout: 30_000 });

    await page.getByTestId("framework-scope-approve").click();
    await expect(page.getByText(SELF_APPROVAL)).toBeVisible();

    // Still in review, on the page and on a fresh read.
    await page.reload();
    await expect(scopeRow(page)).toContainText("review", { timeout: 30_000 });
  });

  test("a second person approves and activates; the open Activate panel passes axe", async ({
    page,
  }) => {
    await signInAs(page, second);
    await page.goto(`/framework-scopes/${FV}`);
    const approve = page.getByTestId("framework-scope-approve");
    await expect(approve).toHaveText("Approve", { timeout: 30_000 });
    await approve.click();
    const row = scopeRow(page);
    await expect(row).toContainText("approved");
    // A second person approved it, so it is not flagged.
    await expect(row.getByTestId("framework-scope-self-approved")).toHaveCount(
      0,
    );

    await page.getByTestId("framework-scope-activate").click();
    await expect(
      page.getByTestId("framework-scope-activate-panel"),
    ).toBeVisible();
    expectNoSeriousAxe(
      await seriousAxeViolations(page),
      "/framework-scopes/[framework_version_id] Activate panel open",
    );
    await page.getByRole("button", { name: "Confirm", exact: true }).click();
    await expect(row).toContainText("Current scope");
  });
});
