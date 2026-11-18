// #2131 — the pure half of the server-side /controls list: value checks,
// the column-to-sort-key map, the header sort cycle, and the query each
// page sends.

import { readFileSync } from "node:fs";
import { join } from "node:path";

import { describe, expect, test } from "vitest";

import { enumOptions } from "@/lib/labels/enum-labels";

import { ALL, DEFAULT_FILTERS, setFilter } from "./filters";
import {
  ariaSort,
  clampedPage,
  FRESHNESS_VALUES,
  myControlsQuery,
  nextSort,
  normalizeFilterValue,
  pageOffset,
  parseSort,
  SORT_KEY_BY_COLUMN,
} from "./server-query";

const CELL = "00000000-0000-4000-8000-000000002131";

// #2359: the freshness_status CHECK values, recorded from the migrated
// database by internal/db/check_enums_integration_test.go.
const BACKEND_FRESHNESS = (
  JSON.parse(
    readFileSync(
      join(
        __dirname,
        "..",
        "..",
        "..",
        "lib",
        "contracts",
        "check-enums.golden.json",
      ),
      "utf8",
    ),
  ) as { enums: Record<string, string[]> }
).enums.FreshnessState;

describe("freshness filter values (#2359)", () => {
  test("the golden records the backend's freshness states", () => {
    expect(BACKEND_FRESHNESS).toEqual(
      expect.arrayContaining(["fresh", "stale", "no_evidence"]),
    );
  });

  test("every Freshness option the page offers is a backend state", () => {
    // page.tsx builds FRESHNESS_OPTIONS from enumOptions("FreshnessState").
    const offered = enumOptions("FreshnessState").map((o) => o.value);
    expect([...offered].sort()).toEqual([...BACKEND_FRESHNESS].sort());
  });

  test("the query accepts exactly the backend states", () => {
    expect([...FRESHNESS_VALUES].sort()).toEqual([...BACKEND_FRESHNESS].sort());
    for (const v of BACKEND_FRESHNESS) {
      expect(normalizeFilterValue("freshness", v)).toBe(v);
    }
  });
});

describe("parseSort / nextSort / ariaSort", () => {
  test("reads key and -key, and nothing else", () => {
    expect(parseSort("name")).toEqual({ key: "name", desc: false });
    expect(parseSort("-last_observed_at")).toEqual({
      key: "last_observed_at",
      desc: true,
    });
    expect(parseSort(ALL)).toBeNull();
    expect(parseSort("")).toBeNull();
    expect(parseSort("title")).toBeNull();
    expect(parseSort("--name")).toBeNull();
  });

  test("a header click cycles ascending, descending, default", () => {
    expect(nextSort(ALL, "state")).toBe("state");
    expect(nextSort("state", "state")).toBe("-state");
    expect(nextSort("-state", "state")).toBe(ALL);
    // Another column starts ascending.
    expect(nextSort("-state", "name")).toBe("name");
  });

  test("aria-sort names the sorted column only", () => {
    expect(ariaSort("-owner", "owner")).toBe("descending");
    expect(ariaSort("owner", "owner")).toBe("ascending");
    expect(ariaSort("owner", "name")).toBe("none");
    expect(ariaSort(ALL, "name")).toBe("none");
  });

  test("every displayed data column maps to a platform sort key", () => {
    expect(Object.keys(SORT_KEY_BY_COLUMN).sort()).toEqual(
      [
        "family",
        "freshness_status",
        "frameworks",
        "last_observed_at",
        "name",
        "owner",
        "result",
        "scf_id",
      ].sort(),
    );
    expect(SORT_KEY_BY_COLUMN.result).toBe("state");
    expect(SORT_KEY_BY_COLUMN.freshness_status).toBe("freshness");
  });
});

describe("normalizeFilterValue", () => {
  test.each([
    ["result", "fail", "fail"],
    ["result", "insufficient_evidence", "inconclusive"],
    ["result", "not_applicable", "na"],
    ["result", "bogus", ALL],
    ["freshness", "no_evidence", "no_evidence"],
    ["freshness", "old", ALL],
    // #2359: the filter once offered "expired"; an old link shows all.
    ["freshness", "expired", ALL],
    ["anchor_fit", "partial", "partial"],
    ["anchor_fit", "loose", ALL],
    ["scope", CELL, CELL],
    ["scope", "cell-1", ALL],
    ["owner", "none", "none"],
    ["owner", CELL, CELL],
    ["owner", "me", ALL],
    ["sort", "-frameworks", "-frameworks"],
    ["sort", "title", ALL],
    ["family", "Identity & Access", "Identity & Access"],
    ["framework", "soc2", "soc2"],
  ] as const)("%s=%s reads as %s", (key, raw, want) => {
    expect(normalizeFilterValue(key, raw)).toBe(want);
  });
});

describe("myControlsQuery", () => {
  test("the default query is the first page in the default order", () => {
    expect(myControlsQuery(DEFAULT_FILTERS, 1, 50)).toEqual({
      limit: "50",
      offset: "0",
    });
  });

  test("sends every active filter, the sort and the page offset", () => {
    let f = setFilter(DEFAULT_FILTERS, "result", "fail");
    f = setFilter(f, "owner", "none");
    f = setFilter(f, "sort", "-last_observed_at");
    f = setFilter(f, "family", "IAC");
    f = setFilter(f, "scope", CELL);
    expect(myControlsQuery(f, 3, 50)).toEqual({
      result: "fail",
      owner: "none",
      sort: "-last_observed_at",
      family: "IAC",
      scope: CELL,
      limit: "50",
      offset: "100",
    });
  });

  test("drops values the platform would refuse", () => {
    const f = setFilter(DEFAULT_FILTERS, "sort", "title");
    expect(myControlsQuery(f, 1, 50)).toEqual({ limit: "50", offset: "0" });
  });
});

describe("paging", () => {
  test("pageOffset is 0-based from a 1-indexed page", () => {
    expect(pageOffset(1, 50)).toBe(0);
    expect(pageOffset(2, 50)).toBe(50);
    expect(pageOffset(0, 50)).toBe(0);
  });

  test("clampedPage moves a page past the end to the last page", () => {
    expect(clampedPage(5, 50, 120)).toBe(3);
    expect(clampedPage(3, 50, 120)).toBeNull();
    expect(clampedPage(2, 50, 0)).toBe(1);
    expect(clampedPage(1, 50, 0)).toBeNull();
  });
});
