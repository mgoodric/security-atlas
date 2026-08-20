// OE-664 — unit tests for the /personnel-security pure presentation
// helpers. The prominence contract (overdue offboarding sorts first and
// carries the rose "Overdue offboarding" badge) is the load-bearing
// logic on this surface, so it is pinned here without rendering.

import { describe, expect, it } from "vitest";

import type { Checklist } from "@/lib/api/personnel-security";

import {
  checklistRank,
  dateLabel,
  isOverdue,
  itemProgress,
  kindLabel,
  sortChecklists,
  statusBadgeClass,
  statusBadgeLabel,
} from "./status";

const NOW = new Date("2026-08-03T12:00:00Z");

function checklist(over: Partial<Checklist>): Checklist {
  return {
    id: "c1",
    kind: "onboarding",
    source: "manual",
    source_event_id: "",
    person_external_id: "p1",
    person_work_email: "",
    person_display_name: "",
    due_at: "2026-09-01T00:00:00Z",
    status: "open",
    items: [],
    ...over,
  };
}

describe("isOverdue", () => {
  it("is true for an open checklist past its due date", () => {
    expect(
      isOverdue({ status: "open", due_at: "2026-08-01T00:00:00Z" }, NOW),
    ).toBe(true);
  });

  it("is false for an open checklist not yet due", () => {
    expect(
      isOverdue({ status: "open", due_at: "2026-09-01T00:00:00Z" }, NOW),
    ).toBe(false);
  });

  it("is false for a completed checklist regardless of due date", () => {
    expect(
      isOverdue({ status: "completed", due_at: "2026-08-01T00:00:00Z" }, NOW),
    ).toBe(false);
  });

  it("is false for a malformed due date", () => {
    expect(isOverdue({ status: "open", due_at: "not-a-date" }, NOW)).toBe(
      false,
    );
  });
});

describe("checklistRank / sortChecklists", () => {
  it("ranks overdue offboarding above everything else", () => {
    const overdueOff = checklist({
      id: "off",
      kind: "offboarding",
      due_at: "2026-08-01T00:00:00Z",
    });
    const overdueOn = checklist({
      id: "on",
      kind: "onboarding",
      due_at: "2026-07-01T00:00:00Z",
    });
    const openFuture = checklist({ id: "fut", due_at: "2026-09-01T00:00:00Z" });
    const done = checklist({
      id: "done",
      status: "completed",
      due_at: "2026-07-01T00:00:00Z",
    });

    expect(checklistRank(overdueOff, NOW)).toBe(0);
    expect(checklistRank(overdueOn, NOW)).toBe(1);
    expect(checklistRank(openFuture, NOW)).toBe(2);
    expect(checklistRank(done, NOW)).toBe(3);

    const sorted = sortChecklists(
      [done, openFuture, overdueOn, overdueOff],
      NOW,
    );
    expect(sorted.map((c) => c.id)).toEqual(["off", "on", "fut", "done"]);
  });

  it("orders within a rank by due date ascending (most overdue first)", () => {
    const older = checklist({
      id: "older",
      kind: "offboarding",
      due_at: "2026-07-01T00:00:00Z",
    });
    const newer = checklist({
      id: "newer",
      kind: "offboarding",
      due_at: "2026-08-01T00:00:00Z",
    });
    const sorted = sortChecklists([newer, older], NOW);
    expect(sorted.map((c) => c.id)).toEqual(["older", "newer"]);
  });

  it("does not mutate its input", () => {
    const rows = [
      checklist({ id: "a", status: "completed" }),
      checklist({
        id: "b",
        kind: "offboarding",
        due_at: "2026-08-01T00:00:00Z",
      }),
    ];
    sortChecklists(rows, NOW);
    expect(rows.map((c) => c.id)).toEqual(["a", "b"]);
  });
});

describe("statusBadgeLabel / statusBadgeClass", () => {
  it("labels overdue offboarding explicitly and styles it rose", () => {
    const c = checklist({
      kind: "offboarding",
      due_at: "2026-08-01T00:00:00Z",
    });
    expect(statusBadgeLabel(c, NOW)).toBe("Overdue offboarding");
    expect(statusBadgeClass(c, NOW)).toContain("rose");
  });

  it("labels overdue onboarding Overdue with amber styling", () => {
    const c = checklist({ kind: "onboarding", due_at: "2026-08-01T00:00:00Z" });
    expect(statusBadgeLabel(c, NOW)).toBe("Overdue");
    expect(statusBadgeClass(c, NOW)).toContain("amber");
  });

  it("labels open-not-due Open and completed Completed", () => {
    const open = checklist({ due_at: "2026-09-01T00:00:00Z" });
    expect(statusBadgeLabel(open, NOW)).toBe("Open");
    const done = checklist({ status: "completed" });
    expect(statusBadgeLabel(done, NOW)).toBe("Completed");
    expect(statusBadgeClass(done, NOW)).toContain("emerald");
  });
});

describe("labels", () => {
  it("kindLabel maps both kinds and falls back to the raw value", () => {
    expect(kindLabel("onboarding")).toBe("Onboarding");
    expect(kindLabel("offboarding")).toBe("Offboarding");
    expect(kindLabel("other")).toBe("other");
  });

  it("dateLabel formats ISO timestamps and dashes empties", () => {
    expect(dateLabel("2026-08-01T00:00:00Z")).toBe("2026-08-01");
    expect(dateLabel("")).toBe("—");
    expect(dateLabel(undefined)).toBe("—");
    expect(dateLabel("garbage")).toBe("garbage");
  });

  it("itemProgress counts completed items", () => {
    expect(
      itemProgress([{ completed_at: "2026-08-01T00:00:00Z" }, {}, {}]),
    ).toBe("1 of 3 items complete");
    expect(itemProgress([{ completed_at: "2026-08-01T00:00:00Z" }])).toBe(
      "1 of 1 item complete",
    );
  });
});
