// #2104 — how each risk audit entry reads in the History section.

import { describe, expect, test } from "vitest";

import {
  formatHistoryValue,
  historyActionLabel,
  historyChanges,
} from "./history-format";

function entry(action: string, before: object, after: object) {
  return {
    id: "e1",
    actor: "user-1",
    action,
    before: before as Record<string, unknown>,
    after: after as Record<string, unknown>,
    created_at: "2026-11-14T10:00:00Z",
  };
}

describe("risk history format (#2104)", () => {
  test("a field edit lists each changed field, before and after", () => {
    const changes = historyChanges(
      entry(
        "fields_changed",
        { title: "Old", review_due_at: "2027-01-01T00:00:00Z" },
        { title: "New", review_due_at: "2027-06-01T00:00:00-07:00" },
      ),
    );
    expect(changes).toEqual([
      { field: "Title", before: "Old", after: "New" },
      { field: "Review due", before: "2027-01-01", after: "2027-06-01" },
    ]);
  });

  test("a category move shows the slug, not the id behind it", () => {
    const changes = historyChanges(
      entry(
        "category_changed",
        { category_id: "a", category: "operational" },
        { category_id: "b", category: "privacy" },
      ),
    );
    expect(changes).toEqual([
      { field: "Category", before: "operational", after: "privacy" },
    ]);
  });

  test("a placement move drops the key that did not change", () => {
    const changes = historyChanges(
      entry(
        "placement_changed",
        { level: "team", org_unit_id: null },
        { level: "org", org_unit_id: null },
      ),
    );
    expect(changes).toEqual([
      { field: "Level", before: "Team", after: "Organisation" },
    ]);
  });

  test("enum values read as their labels (#2330)", () => {
    const changes = historyChanges(
      entry(
        "fields_changed",
        { methodology: "nist_800_30" },
        { methodology: "fair" },
      ),
    );
    expect(changes).toEqual([
      { field: "Methodology", before: "NIST SP 800-30", after: "FAIR" },
    ]);
  });

  test("scores and empty values read in words", () => {
    expect(
      formatHistoryValue("inherent_score", { likelihood: 3, impact: 4 }),
    ).toBe("likelihood 3 × impact 4");
    expect(formatHistoryValue("residual_score", {})).toBe("pending evaluation");
    expect(formatHistoryValue("treatment_owner", "")).toBe("none");
    expect(historyActionLabel("fields_changed")).toBe("Edited");
  });
});
