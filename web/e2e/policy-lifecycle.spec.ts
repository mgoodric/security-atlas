// #2116 — Playwright E2E for policies in the app, against the real platform.
//
//   1. Create a policy from /policies/new (a linked control, a Markdown
//      body whose <script> and <img onerror> stay text), submit it; a second user approves
//      and publishes it with a review date; it shows published with that
//      date.
//   2. "Create new version" on the published policy opens a draft linked
//      to it; publishing the draft supersedes the old version.
//   3. With no control linked the form warns that publishing needs one;
//      discarding the draft removes it.
//
// The e2e user creates and submits; the approver is a second person
// seeded by fixtures/e2e/policy-lifecycle.sql, so the approval rule runs
// and nothing is self-approved. Each run titles its policies uniquely, so
// re-runs on the shared database never collide.

import type { Browser, BrowserContext, Page } from "@playwright/test";

import { ATLAS_JWT_COOKIE } from "../lib/auth";
import { expect, test } from "./fixtures";
import { DEMO_CONTROL_ID, DEMO_TENANT_ID, seedFromFixture } from "./seed";

// Two XSS payloads in the policy body (#2446): a <script> and an <img>
// whose onerror handler runs as soon as the browser fails to load it.
// Both must render as text, so window.__pwned is never set.
const SCRIPT_PAYLOAD = "<script>window.__pwned=1</script>";
const IMG_PAYLOAD = "<img src=x onerror=window.__pwned=1>";

function pwned(page: Page): Promise<number | undefined> {
  return page.evaluate(
    () => (window as unknown as { __pwned?: number }).__pwned,
  );
}

const APPROVER = "44444444-4444-4444-4444-444444442116";
const RUN = Date.now().toString(36);
const TITLE = `Access Control Policy ${RUN}`;
const REVIEW = "2027-11-16";

function atlasBaseURL(): string {
  return process.env.ATLAS_HTTP_URL ?? "http://localhost:8080";
}

async function mintApproverJWT(): Promise<string> {
  const url = `${atlasBaseURL()}/v1/test/issue-jwt`;
  const res = await fetch(url, {
    method: "POST",
    headers: { "Content-Type": "application/json" },
    body: JSON.stringify({
      tenant_id: DEMO_TENANT_ID,
      user_id: `user:${APPROVER}`,
      roles: ["admin"],
      // WORKAROUND (remove with #2428): tenant-admin JWTs can't approve
      // or publish until jwtmw derives IsAdmin/IsApprover from tenant
      // roles. The approver is still a different person from the
      // submitter, so the approval rule runs and the policy is not
      // self-approved.
      // https://github.com/mgoodric/security-atlas/issues/2428
      super_admin: true,
    }),
  });
  if (!res.ok) {
    throw new Error(
      `#2116: ${url} returned ${res.status}: ${await res.text()}`,
    );
  }
  const parsed = (await res.json()) as { token?: string };
  if (!parsed.token) throw new Error(`#2116: ${url} returned no token`);
  return parsed.token;
}

