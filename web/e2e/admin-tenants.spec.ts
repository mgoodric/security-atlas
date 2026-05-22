// Slice 143 — Playwright E2E for the create-tenant management page.
//
// Three assertions, all driven by page.route() mocking of the BFF
// proxies at /api/admin/tenants:
//
//   (a) Page loads, lists the seeded tenants, and a create POST
//       through the form refreshes the list with the new row + the
//       result modal surfaces the new tenant_id.
//   (b) Submitting an invalid slug surfaces the inline error and
//       blocks the upstream POST.
//   (c) Upstream 409 (duplicate slug) surfaces inline.
//
// We mock the BFF rather than seed real tenants because the slice-143
// handler creates platform-global rows under the BYPASSRLS pool — not
// something the Playwright harness can clean up between specs. The
// Go integration test asserts the real behaviour against Postgres.

import { expect, test } from "./fixtures";

const SEED_BOOTSTRAP = {
  id: "11111111-1111-4111-8111-111111111111",
  name: "Bootstrap Tenant",
  slug: null,
  is_bootstrap_tenant: true,
  created_at: "2026-05-22T08:00:00.000Z",
};

const SEED_CREATED = {
  id: "22222222-2222-4222-8222-222222222222",
  name: "Test Tenant A",
  slug: "test-tenant-a",
  is_bootstrap_tenant: false,
  created_at: "2026-05-22T10:00:00.000Z",
  created_by_user_id: "33333333-3333-4333-8333-333333333333",
};

test.describe("admin tenants management page", () => {
  test("renders the seeded list and supports create via form", async ({
    authedPage,
  }) => {
    let listCallCount = 0;
    await authedPage.route("**/api/admin/tenants", async (route) => {
      const req = route.request();
      if (req.method() === "GET") {
        listCallCount++;
        // First load: one tenant (bootstrap). Second load (after
        // create): two.
        const items =
          listCallCount === 1
            ? [SEED_BOOTSTRAP]
            : [SEED_BOOTSTRAP, SEED_CREATED];
        await route.fulfill({
          status: 200,
          contentType: "application/json",
          body: JSON.stringify({ items }),
        });
        return;
      }
      if (req.method() === "POST") {
        // Assert the body shape the BFF forwards upstream.
        const body = req.postDataJSON() as {
          name?: string;
          slug?: string;
          creator_joins_as?: string;
        };
        expect(body.name).toBe("Test Tenant A");
        expect(body.slug).toBe("test-tenant-a");
        expect(body.creator_joins_as).toBe("none");
        await route.fulfill({
          status: 200,
          contentType: "application/json",
          body: JSON.stringify({ tenant: SEED_CREATED }),
        });
        return;
      }
      await route.continue();
    });

    await authedPage.goto("/admin/tenants");

    // Bootstrap row visible.
    const bootstrapRow = authedPage.getByTestId(
      `tenant-row-${SEED_BOOTSTRAP.id}`,
    );
    await expect(bootstrapRow).toBeVisible();
    await expect(bootstrapRow).toContainText("Bootstrap Tenant");
    await expect(bootstrapRow).toContainText("bootstrap_first_install");

    // Fill the create form.
    await authedPage.getByTestId("tenant-name-input").fill(SEED_CREATED.name);
    await authedPage.getByTestId("tenant-slug-input").fill(SEED_CREATED.slug);
    await authedPage.getByTestId("create-tenant-submit").click();

    // Result modal surfaces.
    const modal = authedPage.getByTestId("create-tenant-result");
    await expect(modal).toBeVisible();
    await expect(authedPage.getByTestId("result-tenant-id")).toContainText(
      SEED_CREATED.id,
    );

    // After refetch, the new row appears in the list.
    const createdRow = authedPage.getByTestId(`tenant-row-${SEED_CREATED.id}`);
    await expect(createdRow).toBeVisible();
    await expect(createdRow).toContainText("Test Tenant A");
    await expect(createdRow).toContainText("test-tenant-a");
    await expect(createdRow).toContainText("manual_create");
  });

  test("invalid slug blocks submission with client-side error", async ({
    authedPage,
  }) => {
    await authedPage.route("**/api/admin/tenants", async (route) => {
      if (route.request().method() === "GET") {
        await route.fulfill({
          status: 200,
          contentType: "application/json",
          body: JSON.stringify({ items: [SEED_BOOTSTRAP] }),
        });
        return;
      }
      // Should never be called — client-side validation blocks the POST.
      await route.continue();
    });

    await authedPage.goto("/admin/tenants");

    // Fill the form with an invalid slug.
    await authedPage.getByTestId("tenant-name-input").fill("Bad Slug Test");
    await authedPage.getByTestId("tenant-slug-input").fill("UPPER-CASE");
    await authedPage.getByTestId("create-tenant-submit").click();

    // Client-side error surfaces inline.
    await expect(authedPage.getByTestId("create-tenant-error")).toBeVisible();
    await expect(authedPage.getByTestId("create-tenant-error")).toContainText(
      /slug must match/,
    );

    // The result modal does NOT open.
    await expect(authedPage.getByTestId("create-tenant-result")).toBeHidden();
  });

  test("upstream 409 (duplicate slug) surfaces inline as an error", async ({
    authedPage,
  }) => {
    await authedPage.route("**/api/admin/tenants", async (route) => {
      if (route.request().method() === "GET") {
        await route.fulfill({
          status: 200,
          contentType: "application/json",
          body: JSON.stringify({ items: [SEED_BOOTSTRAP, SEED_CREATED] }),
        });
        return;
      }
      if (route.request().method() === "POST") {
        await route.fulfill({
          status: 409,
          contentType: "application/json",
          body: JSON.stringify({ error: "slug already in use" }),
        });
        return;
      }
      await route.continue();
    });

    await authedPage.goto("/admin/tenants");

    await authedPage.getByTestId("tenant-name-input").fill("Dup Tenant");
    await authedPage.getByTestId("tenant-slug-input").fill("test-tenant-a");
    await authedPage.getByTestId("create-tenant-submit").click();

    // 409 surfaces inline.
    await expect(authedPage.getByTestId("create-tenant-error")).toBeVisible();
    await expect(authedPage.getByTestId("create-tenant-error")).toContainText(
      "slug already in use",
    );
  });
});
