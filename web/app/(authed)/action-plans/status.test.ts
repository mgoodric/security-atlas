// Slice 384 — unit tests for the /action-plans status presentation helpers.

import { describe, expect, it } from "vitest";

import { planStatusLabel, statusLabel, statusPillClass } from "./status";

describe("statusLabel", () => {
  it("maps known statuses to human labels", () => {
    expect(statusLabel("draft")).toBe("Draft");
    expect(statusLabel("in_progress")).toBe("In progress");
    expect(statusLabel("blocked")).toBe("Blocked");
    expect(statusLabel("completed")).toBe("Completed");
    expect(statusLabel("verified")).toBe("Verified");
  });
  it("falls back to the raw value for unknown statuses", () => {
    expect(statusLabel("bogus")).toBe("bogus");
    expect(statusLabel("constructor")).toBe("constructor");
  });
});

describe("planStatusLabel", () => {
  it("says a completed plan's linked controls still fail (#2109)", () => {
    expect(planStatusLabel("completed", true)).toBe(
      "Completed · linked controls still failing",
    );
  });
  it("is the plain label otherwise", () => {
    expect(planStatusLabel("completed", false)).toBe("Completed");
    expect(planStatusLabel("completed")).toBe("Completed");
    expect(planStatusLabel("verified", true)).toBe("Verified");
    expect(planStatusLabel("in_progress", true)).toBe("In progress");
  });
});

describe("statusPillClass", () => {
  it("returns a distinct class per status", () => {
    const classes = new Set(
      ["draft", "in_progress", "blocked", "completed", "verified"].map(
        statusPillClass,
      ),
    );
    expect(classes.size).toBe(5);
  });
  it("returns a muted fallback for unknown statuses", () => {
    expect(statusPillClass("bogus")).toContain("muted");
  });
});
