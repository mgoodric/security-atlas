// OE-664 — /personnel-security end-to-end flow against the real OE-663
// API (no route mocks): create a manual offboarding checklist with a
// past due date via the UI, complete an item with evidence, and assert
// overdue-offboarding prominence (top sort + rose badge) on the list.
//
// Seed posture: this spec is fully self-establishing — the manual
// create form seeds the checklist (the OE-630 store attaches the
// kind-specific template items on create), so no SQL fixture under
// fixtures/e2e/ is required. Person identifiers carry a per-run suffix
// because fixtures/state are additive within a CI run (e2e/README.md
// §seed-harness rule 3).

import type { Page } from "@playwright/test";

import { expect, test as authedTest } from "./fixtures";

// Per-run uniqueness so repeated local runs and CI retries never
// collide on person identifiers.
const RUN = `${Date.now()}`;
const OFFBOARD_PERSON = `e2e-offboard-${RUN}`;
const ONBOARD_PERSON = `e2e-onboard-${RUN}`;

// Create a checklist through the real UI form; resolves once the app
// lands on the detail page (create POST round-trip complete).
async function createChecklist(
  page: Page,
  opts: {
    kind: "onboarding" | "offboarding";
    person: string;
    dueDate?: string;
  },
): Promise<void> {
  await page.goto("/personnel-security/new");
  await expect(page.getByTestId("personnel-create-form")).toBeVisible({
    timeout: 30_000,
  });
  await page.getByTestId("personnel-create-kind").selectOption(opts.kind);
  await page.getByTestId("personnel-create-person-id").fill(opts.person);
  await page.getByTestId("personnel-create-name").fill(opts.person);
  if (opts.dueDate) {
    await page.getByTestId("personnel-create-due").fill(opts.dueDate);
  }
  await page.getByTestId("personnel-create-submit").click();
  await expect(page.getByTestId("personnel-detail")).toBeVisible({
    timeout: 30_000,
  });
}

authedTest.describe("personnel-security", () => {
  authedTest(
    "create → complete an item with evidence → overdue offboarding prominent on the list",
    async ({ authedPage: page }) => {
      // 1. Manual offboarding checklist, due yesterday → overdue.
      const yesterday = new Date(Date.now() - 24 * 60 * 60 * 1000)
        .toISOString()
        .slice(0, 10);
      await createChecklist(page, {
        kind: "offboarding",
        person: OFFBOARD_PERSON,
        dueDate: yesterday,
      });

      // Detail shows the person, the overdue badge, and template items.
      await expect(page.getByTestId("personnel-detail-person")).toHaveText(
        OFFBOARD_PERSON,
      );
      await expect(page.getByTestId("personnel-detail-status")).toHaveText(
        "Overdue offboarding",
      );
      const items = page.getByTestId("personnel-item");
      await expect(items.first()).toBeVisible({ timeout: 30_000 });

      // 2. Complete the first item with evidence URI + notes.
      const first = items.first();
      await first.getByTestId("personnel-item-complete-toggle").click();
      await first
        .getByTestId("personnel-item-evidence-input")
        .fill(`https://tickets.example.com/T-${RUN}`);
      await first
        .getByTestId("personnel-item-notes-input")
        .fill("badge + laptop returned");
      await first.getByTestId("personnel-item-complete-submit").click();

      // The mutation invalidates the detail query; the row re-renders
      // with the completed badge + the recorded evidence URI.
      await expect(first.getByTestId("personnel-item-completed")).toBeVisible({
        timeout: 30_000,
      });
      await expect(first.getByTestId("personnel-item-evidence-uri")).toHaveText(
        `https://tickets.example.com/T-${RUN}`,
      );

      // 3. A second, non-overdue onboarding checklist to sort against.
      const nextMonth = new Date(Date.now() + 30 * 24 * 60 * 60 * 1000)
        .toISOString()
        .slice(0, 10);
      await createChecklist(page, {
        kind: "onboarding",
        person: ONBOARD_PERSON,
        dueDate: nextMonth,
      });

      // 4. List page: the overdue offboarding row carries the rose
      // badge and sorts above the open onboarding row.
      const listResp = page.waitForResponse(
        (r) =>
          r.url().includes("/api/personnel-security/checklists") &&
          r.status() === 200,
        { timeout: 30_000 },
      );
      await page.goto("/personnel-security");
      await listResp;

      const offboardLink = page
        .getByTestId("personnel-row-person")
        .filter({ hasText: OFFBOARD_PERSON });
      await expect(offboardLink).toBeVisible({ timeout: 30_000 });
      await expect(
        page
          .getByTestId("personnel-row-person")
          .filter({ hasText: ONBOARD_PERSON }),
      ).toBeVisible();

      const people = await page
        .getByTestId("personnel-row-person")
        .allInnerTexts();
      const offboardIdx = people.indexOf(OFFBOARD_PERSON);
      const onboardIdx = people.indexOf(ONBOARD_PERSON);
      expect(offboardIdx).toBeGreaterThanOrEqual(0);
      expect(onboardIdx).toBeGreaterThanOrEqual(0);
      expect(offboardIdx).toBeLessThan(onboardIdx);

      // The overdue row's badge is the explicit prominent label.
      const offboardRow = page
        .locator("tr", { hasText: OFFBOARD_PERSON })
        .first();
      await expect(offboardRow.getByTestId("personnel-row-status")).toHaveText(
        "Overdue offboarding",
      );
    },
  );
});
