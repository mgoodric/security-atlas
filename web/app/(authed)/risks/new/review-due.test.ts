import { describe, expect, test } from "vitest";

import { defaultReviewDueDate, reviewDueToSend } from "./review-due";

describe("defaultReviewDueDate (#2059)", () => {
  test("adds the cadence in UTC calendar days", () => {
    const now = new Date("2026-11-14T23:30:00Z");
    expect(defaultReviewDueDate(365, now)).toBe("2027-11-14");
    expect(defaultReviewDueDate(90, now)).toBe("2027-02-12");
  });

  test("crosses a leap day", () => {
    expect(defaultReviewDueDate(1, new Date("2028-02-28T12:00:00Z"))).toBe(
      "2028-02-29",
    );
  });
});

describe("reviewDueToSend (#2059)", () => {
  test("an untouched or cleared date is not sent", () => {
    expect(reviewDueToSend("", "2027-11-14")).toBeUndefined();
    expect(reviewDueToSend("2027-11-14", "2027-11-14")).toBeUndefined();
  });

  test("a changed date is sent as midnight UTC", () => {
    expect(reviewDueToSend("2027-03-01", "2027-11-14")).toBe(
      "2027-03-01T00:00:00Z",
    );
  });
});
