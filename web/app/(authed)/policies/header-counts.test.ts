// Slice 239 — vitest coverage for the /policies header status-count
// formatter. Pure-data tests; no React, no DOM.
//
// AC-5 case coverage: (a) all-published rows, (b) mixed statuses,
// (c) zero rows, (d) `under_review` row promotes the fourth segment.
// Plus the count-0 omission and ordering rules from AC-2 / AC-4.

import { describe, expect, test } from "vitest";

import type { Policy } from "@/lib/api/policies";

import { statusCountsLabel, TALLY_STATUS_ORDER } from "./header-counts";

function makePolicy(over: Partial<Policy> = {}): Policy {
  return {
    id: "00000000-0000-0000-0000-000000000000",
    title: "test policy",
    version: "v1.0",
    body_md: "",
    owner_role: "security_lead",
    approver_role: "cto",
    linked_control_ids: [],
    acknowledgment_required_roles: [],
    status: "published",
    source_attribution: "in_house",
    created_by: "user-1",
    created_at: "2026-01-01T00:00:00Z",
    updated_at: "2026-01-01T00:00:00Z",
    ...over,
  };
}

describe("statusCountsLabel (AC-5)", () => {
  test("AC-5(c) / AC-3: empty input → empty string", () => {
    expect(statusCountsLabel([])).toBe("");
  });

  test("AC-5(a) all-published: only that bin renders ('3 published')", () => {
    const rows = Array.from({ length: 3 }, (_, i) =>
      makePolicy({ id: `p${i}`, status: "published" }),
    );
    expect(statusCountsLabel(rows)).toBe("3 published");
  });

  test("AC-5(a) all-one-status (draft): only that bin renders", () => {
    const rows = Array.from({ length: 7 }, (_, i) =>
      makePolicy({ id: `d${i}`, status: "draft" }),
    );
    expect(statusCountsLabel(rows)).toBe("7 draft");
  });

  test("AC-5(b) / AC-1 mockup case: '14 published · 2 draft · 1 retired'", () => {
    const rows = [
      ...Array.from({ length: 14 }, (_, i) =>
        makePolicy({ id: `pub${i}`, status: "published" }),
      ),
      ...Array.from({ length: 2 }, (_, i) =>
        makePolicy({ id: `drf${i}`, status: "draft" }),
      ),
      makePolicy({ id: "ret1", status: "retired" }),
    ];
    expect(statusCountsLabel(rows)).toBe("14 published · 2 draft · 1 retired");
  });

  test("AC-5(b) mixed statuses preserve canonical order regardless of input order", () => {
    // Intentionally scramble insertion order — the renderer must
    // sort by TALLY_STATUS_ORDER, not by data arrival.
    const rows = [
      makePolicy({ id: "1", status: "retired" }),
      makePolicy({ id: "2", status: "draft" }),
      makePolicy({ id: "3", status: "published" }),
    ];
    expect(statusCountsLabel(rows)).toBe("1 published · 1 draft · 1 retired");
  });

  test("AC-5(d) under_review row promotes the fourth segment (alphabetical tail)", () => {
    const rows = [
      ...Array.from({ length: 14 }, (_, i) =>
        makePolicy({ id: `pub${i}`, status: "published" }),
      ),
      ...Array.from({ length: 2 }, (_, i) =>
        makePolicy({ id: `drf${i}`, status: "draft" }),
      ),
      makePolicy({ id: "ret1", status: "retired" }),
      ...Array.from({ length: 3 }, (_, i) =>
        makePolicy({ id: `ur${i}`, status: "under_review" }),
      ),
    ];
    expect(statusCountsLabel(rows)).toBe(
      "14 published · 2 draft · 1 retired · 3 under_review",
    );
  });

  test("AC-2: approved + under_review both appear in alphabetical tail order", () => {
    // 'approved' before 'under_review' alphabetically.
    const rows = [
      makePolicy({ id: "1", status: "published" }),
      ...Array.from({ length: 2 }, (_, i) =>
        makePolicy({ id: `u${i}`, status: "under_review" }),
      ),
      ...Array.from({ length: 3 }, (_, i) =>
        makePolicy({ id: `a${i}`, status: "approved" }),
      ),
    ];
    expect(statusCountsLabel(rows)).toBe(
      "1 published · 3 approved · 2 under_review",
    );
  });

  test("AC-4: zero-count canonical statuses are OMITTED — no '0 published'", () => {
    // Only retired rows present — published / draft must NOT appear.
    const rows = Array.from({ length: 2 }, (_, i) =>
      makePolicy({ id: `r${i}`, status: "retired" }),
    );
    const tally = statusCountsLabel(rows);
    expect(tally).toBe("2 retired");
    expect(tally).not.toContain("0 ");
    expect(tally).not.toContain("published");
    expect(tally).not.toContain("draft");
  });

  test("P0-239-2 (cousin): renderer never invents statuses not present in data", () => {
    // Only draft + approved here — published / retired / under_review
    // must NOT appear. Approved appears in the alphabetical tail.
    const rows = [
      makePolicy({ id: "1", status: "draft" }),
      makePolicy({ id: "2", status: "approved" }),
    ];
    const tally = statusCountsLabel(rows);
    expect(tally).toBe("1 draft · 1 approved");
    expect(tally).not.toContain("published");
    expect(tally).not.toContain("retired");
    expect(tally).not.toContain("under_review");
  });

  test("non-canonical unknown status (future-proof) appears in alphabetical tail", () => {
    // If the backend lifts the status enum tomorrow, the formatter
    // should accept the new value without a code change.
    const rows = [
      makePolicy({ id: "1", status: "published" }),
      makePolicy({ id: "2", status: "definitely_not_a_real_status" }),
    ];
    expect(statusCountsLabel(rows)).toBe(
      "1 published · 1 definitely_not_a_real_status",
    );
  });

  test("separator is ' · ' (U+00B7 MIDDLE DOT) — matches mockup verbatim", () => {
    const rows = [
      makePolicy({ id: "1", status: "published" }),
      makePolicy({ id: "2", status: "draft" }),
    ];
    const tally = statusCountsLabel(rows);
    expect(tally).toContain(" · ");
    // And NOT the comma / bullet / pipe alternatives.
    expect(tally).not.toContain(", ");
    expect(tally).not.toContain(" • ");
    expect(tally).not.toContain(" | ");
  });

  test("TALLY_STATUS_ORDER matches the slice 239 AC-1 prescribed order", () => {
    expect(TALLY_STATUS_ORDER).toEqual(["published", "draft", "retired"]);
  });

  test("handles a realistic mixed set (sanity)", () => {
    // 50 published, 8 draft, 3 retired, 4 under_review, 2 approved.
    const rows = [
      ...Array.from({ length: 50 }, (_, i) =>
        makePolicy({ id: `p${i}`, status: "published" }),
      ),
      ...Array.from({ length: 8 }, (_, i) =>
        makePolicy({ id: `d${i}`, status: "draft" }),
      ),
      ...Array.from({ length: 3 }, (_, i) =>
        makePolicy({ id: `r${i}`, status: "retired" }),
      ),
      ...Array.from({ length: 4 }, (_, i) =>
        makePolicy({ id: `u${i}`, status: "under_review" }),
      ),
      ...Array.from({ length: 2 }, (_, i) =>
        makePolicy({ id: `a${i}`, status: "approved" }),
      ),
    ];
    expect(statusCountsLabel(rows)).toBe(
      "50 published · 8 draft · 3 retired · 2 approved · 4 under_review",
    );
  });
});
