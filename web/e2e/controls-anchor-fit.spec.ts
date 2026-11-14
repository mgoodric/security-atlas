// Issue #1736 — a control states how well its SCF anchor fits it
// (`anchor_fit: exact | partial | none`). The controls list marks a
// non-exact control and filters by fit; the control detail page shows
// the fit and its note.
//
// HERMETIC: the list BFFs and the detail page's coverage / state /
// effectiveness BFFs are route-mocked (slice 594 convention), so the spec
// pins the page's rendering of the wire fields. Storage, validation and
// the SQL filter are pinned server-side by
// internal/control/anchor_fit_integration_test.go and
// internal/control/manifest_test.go.

import type { Page, Route } from "@playwright/test";

import { expect, seeded, test } from "./fixtures";
import { seedFromFixture } from "./seed";

// Neutral test strings only (P0-A9 / GitGuardian).
const EXACT = "c1736000-0000-4000-8000-00000000000a";
const PARTIAL = "c1736000-0000-4000-8000-00000000000b";
const NONE = "c1736000-0000-4000-8000-00000000000c";
const NONE_NOTE = "No SCF control covers recruitment and retention.";

const base = {
  control_family: "Governance",
  lifecycle_state: "active",
  state: null,
  frameworks: [],
};
const myControls = [
  {
    ...base,
    id: EXACT,
    title: "Quarterly access review (1736)",
    scf_id: "IAC-06",
    bundle_id: "ctrl-1736-exact",
    scf_anchor_id: "a1736000-0000-4000-8000-00000000000a",
    anchor_fit: "exact",
    anchor_fit_note: null,
  },
  {
    ...base,
    id: PARTIAL,
    title: "Audit committee cadence (1736)",
    scf_id: "GOV-01",
    bundle_id: "ctrl-1736-partial",
    scf_anchor_id: "a1736000-0000-4000-8000-00000000000b",
    anchor_fit: "partial",
    anchor_fit_note: "GOV-01 covers oversight, not meeting cadence.",
  },
  {
    ...base,
    id: NONE,
    title: "Recruitment and retention (1736)",
    scf_id: "",
    bundle_id: "ctrl-1736-none",
    scf_anchor_id: null,
    anchor_fit: "none",
    anchor_fit_note: NONE_NOTE,
  },
];

async function mockListBFFs(page: Page) {
  await page.route("**/api/controls**", async (route: Route) => {
    const url = new URL(route.request().url());
    if (url.pathname === "/api/controls-list") {
      await route.fulfill({
        status: 200,
        contentType: "application/json",
        body: JSON.stringify({
          controls: myControls,
          count: myControls.length,
          total: myControls.length,
        }),
      });
      return;
    }
    if (url.pathname === "/api/controls") {
      await route.fulfill({
        status: 200,
        contentType: "application/json",
        body: JSON.stringify({
          anchors: [],
          total: 0,
          catalog_total: 1451,
          with_control_total: 2,
        }),
      });
      return;
    }
    await route.fallback();
  });
  await page.route("**/api/scope-cells**", async (route) => {
    await route.fulfill({
      status: 200,
      contentType: "application/json",
      body: JSON.stringify({ cells: [] }),
    });
  });
}

test.describe("/controls anchor fit (issue #1736)", () => {
  test("marks non-exact controls and filters the list by fit", async ({
    authedPage: page,
  }) => {
    await mockListBFFs(page);
    await page.goto("/controls");

    // The mobile card stack reuses the cell renderers (hidden at desktop),
    // so every locator is scoped to the desktop table.
    const table = page.getByTestId("list-table-wrap");
    await expect(table.getByTestId("list-table-row")).toHaveCount(3);

    // Only the partial and none controls carry a marker; its tooltip is
    // the fit note.
    const markers = table.getByTestId("controls-row-anchor-fit");
    await expect(markers).toHaveCount(2);
    await expect(
      table.locator(
        '[data-testid="controls-row-anchor-fit"][data-anchor-fit="partial"]',
      ),
    ).toHaveText("Partial fit");
    const none = table.locator(
      '[data-testid="controls-row-anchor-fit"][data-anchor-fit="none"]',
    );
    await expect(none).toHaveText("No SCF fit");
    await expect(none).toHaveAttribute("title", NONE_NOTE);

    // The Anchor fit pill narrows the list and lives in the URL.
    await page.getByLabel("Anchor fit", { exact: true }).selectOption("none");
    await page.waitForURL((u) => u.searchParams.get("anchor_fit") === "none");
    await expect(table.getByTestId("list-table-row")).toHaveCount(1);
    await expect(
      table.getByText("Recruitment and retention (1736)"),
    ).toBeVisible();

    await page.getByLabel("Anchor fit", { exact: true }).selectOption("exact");
    await page.waitForURL((u) => u.searchParams.get("anchor_fit") === "exact");
    await expect(table.getByTestId("list-table-row")).toHaveCount(1);
    await expect(table.getByTestId("controls-row-anchor-fit")).toHaveCount(0);

    // The SCF catalog has no fit, so switching view drops the filter and
    // hides the pill.
    await page.getByLabel("View", { exact: true }).selectOption("catalog");
    await page.waitForURL((u) => u.searchParams.get("view") === "catalog");
    expect(new URL(page.url()).searchParams.get("anchor_fit")).toBeNull();
    await expect(page.getByLabel("Anchor fit", { exact: true })).toHaveCount(0);
  });
});

test.describe("/controls/{id} anchor fit (issue #1736)", () => {
  test.beforeAll(() => {
    seedFromFixture("control-detail");
  });

  test("shows the fit and its note", async ({ authedPage: page }) => {
    await page.route(`**/api/controls/${seeded.controlId}/coverage`, (route) =>
      route.fulfill({
        status: 200,
        contentType: "application/json",
        body: JSON.stringify({
          control: {
            id: seeded.controlId,
            bundle_id: "ctrl-1736-detail",
            version: 1,
            title: "Recruitment and retention (1736)",
            control_family: "Human Resources",
            owner_role: "People Operations",
            implementation_type: "manual_periodic",
            lifecycle_state: "active",
            freshness_class: "annual",
            anchor_fit: "none",
            anchor_fit_note: NONE_NOTE,
          },
          anchor: null,
          requirements: [],
        }),
      }),
    );
    await page.route(`**/api/controls/${seeded.controlId}/state`, (route) =>
      route.fulfill({
        status: 200,
        contentType: "application/json",
        body: JSON.stringify({
          control_id: seeded.controlId,
          states: [],
          count: 0,
        }),
      }),
    );
    await page.route(
      `**/api/controls/${seeded.controlId}/effectiveness`,
      (route) => {
        const windowEnd = new Date();
        const windowStart = new Date(windowEnd.getTime() - 30 * 86_400_000);
        return route.fulfill({
          status: 200,
          contentType: "application/json",
          body: JSON.stringify({
            control_id: seeded.controlId,
            pass_rate: 0,
            pass_count: 0,
            total_count: 0,
            window_start: windowStart.toISOString(),
            window_end: windowEnd.toISOString(),
          }),
        });
      },
    );

    await page.goto(`/controls/${seeded.controlId}`);
    const fit = page.getByTestId("control-anchor-fit");
    await expect(fit).toBeVisible();
    await expect(fit).toHaveAttribute("data-anchor-fit", "none");
    await expect(fit).toContainText("No SCF fit");
    await expect(page.getByTestId("control-anchor-fit-note")).toHaveText(
      NONE_NOTE,
    );
    await expect(page.getByTestId("scf-anchor-pill")).toHaveText("unanchored");
  });
});
