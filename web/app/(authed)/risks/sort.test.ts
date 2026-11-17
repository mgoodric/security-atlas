// #2132 — the register's sort is the platform's `?sort=` parameter.

import { describe, expect, test } from "vitest";

import {
  ariaSort,
  DEFAULT_SORT,
  nextSortState,
  parseSortState,
  SORT_KEYS,
  sortParam,
} from "./sort";

describe("parseSortState", () => {
  test("absent is the default: residual, highest first", () => {
    expect(parseSortState(null)).toEqual(DEFAULT_SORT);
    expect(parseSortState("")).toEqual({ key: "residual", dir: "desc" });
  });

  test("key is ascending, -key descending", () => {
    expect(parseSortState("title")).toEqual({ key: "title", dir: "asc" });
    expect(parseSortState("-review_due")).toEqual({
      key: "review_due",
      dir: "desc",
    });
  });

  test("an unknown key or the old key:dir shape is the default", () => {
    for (const raw of ["bogus", "severity:asc", "--title", "residual,age"]) {
      expect(parseSortState(raw)).toEqual(DEFAULT_SORT);
    }
  });

  test("every key round-trips through sortParam", () => {
    for (const key of SORT_KEYS) {
      for (const dir of ["asc", "desc"] as const) {
        const p = sortParam({ key, dir });
        if (p === "") {
          expect({ key, dir }).toEqual(DEFAULT_SORT);
        } else {
          expect(parseSortState(p)).toEqual({ key, dir });
        }
      }
    }
  });
});

describe("sortParam", () => {
  test("the default sends nothing", () => {
    expect(sortParam(DEFAULT_SORT)).toBe("");
  });

  test("ascending residual is sent", () => {
    expect(sortParam({ key: "residual", dir: "asc" })).toBe("residual");
  });
});

describe("nextSortState / ariaSort", () => {
  test("a new column sorts descending first", () => {
    expect(nextSortState(DEFAULT_SORT, "owner")).toEqual({
      key: "owner",
      dir: "desc",
    });
  });

  test("the active column flips", () => {
    expect(nextSortState(DEFAULT_SORT, "residual")).toEqual({
      key: "residual",
      dir: "asc",
    });
    expect(nextSortState({ key: "title", dir: "asc" }, "title")).toEqual({
      key: "title",
      dir: "desc",
    });
  });

  test("aria-sort names the active column's direction only", () => {
    expect(ariaSort(DEFAULT_SORT, "residual")).toBe("descending");
    expect(ariaSort({ key: "title", dir: "asc" }, "title")).toBe("ascending");
    expect(ariaSort(DEFAULT_SORT, "title")).toBe("none");
  });
});
