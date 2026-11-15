// #1818 — Playwright E2E for the Taxonomy settings page (/risks/taxonomy).
//
// Coverage:
//   - Categories: add, rename, archive and set default; delete is disabled,
//     with the counts, for a category in use, and works for an unused one.
//   - Themes: create, rename and delete an organisation theme; built-in
//     themes are labelled and have no edit controls.
//   - A non-admin sees both lists and no edit control, and the page sends
//     no write.
//
// Mocked per the `route.fulfill` convention with a small in-memory model
// of the category and theme APIs, so each write is visible on the page's
// refetch. The platform's own refusals (403 for a non-admin, 409 for a
// delete in use) are covered by the Go integration tests
// (category_admin_integration_test.go, theme_admin_integration_test.go);
// this spec checks what the page offers. No sleeps: every assertion
// auto-waits.
//
// Hard rule (P0-A9): neutral test strings only.

import type { Page, Route } from "@playwright/test";

import { expect, test } from "./fixtures";

type Category = {
  id: string;
  slug: string;
  name: string;
  description: string;
  archived: boolean;
  is_default: boolean;
  risk_count: number;
  rule_count: number;
};

type Theme = {
  name: string;
  description: string;
  source: "default" | "tenant";
  risk_count: number;
  rule_count: number;
};

function category(
  slug: string,
  name: string,
  opts: Partial<
    Pick<Category, "is_default" | "risk_count" | "rule_count">
  > = {},
): Category {
  return {
    id: `00000000-0000-4000-8000-${slug.padEnd(12, "0").slice(0, 12)}`,
    slug,
    name,
    description: "",
    archived: false,
    is_default: opts.is_default ?? false,
    risk_count: opts.risk_count ?? 0,
    rule_count: opts.rule_count ?? 0,
  };
}

function theme(
  name: string,
  source: Theme["source"],
  risk_count = 0,
  rule_count = 0,
): Theme {
  return { name, description: "", source, risk_count, rule_count };
}

type Model = {
  categories: Category[];
  themes: Theme[];
  writes: string[];
};

function newModel(): Model {
  return {
    categories: [
      category("operational", "Operational", {
        is_default: true,
        risk_count: 4,
      }),
      category("privacy", "Privacy", { risk_count: 2, rule_count: 1 }),
      category("financial", "Financial"),
    ],
    themes: [
      theme("ownership", "default", 3),
      theme("vendor-risk", "default"),
      theme("cloud-drift", "tenant", 2, 1),
    ],
    writes: [],
  };
}

async function json(route: Route, status: number, body?: unknown) {
  await route.fulfill({
    status,
    contentType: "application/json",
    body: body === undefined ? "" : JSON.stringify(body),
  });
}

async function mockMe(page: Page, roles: string[], isAdmin: boolean) {
  await page.route(/\/api\/me$/, (route) =>
    json(route, 200, {
      user_id: "00000000-0000-4000-8000-000000000001",
      tenant_id: "00000000-0000-4000-8000-000000000002",
      display_name: "Test user",
      email: "user@example.test",
      idp_subject: "test-subject",
      tenant_role: roles[0] ?? "viewer",
      time_zone: null,
      is_admin: isAdmin,
      // The page reads owner_roles, the credential's roles, as the
      // platform's taxonomy guard does.
      owner_roles: roles,
      roles,
    }),
  );
}

