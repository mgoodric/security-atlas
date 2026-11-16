// #2115 — the framework-scope wizard, and approval on the scope page.
//
//   1. Against the real platform (fixtures/e2e/framework-scope-wizard.sql):
//      the dashboard tile says "Scope not set". The e2e user opens the
//      wizard from the index, picks the framework version in the entity
//      picker, scopes Everything (choose by dimension with nothing picked
//      refuses submit: the picker's null is never sent), names it and
//      submits it for approval. A second user, a tenant admin (the
//      fixture's user_roles row) signed in with a JWT minted by
//      /v1/test/issue-jwt, approves and activates it from the
//      page's header, and the tile turns into a percentage. The dashboard
//      prefetches its panels on the server, so this one cannot be mocked.
//   2. The submitter's own Approve is refused, and a second user approves
//      and activates.
//   3. A by-dimension scope round-trips through Advanced JSON: the
//      picker's predicate shows as JSON, an edit to it shows back in the
//      picker, `null` typed there is refused, and the create request
//      carries the edited predicate.
//   4. Changing an activated scope opens a new draft revision through the
//      create route (from_scope_id); the activated one is never patched
//      and still reads as the current scope.
//
// MOCK STRATEGY (2-4): the framework-scope, framework and scope-dimension
// BFFs are mocked on each browser context with one shared in-memory store,
// so the two users see the same scope. Each context's mock answers as its
// own person, and approve applies the approval rule (approver differs from
// submitter), which the Go tier covers for real against Postgres
// (internal/frameworkscope). The demo tenant leaves Allow self-approval on
// for the seeded scope specs, so the real platform would accept the
// submitter's approve (flagged self-approved) in 1.
//
// Hard rule (P0-A9 / GitGuardian): every id is a neutral test string.

import AxeBuilder from "@axe-core/playwright";
import type {
  Browser,
  BrowserContext,
  Page,
  Request,
  Route,
} from "@playwright/test";

import { ATLAS_JWT_COOKIE } from "../lib/auth";
import { describeAxe, seriousAxeViolations } from "./axe-gate";
import { expect, test } from "./fixtures";
import { DEMO_TENANT_ID, seedFromFixture } from "./seed";

// The real-platform test's version and approver (the fixture's ids).
const REAL_FV = "e2e21150-0000-0000-0000-0000000f0002";
const REAL_LABEL = "2115-wizard-e2e";
const REAL_APPROVER = "44444444-4444-4444-4444-444444442115";

const FV = "00000000-0000-4000-8000-000000002115";
const FRAMEWORK = "00000000-0000-4000-8000-0000000021f0";
const FRAMEWORK_NAME = "Mock scope framework";
const SUBMITTER = "00000000-0000-0000-0000-0000000021e1";
const APPROVER = "00000000-0000-0000-0000-0000000021e2";

// The platform's refusal (internal/frameworkscope ErrSelfApproval).
const SELF_APPROVAL =
  "frameworkscope: the submitter cannot approve their own scope; a second approver must approve it";

type Row = Record<string, unknown> & {
  id: string;
  state: string;
  predicate: unknown;
  name: string;
};

type Store = {
  scopes: Row[];
  /** Every write the page sent: method, path and body. */
  writes: { method: string; path: string; body: unknown }[];
};

function json(body: unknown, status = 200) {
  return {
    status,
    contentType: "application/json",
    body: JSON.stringify(body),
  };
}

function newRow(
  store: Store,
  name: string,
  predicate: unknown,
  state = "draft",
  over: Record<string, unknown> = {},
): Row {
  const n = store.scopes.length + 1;
  const now = new Date(Date.UTC(2026, 10, 1, 0, n)).toISOString();
  const row: Row = {
    id: `00000000-0000-4000-8000-00000021500${n}`,
    framework_version_id: FV,
    name,
    state,
    predicate,
    predicate_hash: `hash-${n}`,
    self_approved: false,
    created_at: now,
    updated_at: now,
    ...over,
  };
  store.scopes.push(row);
  return row;
}

