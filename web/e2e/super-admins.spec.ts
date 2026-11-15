// Slice 142 — Playwright E2E for the super_admin management page.
//
// Two assertions, both driven by page.route() mocking of the BFF
// proxies at /api/admin/super-admins and /api/admin/super-admins/*:
//
//   (a) Page loads, lists the seeded super_admins, and a grant POST
//       through the form refreshes the list with the new row.
//   (b) Per-row menu → Demote → confirmation dialog → a 409
//       last-super_admin error surfaces inline (load-bearing UX for
//       P0-SA-1). The list shows two rows (a stale list: the other was
//       demoted elsewhere), so the UI offers Demote and the backend's
//       refusal is what the operator sees.
//   (c) With one super admin listed, Demote is withheld in the row
//       menu with its reason (#2090).
//
// We mock the BFF rather than seed real super_admins because the
// slice-142 spec doc is platform-tier work; e2e here is the
// frontend-binding contract. The handler-level Go integration test
// asserts the real behaviour against Postgres.

import { expect, test } from "./fixtures";

const SEED_BOOTSTRAP = {
  user_id: "44444444-4444-4444-4444-444444440001",
  granted_at: "2026-05-21T10:00:00.000Z",
  granted_via: "bootstrap_first_install" as const,
  display_name: "Demo Operator",
  email: "demo-operator@example.invalid",
};

const SEED_MANUAL = {
  user_id: "55555555-5555-5555-5555-555555550001",
  granted_at: "2026-05-21T11:00:00.000Z",
  granted_via: "manual_grant" as const,
  display_name: "Backup Operator",
  email: "backup-operator@example.invalid",
};

test.describe("super_admin management page", () => {
  test("renders the seeded list and supports grant via form", async ({
    authedPage,
  }) => {
    let listCallCount = 0;
    await authedPage.route("**/api/admin/super-admins", async (route) => {
      const req = route.request();
      if (req.method() === "GET") {
        listCallCount++;
        // First load: one super_admin. Second load (after grant): two.
        const items =
          listCallCount === 1
            ? [SEED_BOOTSTRAP]
            : [SEED_BOOTSTRAP, SEED_MANUAL];
        await route.fulfill({
          status: 200,
          contentType: "application/json",
          body: JSON.stringify({ items }),
        });
        return;
      }
      if (req.method() === "POST") {
        // Assert the body shape the BFF forwards upstream.
        const body = req.postDataJSON() as { user_id?: string };
        expect(body.user_id).toBe(SEED_MANUAL.user_id);
        await route.fulfill({
          status: 200,
          contentType: "application/json",
          body: JSON.stringify(SEED_MANUAL),
        });
        return;
      }
      await route.continue();
    });

    await authedPage.goto("/admin/super-admins");

    // List loads with the bootstrap row.
    const bootstrapRow = authedPage.getByTestId(
      `super-admin-row-${SEED_BOOTSTRAP.user_id}`,
    );
    await expect(bootstrapRow).toBeVisible();
    await expect(bootstrapRow).toContainText("Demo Operator");
    await expect(bootstrapRow).toContainText("bootstrap_first_install");

    // Submit the grant form.
    await authedPage
      .getByTestId("grant-user-id-input")
      .fill(SEED_MANUAL.user_id);
    await authedPage.getByTestId("grant-super-admin-submit").click();

    // After refetch, the second row appears.
    const manualRow = authedPage.getByTestId(
      `super-admin-row-${SEED_MANUAL.user_id}`,
    );
    await expect(manualRow).toBeVisible();
    await expect(manualRow).toContainText("Backup Operator");
    await expect(manualRow).toContainText("manual_grant");
  });

  test("demote dialog surfaces 409 last-super_admin error inline", async ({
    authedPage,
  }) => {
    // LOAD-BEARING UX scenario: a demote the backend refuses as the
    // last super_admin. The list is stale (two rows), so the UI offers
    // Demote; the backend returns 409 with the safety-rail message and
    // the UI must surface that inline rather than a generic red toast.
    await authedPage.route("**/api/admin/super-admins", async (route) => {
      if (route.request().method() === "GET") {
        await route.fulfill({
          status: 200,
          contentType: "application/json",
          body: JSON.stringify({ items: [SEED_BOOTSTRAP, SEED_MANUAL] }),
        });
        return;
      }
      await route.continue();
    });

    await authedPage.route(
      `**/api/admin/super-admins/${SEED_BOOTSTRAP.user_id}`,
      async (route) => {
        if (route.request().method() === "DELETE") {
          await route.fulfill({
            status: 409,
            contentType: "application/json",
            body: JSON.stringify({
              error: "Cannot demote the last super_admin",
            }),
          });
          return;
        }
        await route.continue();
      },
    );

    await authedPage.goto("/admin/super-admins");

    const row = authedPage.getByTestId(
      `super-admin-row-${SEED_BOOTSTRAP.user_id}`,
    );
    await expect(row).toBeVisible();

    // Open the demote dialog from the row menu.
    await authedPage
      .getByTestId(`super-admin-actions-${SEED_BOOTSTRAP.user_id}`)
      .click();
    await authedPage
      .getByTestId(`demote-button-${SEED_BOOTSTRAP.user_id}`)
      .click();
    const dialog = authedPage.getByTestId("demote-dialog");
    await expect(dialog).toBeVisible();
    await expect(dialog).toContainText("Demote super admin");

    // Confirm the demote → 409 surfaces inline.
    await authedPage.getByTestId("demote-confirm").click();
    await expect(authedPage.getByTestId("demote-error")).toBeVisible();
    await expect(authedPage.getByTestId("demote-error")).toContainText(
      "Cannot demote the last super_admin",
    );

    // The dialog stays open so the operator can cancel and reconsider.
    await expect(dialog).toBeVisible();
  });

  test("the last super admin is not offered Demote, and the reason is shown", async ({
    authedPage,
  }) => {
    let deletes = 0;
    await authedPage.route("**/api/admin/super-admins", async (route) => {
      await route.fulfill({
        status: 200,
        contentType: "application/json",
        body: JSON.stringify({ items: [SEED_BOOTSTRAP] }),
      });
    });
    await authedPage.route("**/api/admin/super-admins/*", async (route) => {
      if (route.request().method() === "DELETE") deletes++;
      await route.fulfill({ status: 204, body: "" });
    });

    await authedPage.goto("/admin/super-admins");
    await authedPage
      .getByTestId(`super-admin-actions-${SEED_BOOTSTRAP.user_id}`)
      .click();
    const item = authedPage.getByTestId(
      `demote-button-${SEED_BOOTSTRAP.user_id}`,
    );
    await expect(item).toHaveAttribute("aria-disabled", "true");
    await expect(
      authedPage.getByTestId(`demote-button-${SEED_BOOTSTRAP.user_id}-reason`),
    ).toHaveText("This is the last super admin. Grant a successor first.");
    await item.click({ force: true });
    await expect(authedPage.getByTestId("demote-dialog")).toHaveCount(0);
    expect(deletes).toBe(0);
  });
});
