// Slice 384 — unit tests for the action-plan create-form validators.

import { describe, expect, it } from "vitest";

import {
  hasErrors,
  validateActionPlanForm,
  type ActionPlanFormValues,
} from "./validate";

const OWNER = "11111111-1111-1111-1111-111111111111";

function base(): ActionPlanFormValues {
  return {
    title: "Close the IAC-06 freshness gap",
    description: "",
    triggeringEvent: "",
    ownerId: OWNER,
    dueDate: "",
    riskIds: [],
    controlIds: [],
  };
}

describe("validateActionPlanForm", () => {
  it("accepts a minimal valid form", () => {
    expect(hasErrors(validateActionPlanForm(base()))).toBe(false);
  });

  it("requires a title", () => {
    const e = validateActionPlanForm({ ...base(), title: "   " });
    expect(e.title).toBeDefined();
  });

  it("rejects a title over 200 chars", () => {
    const e = validateActionPlanForm({ ...base(), title: "x".repeat(201) });
    expect(e.title).toContain("200");
  });

  it("rejects a description over 4000 chars", () => {
    const e = validateActionPlanForm({
      ...base(),
      description: "x".repeat(4001),
    });
    expect(e.description).toContain("4000");
  });

  it("rejects a triggering event over 500 chars", () => {
    const e = validateActionPlanForm({
      ...base(),
      triggeringEvent: "x".repeat(501),
    });
    expect(e.triggeringEvent).toContain("500");
  });

  it("requires a UUID owner", () => {
    expect(
      validateActionPlanForm({ ...base(), ownerId: "" }).ownerId,
    ).toBeDefined();
    expect(
      validateActionPlanForm({ ...base(), ownerId: "not-a-uuid" }).ownerId,
    ).toContain("UUID");
  });

  it("rejects a due date more than 5 years out", () => {
    const far = new Date();
    far.setFullYear(far.getFullYear() + 6);
    const e = validateActionPlanForm({
      ...base(),
      dueDate: far.toISOString().slice(0, 10),
    });
    expect(e.dueDate).toContain("5 years");
  });

  it("accepts a due date within 5 years", () => {
    const soon = new Date();
    soon.setFullYear(soon.getFullYear() + 1);
    const e = validateActionPlanForm({
      ...base(),
      dueDate: soon.toISOString().slice(0, 10),
    });
    expect(e.dueDate).toBeUndefined();
  });

  it("rejects over-cap link selections", () => {
    const ids = Array.from({ length: 51 }, (_, i) => `id-${i}`);
    const e = validateActionPlanForm({ ...base(), riskIds: ids });
    expect(e.links).toBeDefined();
  });
});