/** Mocks the scope page's BFFs on `ctx`, answering as `me`. */
async function mockScopes(ctx: BrowserContext, store: Store, me: string) {
  await ctx.route(/\/api\/frameworks(\?|$)/, (route: Route) =>
    route.fulfill(
      json({
        frameworks: [
          {
            id: FRAMEWORK,
            name: FRAMEWORK_NAME,
            slug: "scope-wizard-e2e",
            issuer: "e2e",
            latest_version_id: FV,
            versions: [{ id: FV, version: "2026", status: "current" }],
          },
        ],
      }),
    ),
  );
  await ctx.route("**/api/scope-dimensions", (route: Route) =>
    route.fulfill(
      json({
        dimensions: [
          {
            name: "environment",
            value_type: "string",
            allowed_values: ["prod", "staging"],
            is_required: false,
            is_builtin: true,
          },
        ],
      }),
    ),
  );
  await ctx.route("**/api/scope-cells", (route: Route) =>
    route.fulfill(json({ cells: [] })),
  );
  await ctx.route(
    /\/api\/framework-scopes(\/|\?|$)/,
    async (route: Route, req: Request) => {
      const url = new URL(req.url());
      const parts = url.pathname.split("/").filter(Boolean); // api, framework-scopes, ...
      const method = req.method();
      const body = req.postData() ? req.postDataJSON() : undefined;
      const now = new Date().toISOString();
      if (method !== "GET") {
        store.writes.push({ method, path: url.pathname, body });
      }

      if (parts.length === 2 && method === "GET") {
        const fv = url.searchParams.get("framework_version");
        return route.fulfill(
          json({
            framework_scopes: store.scopes.filter(
              (s) => !fv || s.framework_version_id === fv,
            ),
          }),
        );
      }
      if (parts.length === 2 && method === "POST") {
        const b = body as Record<string, unknown>;
        if (typeof b.from_scope_id === "string") {
          const src = store.scopes.find((s) => s.id === b.from_scope_id);
          if (!src) return route.fulfill(json({ error: "not found" }, 404));
          const row = newRow(store, src.name, src.predicate);
          return route.fulfill(json({ framework_scope: row }, 201));
        }
        if (b.predicate === null || typeof b.predicate !== "object") {
          return route.fulfill(json({ error: "predicate is required" }, 400));
        }
        const row = newRow(store, b.name as string, b.predicate);
        return route.fulfill(json({ framework_scope: row }, 201));
      }

      const row = store.scopes.find((s) => s.id === parts[2]);
      if (!row) return route.fulfill(json({ error: "not found" }, 404));
      if (parts[3] === "applicability" && parts[4] === "diff") {
        const base = store.scopes.find((s) => s.state === "activated") ?? null;
        return route.fulfill(
          json({
            framework_scope: row,
            baseline: base,
            predicate: {
              changed:
                JSON.stringify(base?.predicate) !==
                JSON.stringify(row.predicate),
              before: base?.predicate ?? null,
              after: row.predicate,
            },
            changes: [],
            reference_changes: [],
          }),
        );
      }
      if (parts.length === 3 && method === "PATCH") {
        if (row.state === "activated" || row.state === "superseded") {
          return route.fulfill(json({ error: "scope is frozen" }, 409));
        }
        const p = (body as { predicate: unknown }).predicate;
        if (p === null || typeof p !== "object") {
          return route.fulfill(json({ error: "predicate is required" }, 400));
        }
        Object.assign(row, { predicate: p, state: "draft", updated_at: now });
        return route.fulfill(
          json({ framework_scope: row, approval_invalidated: false }),
        );
      }
      if (parts.length === 4 && method === "PATCH") {
        const t = parts[3];
        const from: Record<string, string> = {
          submit: "draft",
          approve: "review",
          activate: "approved",
        };
        if (row.state !== from[t]) {
          return route.fulfill(json({ error: `wrong state for ${t}` }, 409));
        }
        if (t === "submit") {
          Object.assign(row, { state: "review", submitted_by: me });
        } else if (t === "approve") {
          if (row.submitted_by === me) {
            return route.fulfill(json({ error: SELF_APPROVAL }, 403));
          }
          Object.assign(row, {
            state: "approved",
            approver_user_id: me,
            approved_at: now,
          });
        } else {
          for (const s of store.scopes) {
            if (s.state === "activated") {
              Object.assign(s, { state: "superseded", superseded_at: now });
            }
          }
          Object.assign(row, {
            state: "activated",
            effective_from: (body as { effective_from: string }).effective_from,
          });
        }
        row.updated_at = now;
        return route.fulfill(json({ framework_scope: row }));
      }
      return route.fulfill(json({ error: "not mocked" }, 404));
    },
  );
}

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
      user_id: `user:${REAL_APPROVER}`,
      roles: ["admin"],
    }),
  });
  if (!res.ok) {
    throw new Error(
      `#2115: ${url} returned ${res.status}: ${await res.text()}`,
    );
  }
  const parsed = (await res.json()) as { token?: string };
  if (!parsed.token) throw new Error(`#2115: ${url} returned no token`);
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

