// #2117 — Playwright for the audit-period detail page (/audits/[id]).
//
//   1. Open a period from the /audits list, see its per-control states,
//      cancel the freeze confirm (nothing is sent), then freeze it and
//      see the freeze hash.
//   2. On a frozen period with the OSCAL bridge available, the export
//      downloads from the page.
//   3. With the bridge absent (the platform does not mount the export
//      route), the page says so instead of saving an error as a file.
//
// MOCK STRATEGY: the BFF wire surface is mocked on the browser CONTEXT,
// as in oscal-export-e2e.spec.ts. Freezing is irreversible, so freezing
// the shared seeded period would change it for every other spec; the
// freeze itself is covered by the Go tier (internal/audit/period).
//
// Hard rule (P0-A9 / GitGuardian): every id and hash is a neutral test
// string.

import type { BrowserContext } from "@playwright/test";

import { expect, test } from "./fixtures";

const PERIOD_ID = "00000000-0000-0000-0000-000000002117";
const CONTROL_A = "00000000-0000-0000-0000-0000000021a1";
const CONTROL_B = "00000000-0000-0000-0000-0000000021b2";
const FROZEN_AT = "2026-04-01T00:00:00Z";
const FROZEN_HASH =
  "2117211721172117211721172117211721172117211721172117211721172117";

type Period = Record<string, unknown>;

function openPeriod(): Period {
  return {
    id: PERIOD_ID,
    name: "SOC 2 2026 Q1 (test-2117)",
    framework_version_id: "00000000-0000-0000-0000-0000000000ff",
    period_start: "2026-01-01T00:00:00Z",
    period_end: "2026-03-31T00:00:00Z",
    status: "open",
    created_by: "test-operator",
    created_at: "2026-01-01T00:00:00Z",
    updated_at: "2026-01-01T00:00:00Z",
  };
}

function frozenPeriod(): Period {
  return {
    ...openPeriod(),
    status: "frozen",
    frozen_at: FROZEN_AT,
    frozen_by: "test-operator",
    frozen_hash: FROZEN_HASH,
    updated_at: FROZEN_AT,
  };
}

function json(body: unknown, status = 200) {
  return {
    status,
    contentType: "application/json",
    body: JSON.stringify(body),
  };
}

// Mocks the list, the single-period read, the controls list and the
// per-control horizon read. `current` is read on every request, so a
// freeze that swaps it is seen by later reads.
async function mockPeriod(
  context: BrowserContext,
  current: { period: Period },
) {
  await context.route(/\/api\/audits$/, async (route, req) => {
    if (req.method() !== "GET") return route.fallback();
    await route.fulfill(
      json({
        audit_periods: [{ ...current.period, framework_label: "SOC 2 2017" }],
        count: 1,
      }),
    );
  });
  await context.route(
    new RegExp(`/api/audits/${PERIOD_ID}$`),
    async (route, req) => {
      if (req.method() !== "GET") return route.fallback();
      await route.fulfill(json({ audit_period: current.period }));
    },
  );
  await context.route(/\/api\/controls-list$/, async (route) => {
    await route.fulfill(
      json({
        controls: [
          {
            id: CONTROL_A,
            title: "Access reviews (test-2117)",
            control_family: "IAC",
            scf_id: "IAC-17",
            lifecycle_state: "active",
            bundle_id: "test-bundle",
          },
          {
            id: CONTROL_B,
            title: "Backups (test-2117)",
            control_family: "BCD",
            scf_id: "BCD-11",
            lifecycle_state: "active",
            bundle_id: "test-bundle",
          },
        ],
        count: 2,
      }),
    );
  });
  await context.route(
    new RegExp(`/api/audits/${PERIOD_ID}/control-state\\?`),
    async (route, req) => {
      const control = new URL(req.url()).searchParams.get("control");
      const observations =
        control === CONTROL_A
          ? [
              {
                evidence_record_id: "00000000-0000-0000-0000-0000000021e1",
                observed_at: "2026-02-01T00:00:00Z",
                result: "pass",
                hash: "aa",
              },
              {
                evidence_record_id: "00000000-0000-0000-0000-0000000021e2",
                observed_at: "2026-03-01T00:00:00Z",
                result: "fail",
                hash: "bb",
              },
            ]
          : [];
      await route.fulfill(
        json({
          audit_period_id: PERIOD_ID,
          control_id: control,
          observations,
          count: observations.length,
        }),
      );
    },
  );
}

