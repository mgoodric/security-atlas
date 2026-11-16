// #2090 — RowActions and ConfirmInline on the admin tables, and the
// neutral treatment / role tones.
//
// The flag and key specs mock the BFF (/api/admin/features,
// /api/admin/credentials) so the mutations they would send are counted
// rather than applied to the shared database. The treatment pill reads
// the seeded dashboard, like dashboard.spec.ts.
//
// Coverage:
//   (a) disabling a flag goes through the row menu and the inline
//       confirm; Cancel sends nothing, Confirm sends one PATCH.
//   (b) revoking a key goes through the row menu and the inline
//       confirm; Cancel (and Esc) send nothing, Confirm sends one revoke.
//   (c) the dashboard's mitigate treatment pill is not destructive.
//   (The last-admin and last-super-admin cases live in
//   admin-users.spec.ts and super-admins.spec.ts.)

import { expect, test } from "./fixtures";

const FLAG = {
  key: "vendors",
  enabled: true,
  description: "Vendor risk module",
  category: "modules",
  has_override: false,
};

const CRED = {
  id: "cred-aaaaaaaa-1111-4111-8111-111111111111",
  tenant_id: "11111111-1111-4111-8111-111111111111",
  scope_predicate: "",
  allowed_kinds: [],
  last4: "zz99",
  issued_at: "2026-11-01T00:00:00Z",
  last_used_at: null,
  is_admin: true,
  is_approver: false,
  owner_roles: [],
};