function scopeRow(page: Page, name: string) {
  return page.getByRole("row").filter({ hasText: name });
}

function posted(store: Store) {
  return store.writes.filter(
    (w) => w.method === "POST" && w.path === "/api/framework-scopes",
  );
}

test.describe("framework-scope wizard against the platform (#2115)", () => {
  test.describe.configure({ mode: "serial" });

  let approverBearer = "";
  test.beforeAll(async () => {
    seedFromFixture("framework-scope-wizard");
    approverBearer = await mintApproverJWT();
  });

  test("create with Everything, submit, a second user approves and activates; the tile shows a percentage", async ({
    authedPage: page,
    browser,
    baseURL,
  }) => {
    const NAME = "Whole company (real-2115)";
    const tile = page.getByTestId("framework-tile").filter({
      hasText: REAL_LABEL,
    });

    // No activated scope: the tile says so.
    await page.goto("/dashboard");
    await expect(tile.getByTestId("framework-tile-scope-not-set")).toHaveText(
      "Scope not set",
      { timeout: 30_000 },
    );
    await expect(tile.getByTestId("framework-tile-coverage")).toHaveCount(0);

    // The empty scope page offers the wizard, not an API instruction.
    await page.goto(`/framework-scopes/${REAL_FV}`);
    await expect(page.getByTestId("framework-scope-empty-create")).toHaveText(
      "Create a scope",
    );
    await expect(page.getByTestId("coming-soon")).toHaveCount(0);

    // The wizard, from the index.
    await page.goto("/framework-scopes");
    await page.getByTestId("framework-scopes-new").click();
    await expect(page).toHaveURL(/\/framework-scopes\/new$/);
    await expect(page.getByTestId("framework-scope-wizard")).toBeVisible();
    const pageFindings = await seriousAxeViolations(page);
    expect(pageFindings, describeAxe(pageFindings)).toEqual([]);

    // Pick the framework version; the open list passes the widget axe scan.
    const versionInput = page.getByTestId("framework-scope-wizard-version");
    await versionInput.click();
    await page.keyboard.type(REAL_LABEL);
    await expect(page.getByRole("listbox")).toBeVisible();
    const open = await new AxeBuilder({ page })
      .include('[data-testid="framework-scope-wizard-version-picker"]')
      .include('[role="listbox"]')
      .analyze();
    expect(
      open.violations.filter(
        (v) => v.impact === "serious" || v.impact === "critical",
      ),
    ).toEqual([]);
    await page.getByRole("option", { name: new RegExp(REAL_LABEL) }).click();
    await expect(versionInput).toHaveValue(new RegExp(REAL_LABEL));

    await page.getByTestId("framework-scope-wizard-name").fill(NAME);

    // Choose by dimension with nothing picked: the picker reports null and
    // the wizard refuses to submit it.
    const submit = page.getByTestId("framework-scope-wizard-submit");
    await page.getByTestId("scope-picker-dimensions").check();
    await expect(submit).toBeDisabled();
    await page.getByTestId("scope-picker-everything").check();
    await expect(submit).toBeEnabled();
    await expect(submit).toHaveText("Submit for approval");
    const created = page.waitForRequest(
      (r) =>
        r.method() === "POST" &&
        new URL(r.url()).pathname === "/api/framework-scopes",
    );
    await submit.click();
    expect((await created).postDataJSON()).toEqual({
      framework_version_id: REAL_FV,
      name: NAME,
      predicate: { op: "true" },
    });

    await expect(page).toHaveURL(new RegExp(`/framework-scopes/${REAL_FV}$`));
    await expect(page.getByTestId("toast-viewport")).toContainText(
      "Scope submitted for approval",
    );
    await expect(scopeRow(page, NAME)).toContainText("review");

    // A second user approves and activates from the header.
    const { ctx, page: approver } = await signedInPage(
      browser,
      baseURL,
      approverBearer,
    );
    try {
      await approver.goto(`/framework-scopes/${REAL_FV}`);
      const approve = approver.getByTestId("framework-scope-approve");
      await expect(approve).toHaveText("Approve", { timeout: 30_000 });
      await approve.click();
      const row = scopeRow(approver, NAME);
      await expect(row).toContainText("approved");
      // A second person approved it, so it is not flagged.
      await expect(
        row.getByTestId("framework-scope-self-approved"),
      ).toHaveCount(0);

      await approver.getByTestId("framework-scope-activate").click();
      await approver
        .getByRole("button", { name: "Confirm", exact: true })
        .click();
      await expect(row).toContainText("Current scope");
    } finally {
      await ctx.close();
    }

    // The tile now shows a percentage: one of the two requirements is
    // covered. Each goto prefetches the dashboard afresh on the server.
    await expect(async () => {
      await page.goto("/dashboard");
      await expect(tile.getByTestId("framework-tile-coverage")).toHaveText(
        "50.0%",
        { timeout: 2_000 },
      );
    }).toPass({ timeout: 20_000 });
    await expect(tile.getByTestId("framework-tile-scope-not-set")).toHaveCount(
      0,
    );
  });
});