// mockTaxonomy serves the category and theme BFF routes from `m`.
async function mockTaxonomy(page: Page, m: Model) {
  await page.route(
    /\/api\/(risks\/categories|themes)(\/[^?]*)?(\?.*)?$/,
    (route, req) => {
      const url = new URL(req.url());
      const method = req.method();
      const parts = url.pathname.split("/").filter(Boolean); // api, ...
      if (method !== "GET") m.writes.push(`${method} ${url.pathname}`);

      if (parts[1] === "risks") {
        const slug = parts[3] ? decodeURIComponent(parts[3]) : undefined;
        const action = parts[4];
        if (!slug) {
          if (method === "GET") {
            return json(route, 200, {
              categories: m.categories,
              count: m.categories.length,
            });
          }
          const body = req.postDataJSON() as { slug: string; name: string };
          const created = category(body.slug, body.name);
          m.categories.push(created);
          return json(route, 201, { category: created });
        }
        const c = m.categories.find((x) => x.slug === slug);
        if (!c) return json(route, 404, { error: "risk category not found" });
        if (method === "PATCH") {
          c.name = (req.postDataJSON() as { name: string }).name;
          return json(route, 200, { category: c });
        }
        if (method === "DELETE") {
          if (c.risk_count || c.rule_count) {
            return json(route, 409, {
              error: "in use",
              risk_count: c.risk_count,
              rule_count: c.rule_count,
            });
          }
          m.categories = m.categories.filter((x) => x !== c);
          return json(route, 204);
        }
        if (action === "archive") c.archived = true;
        if (action === "unarchive") c.archived = false;
        if (action === "set-default") {
          for (const x of m.categories) x.is_default = x === c;
        }
        return json(route, 200, { category: c });
      }

      const name = parts[2] ? decodeURIComponent(parts[2]) : undefined;
      if (!name) {
        if (method === "GET")
          return json(route, 200, { themes: m.themes, count: m.themes.length });
        const body = req.postDataJSON() as { name: string };
        const created = theme(body.name, "tenant");
        m.themes.push(created);
        return json(route, 201, { theme: created });
      }
      const t = m.themes.find((x) => x.name === name && x.source === "tenant");
      if (!t) return json(route, 404, { error: "theme not found" });
      if (method === "PATCH") {
        t.name = (req.postDataJSON() as { name: string }).name;
        return json(route, 200, {
          theme: t,
          risks_updated: t.risk_count,
          rules_updated: t.rule_count,
        });
      }
      if (t.risk_count || t.rule_count) {
        return json(route, 409, {
          error: "in use",
          risk_count: t.risk_count,
          rule_count: t.rule_count,
        });
      }
      m.themes = m.themes.filter((x) => x !== t);
      return json(route, 204);
    },
  );
}

