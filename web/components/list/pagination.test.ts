// Slice 246 — vitest unit coverage for the pagination math helpers.
//
// Pure-data tests for `paginationBounds` and `paginateRows` exported
// from `./pagination`. No React, no DOM — vitest config is `node` env
// per slice 069 P0-A3. Tests cover AC-6 edge cases:
//   * empty result set (0 of 0)
//   * total < pageSize (single page)
//   * exact-boundary total (totalCount === pageSize)
//   * total > pageSize (multi-page) — first page, middle page, last page
//   * out-of-range page clamps to last page
//   * out-of-range page (negative or zero) clamps to page 1

import { describe, expect, test } from "vitest";

import { paginateRows, paginationBounds } from "./pagination";

describe("paginationBounds", () => {
  test("empty result set returns zeroed bounds", () => {
    expect(paginationBounds(1, 50, 0)).toEqual({
      from: 0,
      to: 0,
      totalPages: 0,
    });
  });

  test("single-page result (total < pageSize) returns 1..total", () => {
    expect(paginationBounds(1, 50, 7)).toEqual({
      from: 1,
      to: 7,
      totalPages: 1,
    });
  });

  test("single-page result (total === pageSize) returns 1..pageSize", () => {
    expect(paginationBounds(1, 50, 50)).toEqual({
      from: 1,
      to: 50,
      totalPages: 1,
    });
  });

  test("two-page result: page 1 returns 1..pageSize", () => {
    expect(paginationBounds(1, 50, 73)).toEqual({
      from: 1,
      to: 50,
      totalPages: 2,
    });
  });

  test("two-page result: page 2 returns (pageSize+1)..total", () => {
    expect(paginationBounds(2, 50, 73)).toEqual({
      from: 51,
      to: 73,
      totalPages: 2,
    });
  });

  test("multi-page result: middle page returns correct range", () => {
    // 247 rows, pageSize 50 → 5 pages. Page 3 covers 101..150.
    expect(paginationBounds(3, 50, 247)).toEqual({
      from: 101,
      to: 150,
      totalPages: 5,
    });
  });

  test("multi-page result: last page returns (start..total)", () => {
    // 247 rows, pageSize 50 → 5 pages. Page 5 covers 201..247.
    expect(paginationBounds(5, 50, 247)).toEqual({
      from: 201,
      to: 247,
      totalPages: 5,
    });
  });

  test("out-of-range high page clamps to last page", () => {
    // Requesting page 99 of a 2-page result → clamps to page 2.
    expect(paginationBounds(99, 50, 73)).toEqual({
      from: 51,
      to: 73,
      totalPages: 2,
    });
  });

  test("out-of-range zero page clamps to page 1", () => {
    expect(paginationBounds(0, 50, 73)).toEqual({
      from: 1,
      to: 50,
      totalPages: 2,
    });
  });

  test("out-of-range negative page clamps to page 1", () => {
    expect(paginationBounds(-3, 50, 73)).toEqual({
      from: 1,
      to: 50,
      totalPages: 2,
    });
  });

  test("non-50 pageSize produces correct math (pageSize=10, total=23)", () => {
    expect(paginationBounds(1, 10, 23)).toEqual({
      from: 1,
      to: 10,
      totalPages: 3,
    });
    expect(paginationBounds(2, 10, 23)).toEqual({
      from: 11,
      to: 20,
      totalPages: 3,
    });
    expect(paginationBounds(3, 10, 23)).toEqual({
      from: 21,
      to: 23,
      totalPages: 3,
    });
  });
});

describe("paginateRows", () => {
  // Synthetic row set — strings labelled r1..r247.
  const make = (n: number): string[] =>
    Array.from({ length: n }, (_, i) => `r${i + 1}`);

  test("empty input returns empty slice", () => {
    expect(paginateRows([], 1, 50)).toEqual([]);
  });

  test("total < pageSize returns the whole array", () => {
    const rows = make(7);
    expect(paginateRows(rows, 1, 50)).toEqual(rows);
  });

  test("first page returns rows 1..pageSize", () => {
    const rows = make(247);
    const got = paginateRows(rows, 1, 50);
    expect(got).toHaveLength(50);
    expect(got[0]).toBe("r1");
    expect(got[49]).toBe("r50");
  });

  test("middle page returns the correct slice", () => {
    const rows = make(247);
    // Page 3 → indices 100..149 → rows r101..r150.
    const got = paginateRows(rows, 3, 50);
    expect(got).toHaveLength(50);
    expect(got[0]).toBe("r101");
    expect(got[49]).toBe("r150");
  });

  test("last page returns the residual slice", () => {
    const rows = make(247);
    // Page 5 → indices 200..246 → rows r201..r247 (47 rows).
    const got = paginateRows(rows, 5, 50);
    expect(got).toHaveLength(47);
    expect(got[0]).toBe("r201");
    expect(got[46]).toBe("r247");
  });

  test("out-of-range page clamps to last-page slice", () => {
    const rows = make(73);
    // Page 99 of a 2-page set → clamps to page 2 (rows 51..73 = 23 rows).
    const got = paginateRows(rows, 99, 50);
    expect(got).toHaveLength(23);
    expect(got[0]).toBe("r51");
    expect(got[22]).toBe("r73");
  });

  test("page 0 clamps to page 1 slice", () => {
    const rows = make(73);
    const got = paginateRows(rows, 0, 50);
    expect(got).toHaveLength(50);
    expect(got[0]).toBe("r1");
    expect(got[49]).toBe("r50");
  });
});
