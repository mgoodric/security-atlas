// Slice 665 — Playwright e2e for the board-pack "Generate draft" empty-date
// validation feedback + happy path.
//
// Hermetic mock pattern (per the feedback_e2e_shared_db_hermetic_mock
// lesson, slice 594): this spec route-mocks the BFF for the board-packs
// list (GET) and the generate (POST) so the assertions do not depend on the
// slice-205 demo seed — and so the `board.reporting` feature gate does not
// short-circuit the page into the FeatureDisabledState (a 200 list response
// is the flag-on signal the page keys off). The `authedPage` fixture
// supplies the session cookie that gets past the (authed) layout proxy.
//
// Two scenarios mirror the slice ACs:
//   1. AC-1 + AC-2: clicking "Generate draft" with an empty quarter-end date
//      surfaces an inline validation message (role=alert) and never POSTs —
//      no silent no-op.
//   2. AC-3: a valid quarter-end date still generates the draft (the POST
//      fires and the new pack appears in the list) — no regression.

import { expect, test } from "./fixtures";
import type { Page } from "@playwright/test";

const PACK_ID = "00000000-0000-0000-0000-0000006650aa";

function pack(periodEnd: string, status = "draft") {
  return {
    id: PACK_ID,
    period_end: periodEnd,
    status,
    published_by: "",
    narrative_md: "Quarterly board pack — e2e mock.",
    created_at: `${periodEnd}T00:00:00Z`,
    updated_at: `${periodEnd}T00:00:00Z`,
  };
}

// mockBoardPacks installs the list (GET) handler returning the supplied
// packs, plus a POST handler that records whether a generate was attempted
// and echoes back a created draft.
async function mockBoardPacks(
  page: Page,
  state: { generateCalled: boolean },
): Promise<void> {
  let created: ReturnType<typeof pack> | null = null;
  await page.route("**/api/board-packs", async (route) => {
    if (route.request().method() === "POST") {
      state.generateCalled = true;
      const body = JSON.parse(route.request().postData() ?? "{}");
      created = pack(String(body.period_end ?? "2026-06-30"));
      await route.fulfill({
        status: 201,
        contentType: "application/json",
        body: JSON.stringify(created),
      });
      return;
    }
    await route.fulfill({
      status: 200,
      contentType: "application/json",
      body: JSON.stringify({ packs: created ? [created] : [] }),
    });
  });
}

test.describe("board-pack generate-draft validation (slice 665)", () => {
  test("AC-1 + AC-2: empty quarter-end date shows inline feedback and does not POST", async ({
    authedPage: page,
  }) => {
    const state = { generateCalled: false };
    await mockBoardPacks(page, state);

    await page.goto("/board-packs");

    // The form is rendered (flag-on path) and the date input starts empty.
    const submit = page.getByTestId("board-pack-generate-submit");
    await expect(submit).toBeVisible();

    // No error before the operator attempts a submit (don't pre-shame).
    await expect(page.getByTestId("board-pack-period-end-error")).toHaveCount(
      0,
    );

    // Click "Generate draft" with the date empty.
    await submit.click();

    // Clear inline validation appears — no silent no-op (AC-1). The message
    // is an accessible alert wired to the input via aria-describedby (AC-2).
    const error = page.getByTestId("board-pack-period-end-error");
    await expect(error).toBeVisible();
    await expect(error).toHaveText(/enter a quarter-end date/i);
    await expect(page.getByTestId("board-pack-period-end")).toHaveAttribute(
      "aria-invalid",
      "true",
    );

    // The server was never contacted with an empty date.
    expect(state.generateCalled).toBe(false);
  });

  test("AC-3: a valid quarter-end date generates the draft (no regression)", async ({
    authedPage: page,
  }) => {
    const state = { generateCalled: false };
    await mockBoardPacks(page, state);

    await page.goto("/board-packs");

    const dateInput = page.getByTestId("board-pack-period-end");
    await dateInput.fill("2026-06-30");

    const postResp = page.waitForResponse(
      (r) =>
        r.url().includes("/api/board-packs") &&
        r.request().method() === "POST" &&
        r.status() === 201,
      { timeout: 30_000 },
    );
    await page.getByTestId("board-pack-generate-submit").click();
    await postResp;

    // The generate fired (AC-3) and no validation error was shown.
    expect(state.generateCalled).toBe(true);
    await expect(page.getByTestId("board-pack-period-end-error")).toHaveCount(
      0,
    );

    // The newly created draft surfaces in the list (the success handler
    // invalidates the list query, which refetches and now returns it).
    await expect(page.getByText("2026-06-30")).toBeVisible();
  });
});
