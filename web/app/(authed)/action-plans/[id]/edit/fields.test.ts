import { describe, expect, it } from "vitest";

import type { ActionPlan } from "@/lib/api/action-plans";

import { changedFields, dayOf } from "./fields";

const plan: ActionPlan = {
  id: "d4d4d4d4-0000-4000-8000-000000002108",
  title: "Plan",
  description: "Body",
  triggering_event: "",
  owner_id: "a1a1a1a1-0000-4000-8000-000000002108",
  due_date: "2027-01-15",
  status: "draft",
  created_at: "2026-11-16T00:00:00Z",
  updated_at: "2026-11-16T00:00:00Z",
};

const same = {
  title: plan.title,
  description: plan.description,
  ownerId: plan.owner_id,
  dueDate: "2027-01-15",
};

describe("changedFields", () => {
  it("sends nothing when nothing changed", () => {
    expect(changedFields(plan, same)).toEqual({});
  });

  it("sends only the changed fields, trimmed", () => {
    expect(
      changedFields(plan, {
        ...same,
        title: "  New title ",
        ownerId: "b2b2b2b2-0000-4000-8000-000000002108",
      }),
    ).toEqual({
      title: "New title",
      owner_id: "b2b2b2b2-0000-4000-8000-000000002108",
    });
  });

  it("sends a due date as an RFC 3339 timestamp, and clears an emptied one", () => {
    expect(changedFields(plan, { ...same, dueDate: "2027-02-01" })).toEqual({
      due_date: "2027-02-01T00:00:00Z",
    });
    expect(changedFields(plan, { ...same, dueDate: "" })).toEqual({
      clear_due_date: true,
    });
  });
});

describe("dayOf", () => {
  it("reads a date or a timestamp as its day", () => {
    expect(dayOf("2027-01-15")).toBe("2027-01-15");
    expect(dayOf("2027-01-15T00:00:00Z")).toBe("2027-01-15");
    expect(dayOf(undefined)).toBe("");
  });
});