test.describe("/risks/taxonomy (#1818)", () => {
  test("categories: add, rename, archive, set default, delete", async ({
    authedPage: page,
  }) => {
    const m = newModel();
    await mockMe(page, ["admin"], true);
    await mockTaxonomy(page, m);

    await page.goto("/risks/taxonomy");
    const section = page.getByTestId("taxonomy-categories");
    await expect(section.getByTestId("category-row-operational")).toBeVisible({
      timeout: 30_000,
    });
    await expect(
      page.getByTestId("category-default-operational"),
    ).toBeVisible();

    // Add.
    await page.getByTestId("category-add-slug").fill("cloud-security");
    await page.getByTestId("category-add-name").fill("Cloud Security");
    await page.getByTestId("category-add-submit").click();
    await expect(page.getByTestId("category-name-cloud-security")).toHaveText(
      "Cloud Security",
    );

    // Rename: the name changes, the slug does not.
    await page.getByTestId("category-rename-cloud-security").click();
    await page
      .getByTestId("category-rename-input-cloud-security")
      .fill("Cloud Platform");
    await page.getByTestId("category-rename-save-cloud-security").click();
    await expect(page.getByTestId("category-name-cloud-security")).toHaveText(
      "Cloud Platform",
    );
    expect(m.writes).toContain("PATCH /api/risks/categories/cloud-security");

    // Archive, then un-archive.
    await page.getByTestId("category-archive-financial").click();
    await expect(page.getByTestId("category-archived-financial")).toBeVisible();
    await expect(
      page.getByTestId("category-set-default-financial"),
    ).toHaveCount(0);
    await page.getByTestId("category-unarchive-financial").click();
    await expect(page.getByTestId("category-archived-financial")).toHaveCount(
      0,
    );

    // Set default moves the badge; the default offers no archive.
    await page.getByTestId("category-set-default-cloud-security").click();
    await expect(
      page.getByTestId("category-default-cloud-security"),
    ).toBeVisible();
    await expect(page.getByTestId("category-default-operational")).toHaveCount(
      0,
    );
    await expect(
      page.getByTestId("category-archive-cloud-security"),
    ).toHaveCount(0);

    // Delete is disabled, with the counts, while in use.
    await expect(page.getByTestId("category-delete-privacy")).toBeDisabled();
    await expect(page.getByTestId("category-delete-reason-privacy")).toHaveText(
      "Can't delete: In use by 2 risks, 1 rule",
    );
    // An unused category deletes after the inline confirm.
    await page.getByTestId("category-delete-financial").click();
    await page.getByTestId("category-delete-confirm-financial-ok").click();
    await expect(page.getByTestId("category-row-financial")).toHaveCount(0);
    expect(m.writes).toContain("DELETE /api/risks/categories/financial");
    expect(m.writes).not.toContain("DELETE /api/risks/categories/privacy");
  });

  test("themes: create, rename, delete; built-ins are read-only", async ({
    authedPage: page,
  }) => {
    const m = newModel();
    await mockMe(page, ["grc_engineer"], false);
    await mockTaxonomy(page, m);

    await page.goto("/risks/taxonomy");
    const section = page.getByTestId("taxonomy-themes");
    await expect(section.getByTestId("theme-row-ownership")).toBeVisible({
      timeout: 30_000,
    });

    // Built-ins: labelled, usage shown, no edit controls.
    for (const name of ["ownership", "vendor-risk"]) {
      const row = section.getByTestId(`theme-row-${name}`);
      await expect(row.getByTestId(`theme-builtin-${name}`)).toHaveText(
        "Built-in",
      );
      await expect(row.getByRole("button")).toHaveCount(0);
    }
    await expect(page.getByTestId("theme-usage-ownership")).toHaveText(
      "3 risks, 0 rules",
    );

    // An organisation theme in use cannot be deleted.
    await expect(page.getByTestId("theme-delete-cloud-drift")).toBeDisabled();
    await expect(
      page.getByTestId("theme-delete-reason-cloud-drift"),
    ).toHaveText("Can't delete: In use by 2 risks, 1 rule");

    // Create.
    await page.getByTestId("theme-add-name").fill("shadow-it");
    await page.getByTestId("theme-add-submit").click();
    await expect(section.getByTestId("theme-row-shadow-it")).toBeVisible();
    await expect(section.getByTestId("theme-builtin-shadow-it")).toHaveCount(0);

    // Rename.
    await page.getByTestId("theme-rename-shadow-it").click();
    await page
      .getByTestId("theme-rename-input-shadow-it")
      .fill("unsanctioned-saas");
    await page.getByTestId("theme-rename-save-shadow-it").click();
    await expect(
      section.getByTestId("theme-row-unsanctioned-saas"),
    ).toBeVisible();
    await expect(section.getByTestId("theme-row-shadow-it")).toHaveCount(0);

    // Delete.
    await page.getByTestId("theme-delete-unsanctioned-saas").click();
    await page.getByTestId("theme-delete-confirm-unsanctioned-saas-ok").click();
    await expect(
      section.getByTestId("theme-row-unsanctioned-saas"),
    ).toHaveCount(0);
    expect(m.writes).toEqual([
      "POST /api/themes",
      "PATCH /api/themes/shadow-it",
      "DELETE /api/themes/unsanctioned-saas",
    ]);
  });

  test("a non-admin can view but not edit", async ({ authedPage: page }) => {
    const m = newModel();
    await mockMe(page, ["viewer"], false);
    await mockTaxonomy(page, m);

    await page.goto("/risks/taxonomy");
    const cats = page.getByTestId("taxonomy-categories");
    const themes = page.getByTestId("taxonomy-themes");
    await expect(cats.getByTestId("category-row-privacy")).toBeVisible({
      timeout: 30_000,
    });
    await expect(themes.getByTestId("theme-row-cloud-drift")).toBeVisible();

    // Everything is listed, with usage and state.
    await expect(page.getByTestId("category-usage-privacy")).toHaveText(
      "2 risks, 1 rule",
    );
    await expect(
      page.getByTestId("category-default-operational"),
    ).toBeVisible();
    await expect(page.getByTestId("theme-builtin-ownership")).toBeVisible();

    // No add form, and no button anywhere in either table.
    await expect(page.getByTestId("category-add-form")).toHaveCount(0);
    await expect(page.getByTestId("theme-add-form")).toHaveCount(0);
    await expect(cats.getByRole("button")).toHaveCount(0);
    await expect(themes.getByRole("button")).toHaveCount(0);
    expect(m.writes).toEqual([]);
  });
});
