// #2130 — the dashboard's "Coming up" reads the compliance calendar for
// the next 14 days. These pin the window the panel asks for.

import { describe, expect, test } from "vitest";

import { COMING_UP_DAYS, comingUpWindow } from "./dashboard";

describe("comingUpWindow (#2130)", () => {
  test("is today (UTC) through 13 days later, inclusive: 14 days", () => {
    expect(COMING_UP_DAYS).toBe(14);
    expect(comingUpWindow(new Date("2026-11-14T10:00:00Z"))).toEqual({
      from: "2026-11-14",
      to: "2026-11-27",
    });
  });

  test("uses the UTC date, not the local one, near midnight", () => {
    expect(comingUpWindow(new Date("2026-11-14T23:59:59Z")).from).toBe(
      "2026-11-14",
    );
    expect(comingUpWindow(new Date("2026-11-15T00:00:00Z")).from).toBe(
      "2026-11-15",
    );
  });

  test("crosses a month and a year boundary", () => {
    expect(comingUpWindow(new Date("2026-12-25T12:00:00Z"))).toEqual({
      from: "2026-12-25",
      to: "2027-01-07",
    });
  });
});
