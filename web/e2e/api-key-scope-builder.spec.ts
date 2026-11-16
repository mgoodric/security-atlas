// #2088 — the API-key scope builder.
//
//   1. A key issued with two evidence kinds and one scope dimension picked
//      is stored with exactly those kinds and the equivalent predicate
//      JSON. The issue and the read-back go to the real platform; only
//      the pick lists (registered kinds, scope dimensions) are mocked so
//      the values are fixed.
//   2. The raw JSON toggle round-trips a predicate the builder can show,
//      and explains one it can't while keeping the editor open.
//
// Controls and frameworks are not part of a key's scope: nothing on the
// push path enforces them (#2435), so the builder does not offer them.

import type { BrowserContext, Route } from "@playwright/test";

import { expect, test } from "./fixtures";

const KINDS = ["e2e.scope_builder.alpha.v1", "e2e.scope_builder.beta.v1"];
const UNPICKED = "e2e.scope_builder.gamma.v1";

function json(body: unknown) {
  return {
    status: 200,
    contentType: "application/json",
    body: JSON.stringify(body),
  };
}

async function mockPickLists(ctx: BrowserContext) {
  await ctx.route("**/api/evidence-kinds", (route: Route) =>
    route.fulfill(
      json({
        schemas: [...KINDS, UNPICKED].map((kind) => ({
          kind,
          semver: "1.0.0",
          owner: "platform",
        })),
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
}

type IssuedCred = { id: string };
type ListedCred = {
  id: string;
  scope_predicate: string;
  allowed_kinds: string[];
};

test.describe("API key scope builder (#2088)", () => {
  test("a key issued from the pickers stores the equivalent scope", async ({
    authedPage: page,
  }) => {
    await mockPickLists(page.context());
    await page.goto("/admin/api-keys");

    for (const k of KINDS) {
      await page.getByTestId(`issue-kind-${k}`).click();
    }
    await expect(page.getByTestId("issue-kinds")).toContainText("2 picked");

    await page.getByTestId("scope-picker-dimensions").check();
    await page.getByTestId("scope-picker-value-environment-prod").click();
    const eq = { op: "eq", dim: "environment", value: "prod" };
    await expect(page.getByTestId("scope-picker-predicate")).toHaveText(
      JSON.stringify(eq, null, 2),
    );

    const issued = page.waitForResponse(
      (r) =>
        r.url().endsWith("/api/admin/credentials") &&
        r.request().method() === "POST",
    );
    await page.getByTestId("issue-credential-submit").click();
    const res = await issued;
    expect(res.status()).toBe(201);
    const { id } = (await res.json()) as IssuedCred;
    await expect(page.getByTestId("fresh-secret-callout")).toBeVisible();

    try {
      const list = await page.request.get("/api/admin/credentials");
      expect(list.ok()).toBe(true);
      const { items } = (await list.json()) as { items: ListedCred[] };
      const stored = items.find((c) => c.id === id);
      expect(stored, `issued key ${id} is listed`).toBeTruthy();
      expect([...stored!.allowed_kinds].sort()).toEqual([...KINDS].sort());
      expect(JSON.parse(stored!.scope_predicate)).toEqual(eq);
    } finally {
      // Leave no live key behind in the shared tenant.
      const rev = await page.request.post(
        `/api/admin/credentials/${encodeURIComponent(id)}/revoke`,
      );
      expect(rev.ok(), `revoke of issued key ${id}`).toBe(true);
    }
  });

  test("raw JSON round-trips a predicate the builder can show and explains one it can't", async ({
    authedPage: page,
  }) => {
    await mockPickLists(page.context());
    const posted: unknown[] = [];
    await page.route("**/api/admin/credentials", async (route) => {
      if (route.request().method() === "POST") {
        posted.push(route.request().postDataJSON());
        await route.fulfill({
          status: 201,
          contentType: "application/json",
          body: JSON.stringify({
            id: "cred-raw",
            tenant_id: "t",
            bearer_token: "atlas_test_raw",
            last4: "_raw",
            issued_at: "2026-11-16T00:00:00Z",
          }),
        });
        return;
      }
      await route.fulfill(json({ items: [] }));
    });
    await page.goto("/admin/api-keys");

    await page.getByTestId("scope-picker-dimensions").check();
    await page.getByTestId("scope-picker-value-environment-prod").click();
    const eq = { op: "eq", dim: "environment", value: "prod" };

    // The builder's predicate opens as JSON.
    const toggle = page.getByTestId("issue-scope-raw-toggle");
    await toggle.click();
    const editor = page.getByTestId("issue-scope-json");
    expect(JSON.parse(await editor.inputValue())).toEqual(eq);
    await expect(page.getByTestId("issue-scope-json-hint")).toHaveText(
      "Blank means Everything (unrestricted).",
    );

    // An edit the builder can show goes back into the pickers...
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

    // ...and comes back out unchanged.
    await toggle.click();
    expect(JSON.parse(await editor.inputValue())).toEqual(both);

    // One it can't show is explained, and the editor stays open.
    const anyOf = {
      op: "or",
      args: [
        { op: "eq", dim: "environment", value: "prod" },
        { op: "eq", dim: "geography", value: "eu" },
      ],
    };
    await editor.fill(JSON.stringify(anyOf));
    await toggle.click();
    await expect(page.getByTestId("issue-scope-json-error")).toContainText(
      `The builder can't show this predicate: it uses "or"`,
    );
    await expect(editor).toBeVisible();
    await expect(page.getByTestId("scope-picker")).toHaveCount(0);

    // Invalid JSON is explained the same way.
    await editor.fill("{not json");
    await toggle.click();
    await expect(page.getByTestId("issue-scope-json-error")).toContainText(
      "Invalid JSON",
    );

    // The unrepresentable predicate can still be issued as written.
    await editor.fill(JSON.stringify(anyOf));
    await page.getByTestId("issue-credential-submit").click();
    await expect.poll(() => posted.length).toBe(1);
    const body = posted[0] as { scope_predicate: string };
    expect(JSON.parse(body.scope_predicate)).toEqual(anyOf);
  });
});
