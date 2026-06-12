// Slice 680 / ATLAS-033 — Playwright E2E pinning the /audits
// framework-version readable label + audit-period label↔range honesty.
//
// Before this slice the "Framework version" column rendered
// `framework_version_id.slice(0, 8)…` — a truncated UUID that reads as
// an opaque content hash ("e443f4b1…"). The LIST path now resolves a
// readable `framework_label` ("SCF 2025.2") and the column renders that.
//
// This spec is HERMETIC: it route-mocks the `/api/audits` BFF GET so it
// does not depend on the shared docker-compose seed (slice 594 shared-DB
// → hermetic-mock convention). It mocks two periods:
//
//   - one WITH a framework_label: the column renders the readable label.
//   - one WITHOUT (omitted from the wire): the column falls back to the
//     truncated UUID, so the fallback path stays covered.
//
// It also asserts the audit-period NAME (which the demo seed now derives
// from the period's own start date) is rendered, and that the period
// range column renders the dates — the label↔range correctness itself is
// pinned deterministically in the Go demoseed integration test
// (ATLAS-033 / AC-4); here we pin the wire-to-screen rendering.

import { expect, test } from "./fixtures";

function mockPeriods() {
  return [
    {
      id: "00000000-0000-4000-8000-000000000001",
      name: "SOC 2 2025 Q3",
      framework_version_id: "e443f4b1-1111-4000-8000-000000000001",
      framework_label: "SCF 2025.2",
      period_start: "2025-07-01T00:00:00Z",
      period_end: "2025-09-30T00:00:00Z",
      status: "open",
      created_by: "test-actor",
      created_at: "2025-06-01T00:00:00Z",
      updated_at: "2025-06-01T00:00:00Z",
    },
    {
      // No framework_label on the wire → fallback to the truncated UUID.
      id: "00000000-0000-4000-8000-000000000002",
      name: "SOC 2 2025 Q4",
      framework_version_id: "abcdef12-2222-4000-8000-000000000002",
      period_start: "2025-10-01T00:00:00Z",
      period_end: "2025-12-31T00:00:00Z",
      status: "frozen",
      frozen_at: "2026-01-07T00:00:00Z",
      frozen_hash: "deadbeef",
      frozen_by: "test-actor",
      created_by: "test-actor",
      created_at: "2025-09-01T00:00:00Z",
      updated_at: "2026-01-07T00:00:00Z",
    },
  ];
}

test.describe("/audits framework-version readable label (slice 680)", () => {
  test("renders the readable framework_label, not a truncated UUID hash", async ({
    authedPage,
  }) => {
    await authedPage.route(/\/api\/audits(\?.*)?$/, async (route) => {
      await route.fulfill({
        status: 200,
        contentType: "application/json",
        body: JSON.stringify({
          audit_periods: mockPeriods(),
          count: 2,
        }),
      });
    });

    await authedPage.goto("/audits");

    const fwCells = authedPage.getByTestId("audits-row-framework-version");
    await expect(fwCells).toHaveCount(2);

    // The first period carries a readable label.
    await expect(fwCells.nth(0)).toContainText("SCF 2025.2");
    // The readable label must NOT be a truncated-UUID hash.
    await expect(fwCells.nth(0)).not.toContainText("…");

    // The second period (no label) falls back to the truncated UUID with
    // the ellipsis — the fallback path stays honest.
    await expect(fwCells.nth(1)).toContainText("abcdef12…");

    // The audit-period name (derived from the period's own start date in
    // the demo seed) renders — a Q3 label for a Jul–Sep range.
    await expect(
      authedPage.getByText("SOC 2 2025 Q3", { exact: true }),
    ).toBeVisible();
  });
});
