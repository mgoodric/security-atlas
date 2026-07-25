// Slice 684 — Playwright E2E pinning the /risks header/footer count
// consistency fix (the identical defect slice 666 fixed on /controls).
//
// Before this slice the header read "Showing {visible} of {rows} risks"
// while the shared pagination footer read "Showing 1–50 of 53" — a
// contradiction (the header's verb "Showing" implied all rows were on
// screen, the footer paginated only the first 50).
//
// This spec is HERMETIC: it route-mocks the `/api/risks` BFF GET so it
// does not depend on the shared docker-compose seed (per the slice 594
// shared-DB → hermetic-mock convention). It mocks a register of 53 risks
// and asserts:
//
//   - the header is a COUNT and never uses the verb "Showing" (the verb
//     belongs exclusively to the footer);
//   - with no filter the header reads "53 risks";
//   - the footer reads "Showing 1–50 of 53" (page range of 53);
//   - the two are mutually consistent — both speak of the same 53 total,
//     and only the footer claims a range.

import { expect, test } from "./fixtures";

// Build N mock risks. Neutral test strings only (P0-A4 / GitGuardian).
// Shape mirrors `web/lib/api/risks.ts` `Risk` (the fields the list page
// reads); unused optional fields are omitted and default-render fine.
function mockRisks(count: number) {
  return Array.from({ length: count }, (_, i) => {
    const n = String(i + 1).padStart(3, "0");
    return {
      id: `00000000-0000-4000-8000-0000000${n.padStart(5, "0")}`,
      title: `Test risk ${n}`,
      description: `Neutral test risk ${n}`,
      category: "operational",
      methodology: "nist_800_30",
      inherent_score: null,
      treatment: i % 2 === 0 ? "mitigate" : "accept",
      treatment_owner: i % 2 === 0 ? "owner-a" : "owner-b",
      residual_score: null,
      accepter: "",
      instrument_reference: "",
      linked_control_ids: [],
      created_at: "2026-01-01T00:00:00Z",
      updated_at: "2026-01-01T00:00:00Z",
      themes: [],
      severity: 5,
    };
  });
}

test.describe("/risks header/footer count consistency (slice 684)", () => {
  test("header is a count without the verb 'Showing'; footer owns the range", async ({
    authedPage,
  }) => {
    // The page also fetches org-units for the Org unit pill; mock it so
    // the pill renders deterministically and no network reaches upstream.
    // Registered FIRST so it wins over the broader risks-list pattern for
    // the `/api/risks-hierarchy/**` path (Playwright matches the most
    // recently registered handler first).
    await authedPage.route(
      "**/api/risks-hierarchy/org-units**",
      async (route) => {
        await route.fulfill({
          status: 200,
          contentType: "application/json",
          body: JSON.stringify({ org_units: [] }),
        });
      },
    );
    // Hermetic: 53 risks, no per-tenant state. The list page fetches the
    // BFF at exactly `/api/risks` (no query string); a trailing `?` is
    // tolerated. The glob is anchored with a boundary so it does NOT also
    // swallow the `/api/risks-hierarchy/**` route above.
    await authedPage.route(/\/api\/risks(\?.*)?$/, async (route) => {
      await route.fulfill({
        status: 200,
        contentType: "application/json",
        body: JSON.stringify({ risks: mockRisks(53) }),
      });
    });

    await authedPage.goto("/risks");

    // Header count label — present, reads "53 risks", and does NOT use
    // the verb "Showing".
    const header = authedPage.getByTestId("risks-count-label");
    await expect(header).toBeVisible();
    await expect(header).toContainText("53 risks");
    await expect(header).not.toContainText("Showing");

    // Footer pagination summary — owns the "Showing M–N of TOTAL"
    // phrasing; with 53 risks at 50/page it reads "Showing 1–50 of 53".
    const footer = authedPage.getByTestId("risks-pagination-summary");
    await expect(footer).toBeVisible();
    await expect(footer).toContainText("Showing 1–50 of 53");

    // Consistency: the header total (53) matches the footer total (53),
    // and only the footer claims a page range — non-contradictory.
    const headerText = (await header.textContent())?.trim() ?? "";
    expect(headerText).not.toContain("Showing");
    expect(headerText).toContain("53");
  });
});
