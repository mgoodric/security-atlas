// Slice 151 — Playwright E2E for the /risks/new control-link
// multi-select.
//
// Un-quarantined by slice 351 (AC-4, disposition (a)). The legacy
// `test.skip(!PLAYWRIGHT_RUN_QUARANTINED)` + commented bodies were the
// slice-082-era placeholder. The `ControlMultiSelect` component +
// validation ship; no underlying product bug. Rewritten as a LIVE
// mocked spec per the `questionnaires.spec.ts` `route.fulfill`
// convention (anti-criterion P0-4).
//
// AC coverage:
//   AC-3  Multi-select renders ONLY when treatment === 'mitigate'.
//   AC-4  Client-side validation blocks submit with mitigate + 0 links.
//   AC-5  Form posts linked_control_ids when mitigate + selection exists.
//   AC-6  Newly created risk appears in the risk list.
//
// The control picker fetches `/api/controls-list` (TenantControl[]). We
// seed two deterministic controls, one matching the "access" filter so
// the search-narrowing assertion is stable.
//
// Determinism: every async boundary auto-waits or gates on
// `page.waitForResponse(...)`. No sleeps; no `.count()` snapshots.
//
// Hard rule (P0-A9): neutral test strings only; no vendor-prefixed
// tokens.

import { expect, test } from "./fixtures";

const CONTROL_ACCESS_ID = "33333333-3333-3333-3333-3333000ac001";
const CONTROL_OTHER_ID = "33333333-3333-3333-3333-3333000bk002";
const CREATED_RISK_ID = "00000000-0000-0000-0000-0000000r1s02";
const NEW_RISK_TITLE = "E2E ctrl-link mitigate risk";

function controlsBody() {
  return {
    controls: [
      {
        id: CONTROL_ACCESS_ID,
        title: "Logical access controls",
        control_family: "IAC",
        scf_id: "IAC-06",
        lifecycle_state: "active",
        bundle_id: "soc2-starter",
      },
      {
        id: CONTROL_OTHER_ID,
        title: "Backup integrity verification",
        control_family: "BCD",
        scf_id: "BCD-11",
        lifecycle_state: "active",
        bundle_id: "soc2-starter",
      },
    ],
    count: 2,
  };
}

