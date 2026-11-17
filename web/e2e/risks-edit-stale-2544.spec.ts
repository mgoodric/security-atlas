// #2544 — the risk edit form keeps the version it opened on.
//
// While the form is open, another editor saves the risk. A background
// refetch of the risk (here: window focus after the query's staleTime)
// must not remount the form and drop the user's unsaved edit. Saving then
// sends If-Match with the opening version, the platform answers 412, the
// page shows `risk-edit-stale`, and Reload opens the form on the new
// version. Changing the methodology shows the residual-reset note.
//
// HERMETIC: route-mocks the risk BFF routes (slice 594 convention). The
// "other editor" is a direct change to the mock's stored risk; the PATCH
// mock answers 412 when If-Match is not the stored version, as the
// platform does. The platform side of If-Match is covered by
// internal/api/risks/concurrency_integration_test.go against real Postgres.

import type { Page, Route } from "@playwright/test";

import { expect, test } from "./fixtures";

const RISK_ID = "00000000-0000-4000-8000-000000002544";

// Neutral test strings only (P0-A4 / GitGuardian).
function storedRisk(): Record<string, unknown> {
  return {
    id: RISK_ID,
    title: "Test risk as opened",
    description: "A neutral description.",
    category: "operational",
    category_name: "Operational",
    methodology: "nist_800_30",
    inherent_score: { likelihood: 3, impact: 4 },
    treatment: "untreated",
    treatment_owner: "owner-a",
    residual_score: {},
    review_due_at: "2027-01-15T00:00:00Z",
    accepter: "",
    instrument_reference: "",
    linked_control_ids: [],
    created_at: "2026-01-01T00:00:00Z",
    updated_at: "2026-01-01T00:00:00Z",
    level: "team",
    themes: [],
    severity: 12,
  };
}

async function json(
  route: Route,
  status: number,
  body: unknown,
  headers?: Record<string, string>,
) {
  await route.fulfill({
    status,
    contentType: "application/json",
    headers,
    body: JSON.stringify(body),
  });
}

// mockRisk routes the edit page's BFF calls to one stored risk and
// returns it, so a test can change it behind the open form.
async function mockRisk(page: Page) {
  const risk = storedRisk();
  const patches: { ifMatch: string | undefined; status: number }[] = [];

  await page.route("**/api/risks/categories**", (route) =>
    json(route, 200, {
      categories: [
        {
          id: "c1",
          slug: "operational",
          name: "Operational",
          archived: false,
          is_default: true,
        },
      ],
    }),
  );
  await page.route(`**/api/risks/${RISK_ID}`, async (route, req) => {
    const etag = `"${risk.updated_at as string}"`;
    if (req.method() === "PATCH") {
      const ifMatch = req.headers()["if-match"];
      if (ifMatch !== undefined && ifMatch !== etag) {
        patches.push({ ifMatch, status: 412 });
        await json(route, 412, {
          error: "risk changed since If-Match; reload and retry",
        });
        return;
      }
      patches.push({ ifMatch, status: 200 });
      Object.assign(risk, JSON.parse(req.postData() ?? "{}"), {
        updated_at: "2026-11-17T12:00:00Z",
      });
      await json(route, 200, { risk }, { ETag: `"${risk.updated_at}"` });
      return;
    }
    await json(route, 200, { risk }, { ETag: etag });
  });

  return { risk, patches };
}

test.describe("/risks/[id]/edit keeps its opening version (#2544)", () => {
  test("a background refetch keeps the edit; save is refused 412; Reload opens the new version", async ({
    authedPage: page,
  }) => {
    // A fake clock lets the test move past the query's 60s staleTime, so
    // a window focus really refetches the risk.
    await page.clock.install();
    const { risk, patches } = await mockRisk(page);

    await page.goto(`/risks/${RISK_ID}/edit`);
    const title = page.getByTestId("risks-create-title");
    await expect(title).toHaveValue("Test risk as opened", {
      timeout: 30_000,
    });

    await title.fill("Test risk with my edit");

    // Another editor saves the risk while the form is open.
    risk.title = "Test risk saved elsewhere";
    risk.updated_at = "2026-11-17T09:00:00Z";

    // Background refetch: past staleTime, the tab regains focus.
    await page.clock.fastForward(61_000);
    const refetched = page.waitForResponse(
      (r) =>
        r.url().endsWith(`/api/risks/${RISK_ID}`) &&
        r.request().method() === "GET",
      { timeout: 30_000 },
    );
    await page.evaluate(() => {
      window.dispatchEvent(new Event("visibilitychange"));
      window.dispatchEvent(new Event("focus"));
    });
    await refetched;

    // The refetch brought the new version, and the form kept the edit.
    await expect(title).toHaveValue("Test risk with my edit");
    await expect(page.getByTestId("risk-edit-stale")).toHaveCount(0);

    await page.getByTestId("risks-edit-submit").click();
    await expect(page.getByTestId("risk-edit-stale")).toBeVisible({
      timeout: 30_000,
    });
    // The save carried the opening version, not the refetched one.
    expect(patches).toEqual([
      { ifMatch: '"2026-01-01T00:00:00Z"', status: 412 },
    ]);
    expect(risk.title).toBe("Test risk saved elsewhere");

    await page.getByTestId("risk-edit-stale-reload").click();
    await expect(page.getByTestId("risk-edit-stale")).toHaveCount(0);
    await expect(title).toHaveValue("Test risk saved elsewhere");
  });

  test("changing the methodology shows the residual-reset note", async ({
    authedPage: page,
  }) => {
    await mockRisk(page);

    await page.goto(`/risks/${RISK_ID}/edit`);
    const methodology = page.getByTestId("risks-create-methodology");
    await expect(methodology).toHaveValue("nist_800_30", { timeout: 30_000 });
    const note = page.getByTestId("risk-edit-methodology-reset-note");
    await expect(note).toHaveCount(0);

    await methodology.selectOption("qualitative_5x5");
    await expect(note).toBeVisible();

    await methodology.selectOption("nist_800_30");
    await expect(note).toHaveCount(0);
  });
});
