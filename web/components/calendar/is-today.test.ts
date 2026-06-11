// Slice 668 — unit test for the calendar `isToday` / `isSameLocalDay`
// helpers.
//
// AC-3 coverage: the today match tracks an INJECTED reference date (never
// an inline clock read), so the assertions are deterministic. We pin a
// fixed reference and assert the matching cell is "today" and adjacent /
// same-day-number-different-month cells are not.
//
// Dates are constructed with the local-time `new Date(y, mIdx, d, ...)`
// constructor on purpose — the grid keys cells on LOCAL calendar dates, so
// the comparison is local-time and these tests stay timezone-agnostic
// (they never cross a day boundary via a UTC slice).

import { describe, expect, it } from "vitest";

import { isSameLocalDay, isToday } from "./is-today";

describe("isSameLocalDay", () => {
  it("true for two times on the same local calendar day", () => {
    const morning = new Date(2026, 5, 10, 8, 30, 0);
    const evening = new Date(2026, 5, 10, 23, 59, 59);
    expect(isSameLocalDay(morning, evening)).toBe(true);
  });

  it("false for adjacent days", () => {
    const d10 = new Date(2026, 5, 10, 12, 0, 0);
    const d11 = new Date(2026, 5, 11, 0, 0, 0);
    expect(isSameLocalDay(d10, d11)).toBe(false);
  });

  it("false for the same day-of-month in a different month", () => {
    const june10 = new Date(2026, 5, 10);
    const july10 = new Date(2026, 6, 10);
    expect(isSameLocalDay(june10, july10)).toBe(false);
  });

  it("false for the same month/day in a different year", () => {
    const y2026 = new Date(2026, 5, 10);
    const y2027 = new Date(2027, 5, 10);
    expect(isSameLocalDay(y2026, y2027)).toBe(false);
  });
});

describe("isToday", () => {
  // A fixed, injected reference "now" — deterministic regardless of when
  // the suite runs (AC-3).
  const reference = new Date(2026, 5, 10, 9, 0, 0); // 2026-06-10 09:00 local

  it("true when the cell is the reference day", () => {
    const cell = new Date(2026, 5, 10);
    expect(isToday(cell, reference)).toBe(true);
  });

  it("true regardless of the cell's time-of-day", () => {
    const cellMidnight = new Date(2026, 5, 10, 0, 0, 0);
    const cellLate = new Date(2026, 5, 10, 23, 30, 0);
    expect(isToday(cellMidnight, reference)).toBe(true);
    expect(isToday(cellLate, reference)).toBe(true);
  });

  it("false for the day before and the day after the reference", () => {
    expect(isToday(new Date(2026, 5, 9), reference)).toBe(false);
    expect(isToday(new Date(2026, 5, 11), reference)).toBe(false);
  });

  it("false for a leading/trailing grid cell that shares the day number but spills into an adjacent month", () => {
    // e.g. a June grid showing a trailing July 10 cell must NOT light up
    // when the reference is June 10.
    const julyTrailingCell = new Date(2026, 6, 10);
    expect(isToday(julyTrailingCell, reference)).toBe(false);
  });
});