test.describe("/risks/new control-link multi-select", () => {
  test("multi-select renders only when treatment is mitigate (AC-3)", async ({
    authedPage: page,
  }) => {
    await page.route("**/api/controls-list", async (route) => {
      await route.fulfill({
        status: 200,
        contentType: "application/json",
        body: JSON.stringify(controlsBody()),
      });
    });

    await page.goto("/risks/new");
    await expect(page.getByTestId("risks-create-form")).toBeVisible({
      timeout: 30_000,
    });

    // Slice 663: the opening default treatment is now "avoid" (the
    // fresh-tenant-safe default with no required satellite field), so the
    // picker is hidden on first paint.
    await expect(
      page.getByTestId("risks-create-control-multi-select"),
    ).toHaveCount(0);

    // Switch to mitigate → picker appears.
    await page.getByTestId("risks-create-treatment").selectOption("mitigate");
    await expect(
      page.getByTestId("risks-create-control-multi-select"),
    ).toBeVisible({ timeout: 30_000 });

    // Switch back to avoid → picker disappears.
    await page.getByTestId("risks-create-treatment").selectOption("avoid");
    await expect(
      page.getByTestId("risks-create-control-multi-select"),
    ).toHaveCount(0);
  });

  test("client-side validation blocks submit with mitigate + 0 links (AC-4)", async ({
    authedPage: page,
  }) => {
    let postFired = false;
    await page.route("**/api/controls-list", async (route) => {
      await route.fulfill({
        status: 200,
        contentType: "application/json",
        body: JSON.stringify(controlsBody()),
      });
    });
    await page.route("**/api/risks", async (route, req) => {
      if (req.method() === "POST") postFired = true;
      await route.fulfill({
        status: 200,
        contentType: "application/json",
        body: JSON.stringify({ risks: [] }),
      });
    });

    await page.goto("/risks/new");
    await expect(page.getByTestId("risks-create-form")).toBeVisible({
      timeout: 30_000,
    });
    await page.getByTestId("risks-create-title").fill(NEW_RISK_TITLE);
    await page.getByTestId("risks-create-treatment-owner").fill("e2e-owner");
    // Slice 663: explicitly select mitigate (no longer the opening
    // default). With no controls selected → submit is blocked
    // client-side with the required-error.
    await page.getByTestId("risks-create-treatment").selectOption("mitigate");
    await page.getByTestId("risks-create-submit").click();

    await expect(page).toHaveURL(/\/risks\/new$/);
    await expect(
      page.getByTestId("risks-create-control-multi-select-required-error"),
    ).toBeVisible({ timeout: 30_000 });
    expect(postFired).toBe(false);
  });

  test("filter narrows the picker; selecting a control posts linked_control_ids and the row appears (AC-5 + AC-6)", async ({
    authedPage: page,
  }) => {
    let created = false;
    let postedLinkedIds: string[] | undefined;

    await page.route("**/api/controls-list", async (route) => {
      await route.fulfill({
        status: 200,
        contentType: "application/json",
        body: JSON.stringify(controlsBody()),
      });
    });
    await page.route("**/api/risks", async (route, req) => {
      if (req.method() === "GET") {
        const risks = created
          ? [
              {
                id: CREATED_RISK_ID,
                title: NEW_RISK_TITLE,
                category: "operational",
                treatment: "mitigate",
                treatment_owner: "e2e-owner",
                methodology: "nist_800_30",
                inherent_score: { likelihood: 3, impact: 3, severity: 9 },
                status: "open",
              },
            ]
          : [];
        await route.fulfill({
          status: 200,
          contentType: "application/json",
          body: JSON.stringify({ risks }),
        });
        return;
      }
      // POST — capture the linked_control_ids the form posted.
      const body = JSON.parse(req.postData() ?? "{}") as {
        linked_control_ids?: string[];
      };
      postedLinkedIds = body.linked_control_ids;
      created = true;
      await route.fulfill({
        status: 201,
        contentType: "application/json",
        body: JSON.stringify({
          risk: {
            id: CREATED_RISK_ID,
            title: NEW_RISK_TITLE,
            category: "operational",
            treatment: "mitigate",
            treatment_owner: "e2e-owner",
            methodology: "nist_800_30",
            inherent_score: { likelihood: 3, impact: 3, severity: 9 },
            status: "open",
          },
        }),
      });
    });

    await page.goto("/risks/new");
    await expect(page.getByTestId("risks-create-form")).toBeVisible({
      timeout: 30_000,
    });
    await page.getByTestId("risks-create-title").fill(NEW_RISK_TITLE);
    await page.getByTestId("risks-create-treatment-owner").fill("e2e-owner");
    // Slice 663: mitigate is no longer the opening default — select it
    // so the control picker renders.
    await page.getByTestId("risks-create-treatment").selectOption("mitigate");

    // Picker is now visible. Wait for the controls list.
    await expect(
      page.getByTestId("risks-create-control-multi-select"),
    ).toBeVisible({ timeout: 30_000 });
    await expect(
      page.getByTestId("risks-create-control-multi-select-list"),
    ).toBeVisible({ timeout: 30_000 });

    // Filter narrows to the "access" control only.
    await page
      .getByTestId("risks-create-control-multi-select-filter")
      .fill("access");
    await expect(
      page.getByTestId(
        `risks-create-control-multi-select-option-${CONTROL_ACCESS_ID}`,
      ),
    ).toBeVisible();
    await expect(
      page.getByTestId(
        `risks-create-control-multi-select-option-${CONTROL_OTHER_ID}`,
      ),
    ).toHaveCount(0);

    // Select it; the summary reflects 1 selected.
    await page
      .getByTestId(
        `risks-create-control-multi-select-checkbox-${CONTROL_ACCESS_ID}`,
      )
      .check();
    await expect(
      page.getByTestId("risks-create-control-multi-select-summary"),
    ).toContainText("1 selected");

    // Submit; the POST carries the linked control id.
    const createResp = page.waitForResponse(
      (r) =>
        r.url().includes("/api/risks") &&
        r.request().method() === "POST" &&
        r.status() === 201,
      { timeout: 30_000 },
    );
    await page.getByTestId("risks-create-submit").click();
    await createResp;

    expect(postedLinkedIds).toEqual([CONTROL_ACCESS_ID]);

    // Routed back; the new row appears.
    await expect(page).toHaveURL(/\/risks(\?|$)/, { timeout: 30_000 });
    await expect(
      page
        .getByTestId("risks-row-title")
        .filter({ hasText: NEW_RISK_TITLE })
        .first(),
    ).toBeVisible({ timeout: 30_000 });
  });
});
