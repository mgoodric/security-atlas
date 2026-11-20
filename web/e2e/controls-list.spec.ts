// Slice 098 — Playwright E2E for the /controls list view.
//
// Slice 743 — UN-QUARANTINED. This spec was quarantined behind the
// slice-082 seed-data harness per slice 079's decision. Slice 743 wires
// it to `seedFromFixture("controls-list")` and un-comments the slice-098
// (AC-1 / AC-3 / AC-4 / AC-6), slice-448 (AC-1 / AC-3 / AC-4 / AC-5),
// and slice-468 (AC-2) assertion bodies so the controls-list spec joins
// the gated `Frontend · Playwright e2e` CI rotation.
//
// The fixture seeds its OWN current-SCF spine + 3 anchors (the CI e2e
// database has no SCF import step, so `scf_anchors` is otherwise empty)
// and mirrors each anchor id into a matching `controls` row so the
// slice-468 bulk-assign-owner round-trip resolves a real control per
// selected row. See fixtures/e2e/controls-list.sql for the full
// rationale.
//
// STILL QUARANTINED (preconditions out of scope for slice 743's seed):
//   - slice 224 (Scope pill) — needs a 2nd scope cell + per-cell
//     control_evaluations rows.
//   - slice 226 (Frameworks column) — needs fw_to_scf_edges crosswalk
//     rows so an anchor carries a non-empty frameworks set.
//   - slice 227 (pagination footer) — needs >=51 anchors for a 2-page
//     result.
//   - slice 225 (New-control disclosure) — a toolbar-copy assertion
//     orthogonal to the seed; left for a focused un-quarantine.
// These bodies stay commented; a later slice that seeds their
// preconditions turns them on (P0-743-3 — no assertion is relaxed to
// pass).
//
// Run locally:
//   cd web
//   npx playwright install chromium     # once per machine
//   npx playwright test e2e/controls-list.spec.ts

import type { Page } from "@playwright/test";

import { expect, test } from "./fixtures";

import { seedFromFixture } from "./seed";

// #2141 — assign the selection to a person picked by name.
async function assignTo(page: Page, name: string): Promise<void> {
  await page.getByTestId("controls-bulk-assign-owner").click();
  await page.getByTestId("controls-bulk-owner").fill(name);
  await page
    .getByTestId("controls-bulk-owner-option")
    .filter({ has: page.getByText(name, { exact: true }) })
    .first()
    .click();
  await page.getByTestId("controls-bulk-assign-confirm").click();
}

