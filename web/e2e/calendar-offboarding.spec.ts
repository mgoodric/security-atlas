// #2215 — /calendar with an open offboarding checklist due.
//
// The calendar query emits open offboarding checklists as `offboarding`
// events. Before #2215 the frontend's linkFor had no case for the type
// and threw from assertNever during render, replacing the whole page
// with "This page couldn't load". This spec runs against the real API
// (no route mocks): it creates an offboarding checklist due in a few
// days through the personnel-security form, then asserts /calendar
// renders, lists the event, and links it to the checklist.
//
// Seed posture: self-establishing, like personnel-security.spec.ts —
// the create form seeds the checklist, and the person identifier carries
// a per-run suffix because fixtures/state are additive within a CI run
// (e2e/README.md §seed-harness rule 3).

import { expect, test } from "./fixtures";

const RUN = `${Date.now()}`;
const PERSON = `e2e-calendar-offboard-${RUN}`;

test.describe("compliance calendar — offboarding events (#2215)", () => {
  test("an offboarding checklist due soon renders on /calendar and links to its checklist", async ({
    authedPage: page,
  }) => {
    const pageErrors: string[] = [];
    page.on("pageerror", (err) => pageErrors.push(err.message));

    // 1. Seed: an offboarding checklist due in three days, inside the
    //    agenda's default window.
    const dueDate = new Date(Date.now() + 3 * 24 * 60 * 60 * 1000)
      .toISOString()
      .slice(0, 10);
    await page.goto("/personnel-security/new");
    await expect(page.getByTestId("personnel-create-form")).toBeVisible({
      timeout: 30_000,
    });
    await page.getByTestId("personnel-create-kind").selectOption("offboarding");
    await page.getByTestId("personnel-create-person-id").fill(PERSON);
    await page.getByTestId("personnel-create-name").fill(PERSON);
    await page.getByTestId("personnel-create-due").fill(dueDate);
    await page.getByTestId("personnel-create-submit").click();
    await expect(page.getByTestId("personnel-detail")).toBeVisible({
      timeout: 30_000,
    });
    const checklistID = new URL(page.url()).pathname.split("/").pop();
    expect(checklistID).toBeTruthy();

    // 2. The calendar loads with the offboarding event in it.
    const calendarResp = page.waitForResponse(
      (r) => r.url().includes("/api/calendar") && r.status() === 200,
      { timeout: 30_000 },
    );
    await page.goto("/calendar");
    await calendarResp;

    await expect(
      page.getByRole("heading", { name: /Compliance calendar/ }),
    ).toBeVisible({ timeout: 30_000 });
    await expect(page.getByText("This page couldn't load")).toHaveCount(0);

    const row = page.locator("li", { hasText: PERSON }).first();
    await expect(row).toBeVisible({ timeout: 30_000 });
    await expect(row).toContainText("Offboarding");
    await expect(
      row.locator(`a[href="/personnel-security/${checklistID}"]`),
    ).toHaveCount(1);

    // 3. The legend offers the type for filtering.
    await expect(page.getByLabel("Offboarding due")).toBeVisible();

    expect(pageErrors).toEqual([]);
  });
});