async function signedInPage(
  browser: Browser,
  baseURL: string | undefined,
  bearer: string,
): Promise<{ ctx: BrowserContext; page: Page }> {
  const url = new URL(baseURL ?? "http://localhost:3000");
  const ctx = await browser.newContext({ baseURL: url.origin });
  await ctx.addCookies([
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
  return { ctx, page: await ctx.newPage() };
}

/** Run a one-click header transition and wait for its toast. */
async function transition(page: Page, kind: string, toast: string) {
  await page.getByTestId(`policy-action-${kind}`).click();
  await page.getByTestId("policy-action-confirm-ok").click();
  await expect(page.getByTestId("toast-viewport")).toContainText(toast);
}

/** As the approver: approve the policy under review, then publish it. */
async function approveAndPublish(page: Page, policyURL: string) {
  await page.goto(policyURL);
  await expect(page.getByTestId("policy-detail-status")).toHaveText(
    "under review",
    { timeout: 30_000 },
  );
  await transition(page, "approve", "Policy approved");
  await expect(page.getByTestId("policy-detail-status")).toHaveText("approved");
  await expect(page.getByTestId("policy-detail-self-approved")).toHaveCount(0);

  await page.getByTestId("policy-action-publish").click();
  await expect(page).toHaveURL(new RegExp(`${policyURL}/publish$`));
  await page.getByTestId("policy-publish-next-review").fill(REVIEW);
  await page.getByTestId("policy-publish-submit").click();
  await expect(page).toHaveURL(new RegExp(`${policyURL}$`), {
    timeout: 30_000,
  });
  await expect(page.getByTestId("toast-viewport")).toContainText(
    "Policy published",
  );
}

test.describe("policies in the app (#2116)", () => {
  test.describe.configure({ mode: "serial" });

  let approverBearer = "";
  let v1URL = "";
  test.beforeAll(async () => {
    seedFromFixture("policy-lifecycle");
    approverBearer = await mintApproverJWT();
  });

  test("create and submit; a second user approves and publishes with a review date", async ({
    authedPage: page,
    browser,
    baseURL,
  }) => {
    await page.goto("/policies");
    await page.getByTestId("policies-new").click();
    await expect(page).toHaveURL(/\/policies\/new$/);

    await page.getByTestId("policy-title").fill(TITLE);
    await page.getByTestId("policy-owner-role").fill("security_lead");
    await page.getByTestId("policy-approver-role").fill("cto");
    await page.getByTestId("policy-ack-role-viewer").check();
    await page
      .getByTestId("policy-body")
      .fill(
        `# Purpose\n\nAccess is **least privilege**.\n\n${SCRIPT_PAYLOAD}\n\n${IMG_PAYLOAD}`,
      );

    // The preview renders Markdown through the shared component; the
    // <script> and the <img onerror> are shown as text and never run.
    await page.getByTestId("policy-body-preview-toggle").click();
    const preview = page.getByTestId("policy-body-preview");
    await expect(preview.locator("h1")).toHaveText("Purpose");
    await expect(preview).toContainText(SCRIPT_PAYLOAD);
    await expect(preview).toContainText(IMG_PAYLOAD);
    await expect(preview.locator("script")).toHaveCount(0);
    await expect(preview.locator("img")).toHaveCount(0);
    expect(await pwned(page)).toBeUndefined();
    await page.getByTestId("policy-body-preview-toggle").click();

    // Until a control is linked, the form warns before submit.
    const warning = page.getByTestId("policy-form-no-controls-warning");
    await expect(warning).toBeVisible();
    await page.getByTestId("policy-controls-option-" + DEMO_CONTROL_ID).click();
    await expect(warning).toHaveCount(0);

    await page.getByTestId("policy-submit").click();
    await expect(page).toHaveURL(/\/policies\/[0-9a-f-]{36}$/, {
      timeout: 30_000,
    });
    v1URL = new URL(page.url()).pathname;
    await expect(page.getByTestId("toast-viewport")).toContainText(
      "Policy created",
    );
    await expect(page.getByTestId("policy-detail-status")).toHaveText("draft");
    await expect(page.getByTestId("policy-detail-edit")).toBeVisible();
    const detailBody = page.getByTestId("policy-detail-body");
    await expect(detailBody).toContainText(SCRIPT_PAYLOAD);
    await expect(detailBody).toContainText(IMG_PAYLOAD);
    await expect(detailBody.locator("img")).toHaveCount(0);
    expect(await pwned(page)).toBeUndefined();

    await transition(page, "submit", "Policy submitted for review");
    await expect(page.getByTestId("policy-detail-status")).toHaveText(
      "under review",
    );
    // A policy under review is no longer editable.
    await expect(page.getByTestId("policy-detail-edit")).toHaveCount(0);

    const { ctx, page: approver } = await signedInPage(
      browser,
      baseURL,
      approverBearer,
    );
    try {
      await approveAndPublish(approver, v1URL);
    } finally {
      await ctx.close();
    }

    await page.goto(v1URL);
    await expect(page.getByTestId("policy-detail-status")).toHaveText(
      "published",
      { timeout: 30_000 },
    );
    await expect(page.getByTestId("policy-detail-next-review")).toContainText(
      "2027",
    );
    await expect(page.getByTestId("policy-action-new-version")).toHaveText(
      "Create new version",
    );
  });

  test("Create new version opens a draft linked to the published policy; publishing it supersedes the old one", async ({
    authedPage: page,
    browser,
    baseURL,
  }) => {
    expect(v1URL, "the first test publishes the policy").not.toBe("");
    const v1ID = v1URL.split("/").pop()!;

    await page.goto(v1URL);
    await page.getByTestId("policy-action-new-version").click();
    await expect(page).toHaveURL(
      new RegExp(`/policies/new\\?predecessor=${v1ID}$`),
    );
    await expect(page.getByTestId("policy-new-predecessor")).toContainText(
      TITLE,
    );
    // The form starts from the published policy, version bumped.
    await expect(page.getByTestId("policy-title")).toHaveValue(TITLE);
    await expect(page.getByTestId("policy-version")).toHaveValue("1.1");
    await page
      .getByTestId("policy-body")
      .fill("# Purpose\n\nAccess is reviewed quarterly.");

    const created = page.waitForRequest(
      (r) =>
        r.method() === "POST" && new URL(r.url()).pathname === "/api/policies",
    );
    await page.getByTestId("policy-submit").click();
    expect((await created).postDataJSON()).toMatchObject({
      predecessor_id: v1ID,
      version: "1.1",
    });
    await expect(page).toHaveURL(/\/policies\/[0-9a-f-]{36}$/, {
      timeout: 30_000,
    });
    const v2URL = new URL(page.url()).pathname;
    expect(v2URL).not.toBe(v1URL);
    await expect(page.getByTestId("policy-detail-status")).toHaveText("draft");
    await expect(page.getByTestId("policy-detail-predecessor")).toHaveAttribute(
      "href",
      v1URL,
    );

    // The published version is untouched while the draft is worked on.
    await page.goto(v1URL);
    await expect(page.getByTestId("policy-detail-status")).toHaveText(
      "published",
      { timeout: 30_000 },
    );
    await expect(page.getByTestId("policy-detail-body")).toContainText(
      "least privilege",
    );

    await page.goto(v2URL);
    await transition(page, "submit", "Policy submitted for review");

    const { ctx, page: approver } = await signedInPage(
      browser,
      baseURL,
      approverBearer,
    );
    try {
      await approveAndPublish(approver, v2URL);
    } finally {
      await ctx.close();
    }

    await page.goto(v2URL);
    await expect(page.getByTestId("policy-detail-status")).toHaveText(
      "published",
      { timeout: 30_000 },
    );
    await page.goto(v1URL);
    await expect(page.getByTestId("policy-detail-status")).toHaveText(
      "superseded",
      { timeout: 30_000 },
    );
    await expect(page.getByTestId("policy-action-new-version")).toHaveCount(0);
  });

  test("with no control linked the form warns; discarding the draft removes it", async ({
    authedPage: page,
  }) => {
    const title = `Draft To Discard ${RUN}`;
    await page.goto("/policies/new");
    await expect(
      page.getByTestId("policy-form-no-controls-warning"),
    ).toContainText("Publishing needs at least one linked control");

    await page.getByTestId("policy-title").fill(title);
    await page.getByTestId("policy-owner-role").fill("security_lead");
    await page.getByTestId("policy-approver-role").fill("cto");
    await page.getByTestId("policy-body").fill("To be discarded.");
    await page.getByTestId("policy-submit").click();
    await expect(page).toHaveURL(/\/policies\/[0-9a-f-]{36}$/, {
      timeout: 30_000,
    });
    const draftURL = new URL(page.url()).pathname;
    await expect(
      page.getByTestId("policy-detail-no-controls-warning"),
    ).toBeVisible();

    await page.getByTestId("detail-header-overflow-trigger").click();
    await page.getByTestId("policy-action-discard").click();
    await page.getByTestId("detail-header-overflow-trigger-confirm-ok").click();
    await expect(page).toHaveURL(/\/policies$/, { timeout: 30_000 });
    await expect(page.getByTestId("toast-viewport")).toContainText(
      "Draft discarded",
    );

    await page.goto(draftURL);
    await expect(page.getByTestId("authed-not-found")).toBeVisible({
      timeout: 30_000,
    });
  });
});