test.describe("/controls list view", () => {
  test.beforeAll(() => {
    seedFromFixture("controls-list");
  });

  test("AC-1: /controls renders the anchor table for any signed-in user", async ({
    authedPage: page,
  }) => {
    await page.goto("/controls");
    await expect(page.getByRole("heading", { name: /Controls/ })).toBeVisible();
    await expect(page.getByTestId("list-page")).toBeVisible();
    await expect(page.getByTestId("list-table-wrap")).toBeVisible();
  });

  test("AC-3: horizontal pill filter row narrows the result set", async ({
    authedPage: page,
  }) => {
    // #2780 — the demo tenant's controls are shared: other specs' beforeAll
    // seeds add controls to it while this runs, and the old version
    // compared two row counts taken a moment apart (after networkidle) for
    // whichever family sorted first. The test asserts membership instead,
    // for a family this fixture seeds: before the filter a row of another
    // family shows, after it every row is that family and this fixture's
    // control of it is listed. No count, so no other spec's rows matter.
    const FAMILY = "Cryptography"; // fixture anchor/control 1's family
    const rows = page.getByTestId("list-table-row");
    await page.goto("/controls");
    await expect(rows.filter({ hasNotText: FAMILY }).first()).toBeVisible();
    await page.getByLabel("Family").selectOption({ label: FAMILY });
    await page.waitForURL((u) => u.searchParams.get("family") === FAMILY);
    // In this order: the old page stays up until the filtered one lands
    // (keepPreviousData), and the second check rules out an empty table.
    await expect(rows.filter({ hasNotText: FAMILY })).toHaveCount(0);
    await expect(rows.filter({ hasText: "0E2E743-1" })).toBeVisible();
    // The filter row is horizontal (P0-A2) — verify the pill row renders.
    await expect(page.getByTestId("list-filter-pills")).toBeVisible();
  });

  test("AC-4: empty state surfaces when filters return zero rows", async ({
    authedPage: page,
  }) => {
    await page.goto("/controls?family=DOES-NOT-EXIST");
    await expect(page.getByTestId("list-empty-state")).toBeVisible();
    await expect(
      page.getByText("No controls match these filters"),
    ).toBeVisible();
    // The CTA reads "Clear filters" and clearing returns the user to a
    // populated table.
    await page.getByTestId("list-empty-state-cta").click();
    await expect(page.getByTestId("list-table-wrap")).toBeVisible();
  });

  test("AC-6: row click navigates to /controls/[id]", async ({
    authedPage: page,
  }) => {
    await page.goto("/controls");
    const firstRow = page.getByTestId("list-table-row").first();
    const scfIdLink = firstRow.getByTestId("controls-row-scf-id");
    const href = await scfIdLink.getAttribute("href");
    expect(href).toMatch(/^\/controls\//);
    await scfIdLink.click();
    await expect(page).toHaveURL(/\/controls\/[^/]+$/);
  });

  // #2112 — the fixture's control 3 has an `excepted` evaluation. Its
  // State pill must read "Excepted" and never show as a pass.
  test("excepted control shows Excepted, not pass", async ({
    authedPage: page,
  }) => {
    await page.goto("/controls");
    const row = page
      .getByTestId("list-table-row")
      .filter({ hasText: "0E2E743-3" });
    const state = row.getByTestId("controls-row-state");
    await expect(state).toHaveText("Excepted");
    await expect(state).toHaveAttribute("data-result", "excepted");
    await expect(state).not.toContainText(/pass/i);
  });

  // Slice 224 — Scope filter pill (5th pill, server-side intersection).
  // Pre-conditions the seed harness (slice 082) must establish before
  // these assertions are turned on:
  //   - At least two scope cells in the tenant (the bootstrap seed
  //     ships one default cell; the seed harness adds a second so the
  //     select option assertion exercises a non-degenerate dropdown).
  //   - At least one control_evaluations row recorded against each
  //     cell so the worst_per_anchor rollup narrows visibly when the
  //     pill is set.
  // STILL QUARANTINED (slice 743): these preconditions are out of scope
  // for the controls-list seed.
  test("slice 224 AC-1: Scope pill renders as 5th filter pill", async () => {
    //    await page.goto("/controls");
    //    await expect(page.getByTestId("list-filter-pill-framework")).toBeVisible();
    //    await expect(page.getByTestId("list-filter-pill-family")).toBeVisible();
    //    await expect(page.getByTestId("list-filter-pill-result")).toBeVisible();
    //    await expect(page.getByTestId("list-filter-pill-freshness")).toBeVisible();
    //    await expect(page.getByTestId("list-filter-pill-scope")).toBeVisible();
  });

  test("slice 224 AC-3: selecting a scope cell sets ?scope=<id> on the URL", async () => {
    //    await page.goto("/controls");
    //    const scopePill = page.getByLabel("Scope");
    //    // Pick the second option (first non-ALL cell).
    //    await scopePill.selectOption({ index: 1 });
    //    await page.waitForLoadState("networkidle");
    //    const url = new URL(page.url());
    //    expect(url.searchParams.get("scope")).toMatch(
    //      /^[0-9a-f-]{36}$/,
    //    );
  });

  test("slice 224 AC-3: clearing the scope cell removes ?scope from the URL", async () => {
    //    await page.goto("/controls?scope=00000000-0000-0000-0000-000000000001");
    //    const scopePill = page.getByLabel("Scope");
    //    await scopePill.selectOption({ index: 0 }); // "All cells"
    //    await page.waitForLoadState("networkidle");
    //    const url = new URL(page.url());
    //    expect(url.searchParams.get("scope")).toBeNull();
  });

  // Slice 226 — Frameworks-per-row column (right-aligned, mockup line 197).
  // Pre-conditions the seed harness (slice 082) must establish before
  // these assertions are turned on:
  //   - The SCF catalog + at least one framework crosswalk (SOC 2 v2017)
  //     are loaded so at least one anchor carries a non-empty frameworks
  //     array. The setupHTTPServer in the Go integration tests already
  //     does this for the integration suite; the seed harness must
  //     replicate the bring-up for the e2e harness.
  // STILL QUARANTINED (slice 743): the fw_to_scf_edges crosswalk rows are
  // out of scope for the controls-list seed.
  test("slice 226 AC-5: Frameworks column header is present", async () => {
    //    await page.goto("/controls");
    //    await expect(
    //      page.getByRole("columnheader", { name: /Frameworks/ }),
    //    ).toBeVisible();
  });

  test("slice 226 AC-5 + AC-9: at least one row carries a non-empty Frameworks cell", async () => {
    //    await page.goto("/controls");
    //    // Wait for data to load.
    //    await page.waitForLoadState("networkidle");
    //    const populatedFrameworks = page.getByTestId("controls-row-frameworks");
    //    expect(await populatedFrameworks.count()).toBeGreaterThan(0);
    //    // At least one cell must contain the middle-dot separator OR a
    //    // single canonical abbreviation (SOC2 / ISO / CSF / PCI / HIPAA / GDPR).
    //    const firstText = await populatedFrameworks.first().textContent();
    //    expect(firstText).toMatch(/SOC2|ISO|CSF|PCI|HIPAA|GDPR/);
  });

  test("slice 226 AC-6: anchors with no satisfaction edges render the em-dash placeholder", async () => {
    //    await page.goto("/controls");
    //    await page.waitForLoadState("networkidle");
    //    // The empty-set marker shares the `controls-row-frameworks-empty`
    //    // test-id so the assertion is stable when the SCF catalog
    //    // contains anchors a crosswalk hasn't mapped yet.
    //    const empties = page.getByTestId("controls-row-frameworks-empty");
    //    // 0 is a valid count (every anchor MAY be mapped); just verify
    //    // the locator is plumbed correctly — when the catalog grows to
    //    // include unmapped anchors, this becomes a non-zero check.
    //    expect(await empties.count()).toBeGreaterThanOrEqual(0);
  });

  // Slice 227 — /controls list pagination footer. The footer is
  // unconditional once at least one row is in the filtered set; with a
  // multi-page catalog the Previous / Next buttons round-trip through
  // the URL `?page=N`. Assertions stay quarantined behind the slice 082
  // seed-data harness, matching the rest of this spec. Pre-conditions
  // the harness must establish:
  //   - At least 51 anchor rows in the seeded catalog (so the default
  //     `CONTROLS_PAGE_SIZE = 50` produces a 2-page result). The SCF
  //     bootstrap importer (slice 006) ships ~53 anchors on the
  //     atlas-edge instance, which already satisfies this on main.
  // STILL QUARANTINED (slice 743): the controls-list seed ships 3
  // anchors, not the >=51 a 2-page result needs.
  test("AC-227-1: pagination footer renders with truth-telling summary", async () => {
    //    await page.goto("/controls");
    //    const footer = page.getByTestId("controls-pagination");
    //    await expect(footer).toBeVisible();
    //    // With >=51 seeded anchors the page-1 summary reads "Showing 1–50 of N".
    //    await expect(
    //      page.getByTestId("controls-pagination-summary"),
    //    ).toContainText("Showing 1–50 of");
  });

  test("AC-227-2: Previous is disabled on page 1, Next is enabled", async () => {
    //    await page.goto("/controls");
    //    await expect(page.getByTestId("controls-pagination-prev")).toBeDisabled();
    //    await expect(page.getByTestId("controls-pagination-next")).toBeEnabled();
  });

  test("AC-227-3: clicking Next advances to ?page=2 and updates the summary", async () => {
    //    await page.goto("/controls");
    //    await page.getByTestId("controls-pagination-next").click();
    //    await expect(page).toHaveURL(/\/controls\?(.*&)?page=2/);
    //    // Page 2 summary reads "Showing 51–N of N" (or similar).
    //    await expect(
    //      page.getByTestId("controls-pagination-summary"),
    //    ).toContainText("Showing 51");
    //    // Previous is now enabled; Next is disabled (only 2 pages).
    //    await expect(page.getByTestId("controls-pagination-prev")).toBeEnabled();
    //    await expect(page.getByTestId("controls-pagination-next")).toBeDisabled();
  });

  test("AC-227-4: Previous from page 2 returns to page 1 with the page param dropped", async () => {
    //    await page.goto("/controls?page=2");
    //    await page.getByTestId("controls-pagination-prev").click();
    //    // Canonical page-1 URL drops the `page` param.
    //    await expect(page).toHaveURL(/\/controls(\?[^p]*)?$/);
    //    await expect(page.getByTestId("controls-pagination-prev")).toBeDisabled();
  });

  test("AC-227-5: filter mutation while on page 2 resets to page 1", async () => {
    //    await page.goto("/controls?page=2");
    //    // Apply a filter change (Family → first non-ALL option).
    //    const familyPill = page.getByLabel("Family");
    //    await familyPill.selectOption({ index: 1 });
    //    // The page param must be dropped on the next URL replace.
    //    await expect(page).not.toHaveURL(/[?&]page=/);
    //    await expect(page.getByTestId("controls-pagination-prev")).toBeDisabled();
  });

  test("AC-227-6: refresh on ?page=2 preserves the page state", async () => {
    //    await page.goto("/controls?page=2");
    //    await page.reload();
    //    await expect(page).toHaveURL(/[?&]page=2/);
    //    await expect(
    //      page.getByTestId("controls-pagination-summary"),
    //    ).toContainText("Showing 51");
  });

  test("slice 225 AC-4: New control disclosure replaces the disabled button", async () => {
    // Slice 225 closed the F-178-225 HONESTY-GAP by replacing a
    // permanently-disabled `<Button>New control</Button>` in the
    // toolbar with a non-button `<span>` that discloses the future-
    // state (the create-control flow lands in a future slice; SCF
    // importer + atlas CLI are the current instantiation paths).
    // AC-4 has two halves:
    //
    //   1. The disclosure is present, visible, and its text contains
    //      "create-control" (load-bearing substring pinned by the
    //      vitest sibling spec at
    //      `web/app/(authed)/controls/new-control-future.test.ts`).
    //   2. No disabled `<button>` with the literal text "New control"
    //      exists anywhere on the page.
    //
    // Retired by #2082: the create-control disclosure and
    // new-control-future.ts were removed; only the "no disabled New
    // control button" half still applies.
    //
    // STILL QUARANTINED (slice 743): a toolbar-copy assertion
    // orthogonal to the controls-list seed; left for a focused
    // un-quarantine.
    //    await page.goto("/controls");
    //    const disclosure = page.getByTestId(
    //      "controls-new-control-disabled-reason",
    //    );
    //    await expect(disclosure).toBeVisible();
    //    const text = (await disclosure.textContent())?.toLowerCase() ?? "";
    //    expect(text).toContain("create-control");
    //    // `title` attribute carries the same copy as the visible text
    //    // so screen readers and pointer-hover both surface the same
    //    // disclosure. (aria-label likewise — both are set.)
    //    const titleAttr = await disclosure.getAttribute("title");
    //    expect(titleAttr).toMatch(/create-control/i);
    //    // No disabled <button> with the original label survives.
    //    await expect(
    //      page.locator("button[disabled]", { hasText: "New control" }),
    //    ).toHaveCount(0);
  });

  // ------------------------------------------------------------------
  // Slice 448 — multi-select + saved filter-views (operator ergonomics).
  //
  // Un-quarantined by slice 743. The seeded fixture ships 3 anchor rows
  // (each backed by a tenant control of the same id), the demo user as
  // the assign target, and a clean saved_views / owner-assignments reset.
  //
  // SCOPE NOTE (decisions log 468): the slice-448 bulk-assign FUTURE-STATE
  // disclosure was replaced by a WORKING trigger once the server-backed
  // endpoint landed (slice 468). The slice-468 AC-2 body below drives the
  // real mutation; the selection machinery + cap surface + saved-view
  // persistence are exercised against the live server-backed store.
  // NOTE on checkbox locators: the Checkbox primitive
  // (web/components/ui/checkbox.tsx) wraps Base UI's Checkbox.Root, which
  // renders a `role="checkbox"` element. The `data-testid` is plumbed for
  // discoverability, but Base UI resolves the testid to multiple DOM nodes
  // during hydration, so the unique, accessible locator is the ARIA role +
  // its aria-label. We use getByRole for the select checkboxes; this is the
  // stable selector, not a relaxation of the slice-448 contract.
  test("slice 448 AC-1: each row carries a select checkbox + a select-all header", async ({
    authedPage: page,
  }) => {
    await page.goto("/controls");
    await expect(page.getByTestId("list-table-row").first()).toBeVisible();
    await expect(
      page.getByRole("checkbox", { name: "Select all controls in view" }),
    ).toBeVisible();
    const rowChecks = page.getByRole("checkbox", { name: /^Select control / });
    expect(await rowChecks.count()).toBeGreaterThan(0);
  });

  test("slice 448 AC-1: selecting a row reveals the selection bar with a live count", async ({
    authedPage: page,
  }) => {
    await page.goto("/controls");
    await expect(page.getByTestId("list-table-row").first()).toBeVisible();
    // No selection bar before any row is selected.
    await expect(page.getByTestId("controls-selection-bar")).toHaveCount(0);
    await page
      .getByRole("checkbox", { name: /^Select control / })
      .first()
      .click();
    await expect(page.getByTestId("controls-selection-bar")).toBeVisible();
    await expect(page.getByTestId("controls-selection-count")).toContainText(
      "1 selected",
    );
  });

  test("slice 448 AC-1: select-all-in-view toggles every visible row", async ({
    authedPage: page,
  }) => {
    await page.goto("/controls");
    await expect(page.getByTestId("list-table-row").first()).toBeVisible();
    const rowChecks = page.getByRole("checkbox", { name: /^Select control / });
    const total = await rowChecks.count();
    const selectAll = page.getByRole("checkbox", {
      name: "Select all controls in view",
    });
    await selectAll.click();
    await expect(page.getByTestId("controls-selection-count")).toContainText(
      `${total} selected`,
    );
    // Toggling the header again clears the visible selection.
    await selectAll.click();
    await expect(page.getByTestId("controls-selection-bar")).toHaveCount(0);
  });

  // Slice 468 / #2141 — bulk assign owner round-trips to
  // `/v1/controls:bulk-assign-owner` (the upstream re-checks role + tenant
  // PER ITEM — AC-11). #2141 replaced "assign to me" with a people picker;
  // the bulk tests against a tenant of their own are in
  // controls-bulk-2141.spec.ts. Here the demo user picks themselves, which
  // is the round-trip #2128 reads back below.
  //
  // Slice 745: the success message renders in a persistent region above
  // the table, independent of the selection, so it survives the
  // selection-clear that runs in the same batched success update.
  test("slice 468 AC-2: bulk assign owner assigns the selection to the picked person", async ({
    authedPage: page,
  }) => {
    const meRes = await page.request.get("/api/me");
    const me = ((await meRes.json()) as { display_name: string }).display_name;
    await page.goto("/controls");
    await expect(page.getByTestId("list-table-row").first()).toBeVisible();
    await page
      .getByRole("checkbox", { name: /^Select control / })
      .first()
      .click();
    await expect(page.getByTestId("controls-selection-bar")).toBeVisible();
    await assignTo(page, me);
    await expect(
      page.getByTestId("controls-bulk-assign-message"),
    ).toContainText(/assigned \d+ control/i);
    // Selection clears on success — the observable success effect.
    await expect(page.getByTestId("controls-selection-bar")).toHaveCount(0);
  });

  // #2128 — the assignment is read back: assigning myself shows my name in
  // the Owner column and on the control page, while the manifest
  // owner_role stays visible as the default owner role. "My name" is read
  // from /api/me: the demo user's display name depends on which fixture
  // seeded that row first.
  test("#2128: assign to me shows my name in the Owner column and on the control page", async ({
    authedPage: page,
  }) => {
    const meRes = await page.request.get("/api/me");
    expect(meRes.ok()).toBe(true);
    const me = ((await meRes.json()) as { display_name: string }).display_name;
    expect(me).toBeTruthy();
    await page.goto("/controls");
    const row = page
      .getByTestId("list-table-row")
      .filter({ hasText: "Encryption at rest (e2e fixture control 1)" });
    await expect(row).toBeVisible();
    await row.getByRole("checkbox", { name: /^Select control / }).click();
    await assignTo(page, me);
    await expect(
      page.getByTestId("controls-bulk-assign-message"),
    ).toContainText(/assigned \d+ control/i);
    await expect(row.getByTestId("controls-row-owner")).toContainText(me);

    await page.goto("/controls/e2e74300-0000-0000-0000-00000000a001");
    await expect(page.getByTestId("control-owner")).toContainText(me);
    await expect(page.getByTestId("control-owner-role")).toHaveText(
      "security-engineering",
    );
  });

  test("#2141: below the cap, the selection bar does not mention it", async ({
    authedPage: page,
  }) => {
    await page.goto("/controls");
    await expect(page.getByTestId("list-table-row").first()).toBeVisible();
    await page
      .getByRole("checkbox", { name: /^Select control / })
      .first()
      .click();
    await expect(page.getByTestId("controls-selection-bar")).toBeVisible();
    await expect(page.getByTestId("controls-selection-bar")).not.toContainText(
      /cap/i,
    );
    // The over-cap alert is absent for a small selection; the over-cap
    // branch is driven in controls-bulk-2141.spec.ts.
    await expect(page.getByTestId("controls-selection-overcap")).toHaveCount(0);
  });

  test("slice 448 AC-4 + AC-5: save the current filter set as a named view, then re-apply it", async ({
    authedPage: page,
  }) => {
    // Save is disabled until a filter is active.
    await page.goto("/controls");
    await expect(page.getByTestId("controls-save-view-open")).toBeDisabled();
    // Apply a filter, then save.
    await page.getByLabel("Family").selectOption({ index: 1 });
    await page.waitForLoadState("networkidle");
    await expect(page.getByTestId("controls-save-view-open")).toBeEnabled();
    await page.getByTestId("controls-save-view-open").click();
    await page.getByTestId("controls-save-view-name").fill("Weekly triage");
    await page.getByTestId("controls-save-view-confirm").click();
    // The view is now selectable.
    const select = page.getByTestId("controls-saved-views-select");
    await expect(select).toContainText("Weekly triage");
    // Clear filters (None), then re-load the saved view and confirm the
    // family filter is re-applied to the URL. The filter state lives in the
    // URL (the page does router.replace), and that update is async — wait on
    // the URL change between selections rather than networkidle, which does
    // NOT settle a client-side router.replace.
    await select.selectOption({ label: "None" });
    await page.waitForURL((u) => !u.searchParams.has("family"));
    await select.selectOption({ label: "Weekly triage" });
    await page.waitForURL((u) => u.searchParams.has("family"));
    expect(new URL(page.url()).searchParams.get("family")).not.toBeNull();
  });

  test("slice 448 AC-4: a duplicate view name is rejected with an inline error", async ({
    authedPage: page,
  }) => {
    await page.goto("/controls?family=IAC");
    await page.getByTestId("controls-save-view-open").click();
    await page.getByTestId("controls-save-view-name").fill("My view");
    await page.getByTestId("controls-save-view-confirm").click();
    // Wait for the first save to settle before re-opening.
    await expect(page.getByTestId("controls-saved-views-select")).toContainText(
      "My view",
    );
    // Save the same name again (case-insensitive duplicate).
    await page.getByTestId("controls-save-view-open").click();
    await page.getByTestId("controls-save-view-name").fill("my view");
    await page.getByTestId("controls-save-view-confirm").click();
    await expect(page.getByTestId("controls-save-view-error")).toContainText(
      /already exists/i,
    );
  });

  // Un-quarantined by slice 746. The slice-743 finding (stale post-delete
  // `<select>`) was a real live-impl bug: the saved-views BFF GET handler
  // returned the browser-facing Response without `Cache-Control: no-store`,
  // so the React-Query refetch after a DELETE could be served the
  // browser-HTTP-cached (pre-delete) body and the deleted view lingered
  // until a hard reload. Slice 746 attaches `Cache-Control: no-store` to the
  // saved-views BFF GET (per-route, via the opt-in `noStore` wrapper — the
  // shared `forwardJSON` default is unchanged), so the in-place refetch now
  // returns the fresh (shorter) list and the deleted view drops out without
  // a page reload. This assertion is the oracle for that fix.
  test("slice 448 AC-5: a saved view can be deleted", async ({
    authedPage: page,
  }) => {
    await page.goto("/controls?family=IAC");
    await page.getByTestId("controls-save-view-open").click();
    await page.getByTestId("controls-save-view-name").fill("Disposable");
    await page.getByTestId("controls-save-view-confirm").click();
    await expect(page.getByTestId("controls-saved-views-select")).toContainText(
      "Disposable",
    );
    await page.getByTestId("controls-saved-views-delete").click();
    await expect(
      page.getByTestId("controls-saved-views-select"),
    ).not.toContainText("Disposable");
  });
});
