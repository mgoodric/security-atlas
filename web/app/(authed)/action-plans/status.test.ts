// Slice 384 — unit tests for the /action-plans status presentation helpers.

import { describe, expect, it } from "vitest";

import { dateLabel, statusLabel, statusPillClass } from "./status";

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

describe("dateLabel", () => {
  it("formats an ISO timestamp to YYYY-MM-DD", () => {
    expect(dateLabel("2026-12-01T10:30:00Z")).toBe("2026-12-01");
  });
  it("formats a bare date", () => {
    expect(dateLabel("2026-06-14")).toBe("2026-06-14");
  });
  it("renders an em dash for empty input", () => {
    expect(dateLabel()).toBe("—");
    expect(dateLabel(null)).toBe("—");
    expect(dateLabel("")).toBe("—");
  });
  it("returns the raw string for a malformed date", () => {
    expect(dateLabel("not-a-date")).toBe("not-a-date");
  });
});