test.describe("row actions with inline confirm", () => {
  test("disabling a flag goes through the row menu and the inline confirm", async ({
    authedPage: page,
  }) => {
    const patches: { enabled: boolean; reason: string }[] = [];
    await page.route("**/api/admin/features", (route) =>
      route.fulfill({
        status: 200,
        contentType: "application/json",
        body: JSON.stringify({ items: [FLAG] }),
      }),
    );
    await page.route(`**/api/admin/features/${FLAG.key}`, async (route) => {
      patches.push(route.request().postDataJSON());
      await route.fulfill({
        status: 200,
        contentType: "application/json",
        body: JSON.stringify({ ...FLAG, enabled: false }),
      });
    });

    await page.goto("/admin/features");
    const trigger = page.getByTestId(`flag-actions-${FLAG.key}`);
    await expect(trigger).toHaveAttribute("aria-haspopup", "menu");

    // Cancel: nothing is sent.
    await trigger.click();
    await page.getByTestId(`flag-disable-${FLAG.key}`).click();
    const confirm = page.getByTestId(`flag-actions-${FLAG.key}-confirm`);
    await expect(confirm).toBeVisible();
    await expect(confirm).toContainText(`Disable ${FLAG.key}?`);
    // No modal and no native dialog: the confirm is inline.
    await expect(page.getByRole("dialog")).toHaveCount(0);
    await page.getByTestId(`flag-actions-${FLAG.key}-confirm-cancel`).click();
    await expect(confirm).toBeHidden();
    await expect(trigger).toBeFocused();
    expect(patches).toEqual([]);

    // Confirm: one PATCH, carrying the reason.
    await trigger.click();
    await page.getByTestId(`flag-disable-${FLAG.key}`).click();
    await page
      .getByTestId(`flag-disable-reason-${FLAG.key}`)
      .fill("module unused");
    await page.getByTestId(`flag-actions-${FLAG.key}-confirm-ok`).click();
    await expect.poll(() => patches.length).toBe(1);
    expect(patches[0]).toEqual({ enabled: false, reason: "module unused" });
  });

  // Review of #2259: the reason must not outlive its confirm, or a later
  // disable resends it into the audit trail.
  test("the disable reason resets on Cancel and after a successful disable", async ({
    authedPage: page,
  }) => {
    const patches: { enabled: boolean; reason: string }[] = [];
    // The list stays enabled, so the row can be disabled a second time.
    await page.route("**/api/admin/features", (route) =>
      route.fulfill({
        status: 200,
        contentType: "application/json",
        body: JSON.stringify({ items: [FLAG] }),
      }),
    );
    await page.route(`**/api/admin/features/${FLAG.key}`, async (route) => {
      patches.push(route.request().postDataJSON());
      await route.fulfill({
        status: 200,
        contentType: "application/json",
        body: JSON.stringify({ ...FLAG, enabled: false }),
      });
    });

    await page.goto("/admin/features");
    const trigger = page.getByTestId(`flag-actions-${FLAG.key}`);
    const reason = page.getByTestId(`flag-disable-reason-${FLAG.key}`);
    const openConfirm = async () => {
      await trigger.click();
      await page.getByTestId(`flag-disable-${FLAG.key}`).click();
    };

    // Cancel clears it.
    await openConfirm();
    await reason.fill("cancelled reason");
    await page.getByTestId(`flag-actions-${FLAG.key}-confirm-cancel`).click();
    await openConfirm();
    await expect(reason).toHaveValue("");

    // A successful disable clears it.
    await reason.fill("first reason");
    await page.getByTestId(`flag-actions-${FLAG.key}-confirm-ok`).click();
    await expect.poll(() => patches.length).toBe(1);
    await openConfirm();
    await expect(reason).toHaveValue("");
    await page.getByTestId(`flag-actions-${FLAG.key}-confirm-ok`).click();
    await expect.poll(() => patches.length).toBe(2);
    expect(patches[1]).toEqual({ enabled: false, reason: "" });
  });

  test("a failed flag disable surfaces its error", async ({
    authedPage: page,
  }) => {
    await page.route("**/api/admin/features", (route) =>
      route.fulfill({
        status: 200,
        contentType: "application/json",
        body: JSON.stringify({ items: [FLAG] }),
      }),
    );
    await page.route(`**/api/admin/features/${FLAG.key}`, (route) =>
      route.fulfill({ status: 500, body: "" }),
    );

    await page.goto("/admin/features");
    await page.getByTestId(`flag-actions-${FLAG.key}`).click();
    await page.getByTestId(`flag-disable-${FLAG.key}`).click();
    await page.getByTestId(`flag-actions-${FLAG.key}-confirm-ok`).click();
    await expect(page.getByTestId("flag-patch-error")).toContainText(
      "patch: 500",
    );
  });

  test("a failed settings-token revoke surfaces its error", async ({
    authedPage: page,
  }) => {
    // Both the list and the revoke are mocked (this spec does not load
    // settings.spec.ts's token fixture), so nothing touches the shared
    // database.
    await page.route("**/api/admin/credentials", (route) =>
      route.fulfill({
        status: 200,
        contentType: "application/json",
        body: JSON.stringify({ items: [CRED] }),
      }),
    );
    await page.route("**/api/admin/credentials/*/revoke", (route) =>
      route.fulfill({ status: 500, body: "" }),
    );

    await page.goto("/settings");
    const row = page
      .getByTestId("settings-token-row")
      .filter({ hasText: CRED.last4 });
    await expect(row).toBeVisible();
    await row.getByTestId("settings-token-actions").click();
    await page.getByTestId("settings-token-revoke-button").click();
    await row.getByTestId("settings-token-actions-confirm-ok").click();
    await expect(page.getByTestId("settings-token-revoke-error")).toContainText(
      "revoke credential: 500",
    );
    await expect(row).toBeVisible();
  });

  test("revoking a key goes through the row menu and the inline confirm", async ({
    authedPage: page,
  }) => {
    const revoked: string[] = [];
    let live = [CRED];
    await page.route("**/api/admin/credentials", (route) =>
      route.fulfill({
        status: 200,
        contentType: "application/json",
        body: JSON.stringify({ items: live }),
      }),
    );
    await page.route("**/api/admin/credentials/*/revoke", async (route) => {
      const id = decodeURIComponent(
        route.request().url().split("/").at(-2) ?? "",
      );
      revoked.push(id);
      live = live.filter((c) => c.id !== id);
      await route.fulfill({ status: 204, body: "" });
    });

    await page.goto("/admin/api-keys");
    const trigger = page.getByTestId(`cred-actions-${CRED.id}`);
    await expect(trigger).toBeVisible();

    // Cancel: nothing is sent.
    await trigger.click();
    await page.getByTestId(`cred-revoke-${CRED.id}`).click();
    const confirm = page.getByTestId(`cred-actions-${CRED.id}-confirm`);
    await expect(confirm).toContainText("Revoke the credential ending zz99?");
    await page.getByTestId(`cred-actions-${CRED.id}-confirm-cancel`).click();
    await expect(confirm).toBeHidden();
    expect(revoked).toEqual([]);

    // Esc on the open confirm cancels too.
    await trigger.click();
    await page.getByTestId(`cred-revoke-${CRED.id}`).click();
    await expect(confirm).toBeVisible();
    await page.keyboard.press("Escape");
    await expect(confirm).toBeHidden();
    expect(revoked).toEqual([]);

    // The admin badge on the key is not destructive.
    await expect(
      page
        .getByRole("row")
        .filter({ hasText: "zz99" })
        .getByText("admin", { exact: true }),
    ).not.toHaveClass(/(^|\s)text-destructive(\s|$)/);

    // Confirm: one revoke for this key.
    await trigger.click();
    await page.getByTestId(`cred-revoke-${CRED.id}`).click();
    await page.getByTestId(`cred-actions-${CRED.id}-confirm-ok`).click();
    await expect.poll(() => revoked).toEqual([CRED.id]);
    await expect(trigger).toBeHidden();
  });
});

test.describe("neutral treatment tones", () => {
  test("the dashboard's mitigate treatment pill is not destructive", async ({
    authedPage: page,
  }) => {
    await page.goto("/dashboard");
    const pill = page.getByTestId("top-risk-treatment").first();
    await expect(pill).toHaveText("Mitigate");
    await expect(pill).not.toHaveClass(/(^|\s)text-destructive(\s|$)/);
  });
});
