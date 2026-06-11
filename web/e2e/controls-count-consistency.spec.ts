// Slice 666 — Playwright E2E pinning the /controls header/footer count
// consistency fix (ATLAS-007).
//
// Before this slice the header read "Showing 53 of 53 SCF anchors"
// while the pagination footer read "Showing 1–50 of 53" — a
// contradiction (the header's verb "Showing" implied all 53 were on
// screen, the footer paginated only the first 50).
//
// This spec is HERMETIC: it route-mocks the `/api/controls` BFF GET so
// it does not depend on the shared docker-compose seed (per the
// slice 594 shared-DB → hermetic-mock convention). It mocks a catalog
// of 53 anchors (the count from the audit) and asserts:
//
//   - the header is a COUNT and never uses the verb "Showing" (the
//     verb belongs exclusively to the footer);
//   - with no filter the header reads "53 SCF anchors";
//   - the footer reads "Showing 1–50 of 53" (page range of 53);
//   - the two are mutually consistent — both speak of the same 53
//     total, and only the footer claims a range.

import { expect, test } from "./fixtures";

// Build N mock anchors. Neutral test strings only (P0-A9 / GitGuardian).
function mockAnchors(count: number) {
  return Array.from({ length: count }, (_, i) => {
    const n = String(i + 1).padStart(3, "0");
    return {
      id: `00000000-0000-4000-8000-0000000${n.padStart(5, "0")}`,
      scf_id: `SCF-TST-${n}`,
      family: i % 3 === 0 ? "Access Control" : "Risk Management",
      name: `Test anchor ${n}`,
      description: `Neutral test anchor ${n}`,
      state: null,
      frameworks: [],
    };
  });
}

test.describe("/controls header/footer count consistency (slice 666)", () => {
  test("header is a count without the verb 'Showing'; footer owns the range", async ({
    authedPage,
  }) => {
    // Hermetic: 53 anchors, no per-tenant state, no framework edges.
    await authedPage.route("**/api/controls**", async (route) => {
      await route.fulfill({
        status: 200,
        contentType: "application/json",
        body: JSON.stringify({ anchors: mockAnchors(53) }),
      });
    });
    // Scope-cells call is non-fatal but mock it so the pill renders
    // deterministically and no network reaches the platform.
    await authedPage.route("**/api/scopes/cells**", async (route) => {
      await route.fulfill({
        status: 200,
        contentType: "application/json",
        body: JSON.stringify({ cells: [] }),
      });
    });

    await authedPage.goto("/controls");

    // Header count label — present, reads "53 SCF anchors", and does
    // NOT use the verb "Showing".
    const header = authedPage.getByTestId("controls-count-label");
    await expect(header).toBeVisible();
    await expect(header).toContainText("53 SCF anchors");
    await expect(header).not.toContainText("Showing");

    // Footer pagination summary — owns the "Showing M–N of TOTAL"
    // phrasing; with 53 anchors at 50/page it reads "Showing 1–50 of 53".
    const footer = authedPage.getByTestId("controls-pagination-summary");
    await expect(footer).toBeVisible();
    await expect(footer).toContainText("Showing 1–50 of 53");

    // Consistency: the header total (53) matches the footer total (53),
    // and only the footer claims a page range — non-contradictory.
    const headerText = (await header.textContent())?.trim() ?? "";
    expect(headerText).not.toContain("Showing");
    expect(headerText).toContain("53");
  });
});