test.describe("framework-scope wizard (#2115)", () => {
  test("the submitter's own approve is refused; a second user approves and activates", async ({
    authedPage: page,
    browser,
    baseURL,
  }) => {
    const store: Store = { scopes: [], writes: [] };
    await mockScopes(page.context(), store, SUBMITTER);

    // The wizard, from the index.
    await page.goto("/framework-scopes");
    await page.getByTestId("framework-scopes-new").click();
    await expect(page).toHaveURL(/\/framework-scopes\/new$/);
    const versionInput = page.getByTestId("framework-scope-wizard-version");
    await versionInput.click();
    await page.keyboard.type("Mock scope");
    await page
      .getByRole("option", { name: new RegExp(FRAMEWORK_NAME) })
      .click();
    await expect(versionInput).toHaveValue(new RegExp(FRAMEWORK_NAME));

    await page
      .getByTestId("framework-scope-wizard-name")
      .fill("Production systems (test-2115)");

    // Choose by dimension with nothing picked: the picker reports null and
    // the wizard refuses to submit it.
    const submit = page.getByTestId("framework-scope-wizard-submit");
    await page.getByTestId("scope-picker-dimensions").check();
    await expect(submit).toBeDisabled();
    await page.getByTestId("scope-picker-everything").check();
    await expect(submit).toBeEnabled();
    await expect(submit).toHaveText("Submit for approval");
    await submit.click();

    await expect(page).toHaveURL(new RegExp(`/framework-scopes/${FV}$`));
    await expect(page.getByTestId("toast-viewport")).toContainText(
      "Scope submitted for approval",
    );
    expect(posted(store)).toHaveLength(1);
    expect(posted(store)[0].body).toEqual({
      framework_version_id: FV,
      name: "Production systems (test-2115)",
      predicate: { op: "true" },
    });
    const row = scopeRow(page, "Production systems (test-2115)");
    await expect(row).toContainText("review");

    // The submitter's own approve is refused by the platform.
    await page.getByTestId("framework-scope-approve").click();
    await expect(page.getByText(SELF_APPROVAL)).toBeVisible();
    await expect(row).toContainText("review");
    expect(store.scopes[0].state).toBe("review");

    // A second user approves and activates from the header.
    const approverCtx = await browser.newContext({
      baseURL,
      storageState: await page.context().storageState(),
    });
    try {
      await mockScopes(approverCtx, store, APPROVER);
      const approver = await approverCtx.newPage();
      await approver.goto(`/framework-scopes/${FV}`);
      const approve = approver.getByTestId("framework-scope-approve");
      await expect(approve).toHaveText("Approve", { timeout: 30_000 });
      await approve.click();
      await expect(
        scopeRow(approver, "Production systems (test-2115)"),
      ).toContainText("approved");

      await approver.getByTestId("framework-scope-activate").click();
      await expect(
        approver.getByTestId("framework-scope-activate-panel"),
      ).toBeVisible();
      await approver
        .getByRole("button", { name: "Confirm", exact: true })
        .click();
      await expect(
        scopeRow(approver, "Production systems (test-2115)"),
      ).toContainText("Current scope");
      expect(store.scopes[0]).toMatchObject({
        state: "activated",
        submitted_by: SUBMITTER,
        approver_user_id: APPROVER,
      });
    } finally {
      await approverCtx.close();
    }
  });

  test("a by-dimension scope round-trips through Advanced JSON", async ({
    authedPage: page,
  }) => {
    const store: Store = { scopes: [], writes: [] };
    await mockScopes(page.context(), store, SUBMITTER);

    // Opened from a version's page, the version is pre-filled.
    await page.goto(`/framework-scopes/new?framework_version_id=${FV}`);
    await expect(
      page.getByTestId("framework-scope-wizard-version"),
    ).toHaveValue(new RegExp(FRAMEWORK_NAME), { timeout: 30_000 });
    await page
      .getByTestId("framework-scope-wizard-name")
      .fill("Production only (test-2115)");

    await page.getByTestId("scope-picker-dimensions").check();
    await page.getByTestId("scope-picker-value-environment-prod").click();
    const eq = { op: "eq", dim: "environment", value: "prod" };
    await expect(page.getByTestId("scope-picker-predicate")).toHaveText(
      JSON.stringify(eq, null, 2),
    );

    // Advanced shows the picker's predicate as JSON.
    const toggle = page.getByTestId("framework-scope-wizard-advanced");
    await toggle.click();
    const editor = page.getByTestId("framework-scope-wizard-json");
    expect(JSON.parse(await editor.inputValue())).toEqual(eq);

    // `null` is refused: no submit, and the picker stays closed.
    const submit = page.getByTestId("framework-scope-wizard-submit");
    await editor.fill("null");
    await expect(submit).toBeDisabled();
    await toggle.click();
    await expect(
      page.getByTestId("framework-scope-wizard-json-error"),
    ).toContainText("is a JSON object");
    await expect(editor).toBeVisible();

    // An edit to the JSON shows back in the picker.
    const both = { op: "in", dim: "environment", values: ["prod", "staging"] };
    await editor.fill(JSON.stringify(both));
    await toggle.click();
    await expect(page.getByTestId("scope-picker")).toBeVisible();
    await expect(
      page.getByTestId("scope-picker-value-environment-prod"),
    ).toBeChecked();
    await expect(
      page.getByTestId("scope-picker-value-environment-staging"),
    ).toBeChecked();

    // And back again, unchanged.
    await toggle.click();
    expect(JSON.parse(await editor.inputValue())).toEqual(both);
    await toggle.click();

    await submit.click();
    await expect(page).toHaveURL(new RegExp(`/framework-scopes/${FV}$`));
    expect(posted(store)).toHaveLength(1);
    expect(posted(store)[0].body).toEqual({
      framework_version_id: FV,
      name: "Production only (test-2115)",
      predicate: both,
    });
    // No write ever carried a null predicate.
    for (const w of store.writes) {
      expect(
        (w.body as { predicate?: unknown } | undefined)?.predicate,
      ).not.toBe(null);
    }
  });

  test("changing an activated scope opens a new draft revision; the activated one is unchanged", async ({
    authedPage: page,
  }) => {
    const store: Store = { scopes: [], writes: [] };
    const active = newRow(
      store,
      "Whole company (test-2115)",
      { op: "true" },
      "activated",
      { submitted_by: APPROVER, effective_from: "2026-11-01T00:00:00Z" },
    );
    await mockScopes(page.context(), store, SUBMITTER);

    await page.goto(`/framework-scopes/${FV}`);
    // The new revision copies the name, so the activated row is found by
    // its state.
    const activeRow = page
      .getByRole("row")
      .filter({ hasText: "Current scope" });
    await expect(activeRow).toContainText("Whole company (test-2115)", {
      timeout: 30_000,
    });
    // The activated revision is read-only: no editor on its row.
    await expect(
      activeRow.getByTestId("framework-scope-predicate-frozen"),
    ).toBeVisible();
    await expect(
      activeRow.getByRole("button", { name: "Save predicate" }),
    ).toHaveCount(0);

    await page.getByTestId("framework-scope-new-revision").click();
    await expect(page.getByTestId("framework-scope-submit")).toHaveText(
      "Submit for approval",
    );
    expect(posted(store)).toEqual([
      {
        method: "POST",
        path: "/api/framework-scopes",
        body: { from_scope_id: active.id },
      },
    ]);
    const draft = store.scopes[1];
    expect(draft.state).toBe("draft");

    // Edit the new draft's predicate.
    const draftRow = page.getByRole("row").filter({ hasText: "draft" });
    await draftRow
      .getByRole("textbox")
      .fill(JSON.stringify({ op: "eq", dim: "environment", value: "prod" }));
    await draftRow.getByRole("button", { name: "Save predicate" }).click();
    await expect
      .poll(() => store.scopes[1].predicate)
      .toEqual({ op: "eq", dim: "environment", value: "prod" });

    // The activated revision was never written to, and is still current.
    expect(store.writes.filter((w) => w.path.includes(active.id))).toEqual([]);
    expect(active).toMatchObject({
      state: "activated",
      predicate: { op: "true" },
    });
    await expect(activeRow).toHaveCount(1);
    await expect(activeRow).toContainText("activated");
  });
});