test.describe("audit-period detail (#2117)", () => {
  test("open a period from the list, see per-control states, freeze it after the confirm, see the hash", async ({
    authedPage: page,
  }) => {
    const context = page.context();
    const current = { period: openPeriod() };
    await mockPeriod(context, current);

    let freezeCalls = 0;
    await context.route(
      new RegExp(`/api/audits/${PERIOD_ID}/freeze$`),
      async (route, req) => {
        expect(req.method()).toBe("POST");
        freezeCalls += 1;
        current.period = frozenPeriod();
        await route.fulfill(json({ audit_period: current.period }));
      },
    );

    await page.goto("/audits");
    const row = page
      .getByTestId("list-table-row")
      .filter({ hasText: "test-2117" });
    await expect(row).toBeVisible({ timeout: 30_000 });
    await row.click();
    await expect(page).toHaveURL(new RegExp(`/audits/${PERIOD_ID}$`));

    // Header: name, status, framework label, dates.
    await expect(page.getByTestId("audit-period-name")).toHaveText(
      "SOC 2 2026 Q1 (test-2117)",
    );
    await expect(page.getByTestId("audit-period-status")).toHaveText("Open");
    await expect(page.getByTestId("audit-period-framework")).toHaveText(
      "SOC 2 2017",
    );

    // Per-control states from the horizon read.
    const rowA = page.locator(
      `[data-testid="audit-period-control-row"][data-control-id="${CONTROL_A}"]`,
    );
    const rowB = page.locator(
      `[data-testid="audit-period-control-row"][data-control-id="${CONTROL_B}"]`,
    );
    await expect(rowA.getByTestId("audit-period-control-count")).toHaveText(
      "2",
    );
    await expect(rowA.getByTestId("audit-period-control-result")).toHaveText(
      "Fail",
    );
    await expect(rowB.getByTestId("audit-period-control-count")).toHaveText(
      "0",
    );
    await expect(rowB.getByTestId("audit-period-control-result")).toHaveText(
      "No evidence",
    );

    // The confirm says the evidence becomes immutable; Cancel sends
    // nothing and leaves the period open.
    await page.getByTestId("audit-period-freeze").click();
    const confirm = page.getByTestId("audit-period-freeze-confirm");
    await expect(confirm).toBeVisible();
    await expect(confirm).toContainText("evidence becomes immutable");
    await page.getByTestId("audit-period-freeze-confirm-cancel").click();
    await expect(confirm).toBeHidden();
    // #2518: the header's Freeze, disabled while the confirm was open,
    // gets focus back.
    await expect(page.getByTestId("audit-period-freeze")).toBeFocused();
    expect(freezeCalls).toBe(0);
    await expect(page.getByTestId("audit-period-status")).toHaveText("Open");
    await expect(page.getByTestId("audit-period-frozen-hash")).toHaveCount(0);

    // Confirm: the period freezes and the hash and time are shown.
    await page.getByTestId("audit-period-freeze").click();
    await page.getByTestId("audit-period-freeze-confirm-ok").click();
    await expect(page.getByTestId("audit-period-frozen-hash")).toHaveText(
      FROZEN_HASH,
    );
    await expect(page.getByTestId("audit-period-frozen-at")).toHaveText(
      FROZEN_AT,
    );
    await expect(page.getByTestId("audit-period-status")).toHaveText("Frozen");
    await expect(page.getByTestId("audit-period-freeze")).toHaveCount(0);
    expect(freezeCalls).toBe(1);
  });

  test("with the OSCAL bridge available, export downloads from the page", async ({
    authedPage: page,
  }) => {
    const context = page.context();
    await mockPeriod(context, { period: frozenPeriod() });
    const filename = `oscal-bundle-${PERIOD_ID}-2026-04-01.json`;
    await context.route(
      new RegExp(`/api/audits/${PERIOD_ID}/oscal-export$`),
      async (route) => {
        await route.fulfill({
          status: 200,
          contentType: "application/json",
          headers: {
            "Content-Disposition": `attachment; filename="${filename}"`,
          },
          body: JSON.stringify({
            audit_period_id: PERIOD_ID,
            frozen_at: FROZEN_AT,
            members: [],
          }),
        });
      },
    );

    await page.goto(`/audits/${PERIOD_ID}`);
    const button = page.getByTestId("audit-period-oscal-export");
    await expect(button).toBeVisible({ timeout: 30_000 });

    const downloadPromise = context.waitForEvent("download", {
      timeout: 30_000,
    });
    await button.click();
    const download = await downloadPromise;
    expect(download.suggestedFilename()).toBe(filename);
  });

  test("with the OSCAL bridge absent, the page says so", async ({
    authedPage: page,
  }) => {
    const context = page.context();
    await mockPeriod(context, { period: frozenPeriod() });
    // The platform mounts no export route without the bridge: chi's
    // bare 404, which the BFF passes through.
    await context.route(
      new RegExp(`/api/audits/${PERIOD_ID}/oscal-export$`),
      async (route) => {
        await route.fulfill({
          status: 404,
          contentType: "text/plain",
          body: "404 page not found",
        });
      },
    );

    await page.goto(`/audits/${PERIOD_ID}`);
    await page.getByTestId("audit-period-oscal-export").click();
    await expect(
      page.getByTestId("audit-period-oscal-export-unavailable"),
    ).toContainText("OSCAL bridge isn't running");
  });
});
