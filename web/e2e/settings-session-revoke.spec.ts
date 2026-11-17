// #2280 -- Settings > Sessions "Revoke" asks to confirm before it signs
// another device out. Revoke is irreversible from that device, so it
// takes the shared inline confirm (ConfirmInline, #2259): Cancel and
// Escape send nothing, Confirm sends the DELETE.
//
// HERMETIC: /api/me/sessions and its DELETE are page.route-mocked with a
// stateful list, so the spec owns the rows it revokes and never ends a
// session the shared seed relies on.
//
// Run locally:
//   cd web
//   npx playwright test e2e/settings-session-revoke.spec.ts

import { expect, test } from "./fixtures";

const CURRENT_ID = "aaaaaaaa-0000-0000-0000-000000000001";
const OTHER_ID = "aaaaaaaa-0000-0000-0000-000000000002";

test.describe("/settings Sessions revoke -- inline confirm", () => {
  test("Revoke asks first; Cancel and Escape keep the session, Confirm revokes it", async ({
    authedPage: page,
  }) => {
    let sessions = [
      {
        id: CURRENT_ID,
        last4: "cur1",
        created_at: "2026-11-01T10:00:00Z",
        last_used_at: "2026-11-17T09:00:00Z",
        is_current: true,
      },
      {
        id: OTHER_ID,
        last4: "oth2",
        created_at: "2026-11-02T10:00:00Z",
        last_used_at: "2026-11-16T09:00:00Z",
        is_current: false,
      },
    ];
    const deleted: string[] = [];

    await page.route("**/api/me/sessions", async (route) => {
      if (route.request().method() !== "GET") {
        await route.fallback();
        return;
      }
      await route.fulfill({
        status: 200,
        contentType: "application/json",
        body: JSON.stringify({ sessions, count: sessions.length }),
      });
    });
    await page.route("**/api/me/sessions/*", async (route) => {
      if (route.request().method() !== "DELETE") {
        await route.fallback();
        return;
      }
      const id = decodeURIComponent(
        new URL(route.request().url()).pathname.split("/").pop() ?? "",
      );
      deleted.push(id);
      sessions = sessions.filter((s) => s.id !== id);
      await route.fulfill({ status: 204, body: "" });
    });

    await page.goto("/settings");
    const rows = page.getByTestId("settings-session-row");
    await expect(rows).toHaveCount(2);

    const revoke = page.getByTestId("settings-session-revoke");
    const confirm = page.getByTestId("settings-session-revoke-confirm");
    const cancel = page.getByTestId("settings-session-revoke-confirm-cancel");

    // Revoke opens the confirm, naming what happens; focus lands on Cancel.
    await revoke.click();
    await expect(confirm).toContainText("That device is signed out");
    await expect(cancel).toBeFocused();

    // Cancel: nothing sent, the session stays.
    await cancel.click();
    await expect(confirm).toHaveCount(0);
    await expect(rows).toHaveCount(2);
    expect(deleted).toEqual([]);

    // Escape cancels too.
    await revoke.click();
    await expect(confirm).toBeVisible();
    await page.keyboard.press("Escape");
    await expect(confirm).toHaveCount(0);
    expect(deleted).toEqual([]);

    // Confirm: the DELETE goes out and the row leaves the list.
    await revoke.click();
    await page.getByTestId("settings-session-revoke-confirm-ok").click();
    await expect.poll(() => deleted).toEqual([OTHER_ID]);
    await expect(rows).toHaveCount(1);
    await expect(revoke).toHaveCount(0);
  });
});
