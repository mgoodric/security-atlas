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

  test("an acceptance approval reads its expiry, approver and self-approval (#2105)", () => {
    const changes = historyChanges(
      entry(
        "acceptance_approved",
        { acceptance_status: "proposed" },
        {
          acceptance_status: "approved",
          accepted_until: "2027-06-30",
          accepter: "u-2",
          self_approved: true,
        },
      ),
    );
    expect(changes.map((c) => c.field)).toEqual([
      "Acceptance",
      "Accepted until",
      "Approved by",
      "Self-approved",
    ]);
    expect(changes[3]).toEqual({
      field: "Self-approved",
      before: "none",
      after: "yes",
    });
    expect(historyActionLabel("acceptance_approved")).toBe(
      "Acceptance approved",
    );
  });

  test("a second-person approval shows no self-approval line (#2105)", () => {
    const changes = historyChanges(
      entry("acceptance_approved", {}, { self_approved: false }),
    );
    expect(changes).toEqual([]);
  });

  test("a creation and a theme change read as their fields (#2835)", () => {
    expect(historyActionLabel("created")).toBe("Created");
    expect(historyActionLabel("themes_changed")).toBe("Themes changed");
    expect(
      historyChanges(
        entry(
          "created",
          {},
          {
            title: "New risk",
            category_id: "6a7c829b-ad4a-4472-aaac-46e95717767f",
            treatment: "untreated",
            owner_user_id: null,
            treatment_owner: "",
          },
        ),
      ).map((c) => c.field),
    ).toEqual(["Title", "Treatment"]);
    expect(
      historyChanges(
        entry(
          "themes_changed",
          { themes: ["ownership"] },
          { themes: ["monitoring", "ownership"] },
        ),
      ),
    ).toEqual([
      { field: "Themes", before: "ownership", after: "monitoring, ownership" },
    ]);
    expect(
      historyChanges(
        entry("themes_changed", { themes: [] }, { themes: ["ownership"] }),
      ),
    ).toEqual([{ field: "Themes", before: "none", after: "ownership" }]);
  });

  test("a control link and unlink read as the control (#2106)", () => {
    const control = "6a7c829b-ad4a-4472-aaac-46e95717767f";
    expect(historyActionLabel("control_linked")).toBe("Control linked");
    expect(historyActionLabel("control_unlinked")).toBe("Control unlinked");
    expect(
      historyChanges(
        entry(
          "control_unlinked",
          { control_id: control },
          { control_id: null },
        ),
      ),
    ).toEqual([
      { field: "Control", before: control, after: "none", ref: "control" },
    ]);
  });

  test("a child risk link and unlink read as the child risk (#2958)", () => {
    const child = "0b1f5a3e-3c55-4b8e-9d0e-2b6f8a9c1d42";
    expect(historyActionLabel("child_linked")).toBe("Child risk linked");
    expect(historyActionLabel("child_unlinked")).toBe("Child risk unlinked");
    expect(
      historyChanges(
        entry(
          "child_linked",
          { child_risk_id: null },
          { child_risk_id: child },
        ),
      ),
    ).toEqual([
      { field: "Child risk", before: "none", after: child, ref: "risk" },
    ]);
    expect(
      historyChanges(
        entry(
          "child_unlinked",
          { child_risk_id: child },
          { child_risk_id: null },
        ),
      ),
    ).toEqual([
      { field: "Child risk", before: child, after: "none", ref: "risk" },
    ]);
  });

  test("id-valued fields carry the kind of thing they name (#2138)", () => {
    const changes = historyChanges(
      entry(
        "acceptance_approved",
        { owner_user_id: "u-1", org_unit_id: null },
        { owner_user_id: "u-2", org_unit_id: "ou-1", accepter: "u-3" },
      ),
    );
    expect(changes.map((c) => [c.field, c.ref])).toEqual([
      ["Owner", "person"],
      ["Org unit", "org_unit"],
      ["Approved by", "person"],
    ]);
    expect(historyActionLabel("some_new_action")).toBe("Some new action");
  });

  test("a close and a reopen read their status and closure (#2107)", () => {
    expect(historyActionLabel("closed")).toBe("Closed");
    expect(historyActionLabel("reopened")).toBe("Reopened");
    expect(
      historyChanges({
        id: "e1",
        actor: "u1",
        action: "closed",
        before: { status: "open" },
        after: {
          status: "closed",
          closure_reason: "merged",
          closure_note: "Covered by the vendor risk.",
        },
        created_at: "2026-11-15T00:00:00Z",
      }),
    ).toEqual([
      { field: "Status", before: "Open", after: "Closed" },
      {
        field: "Closure reason",
        before: "none",
        after: "Merged into another risk",
      },
      {
        field: "Closure note",
        before: "none",
        after: "Covered by the vendor risk.",
      },
    ]);
  });
});
