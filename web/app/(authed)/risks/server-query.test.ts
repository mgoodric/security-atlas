// #2132 — the register's request is built from the URL: filters checked,
// defaults left out, the sort and the page appended.

import { describe, expect, test } from "vitest";

import {
  ALL,
  clampedPage,
  DEFAULT_FILTERS,
  filtersFromParams,
  isDefaultFilters,
  normalizeFilterValue,
  parseColumns,
  registerQuery,
  shortRiskId,
  toggleColumn,
} from "./server-query";

const OWNER = "0b6f3a1e-2c4d-4e5f-8a9b-0c1d2e3f4a5b";

function params(q: string) {
  return new URLSearchParams(q);
}

describe("normalizeFilterValue", () => {
  test("allow-listed values pass", () => {
    expect(normalizeFilterValue("status", "closed")).toBe("closed");
    expect(normalizeFilterValue("status", ALL)).toBe(ALL);
    expect(normalizeFilterValue("owner", "none")).toBe("none");
    expect(normalizeFilterValue("owner", OWNER)).toBe(OWNER);
    expect(normalizeFilterValue("treatment", "avoid")).toBe("avoid");
    expect(normalizeFilterValue("methodology", "fair")).toBe("fair");
    expect(normalizeFilterValue("review_due", "due_soon")).toBe("due_soon");
    expect(normalizeFilterValue("category", "privacy")).toBe("privacy");
    expect(normalizeFilterValue("review_due_from", "2026-11-30")).toBe(
      "2026-11-30",
    );
  });

  test("a value the platform would refuse reads as the default", () => {
    expect(normalizeFilterValue("status", "archived")).toBe("open");
    expect(normalizeFilterValue("owner", "alice")).toBe(ALL);
    expect(normalizeFilterValue("treatment", "ignore")).toBe(ALL);
    expect(normalizeFilterValue("review_due", "soon")).toBe(ALL);
    expect(normalizeFilterValue("category", "Not A Slug")).toBe(ALL);
    expect(normalizeFilterValue("org_unit", "platform")).toBe(ALL);
    expect(normalizeFilterValue("review_due_to", "2026-02-30")).toBe(ALL);
    expect(normalizeFilterValue("review_due_to", "tomorrow")).toBe(ALL);
  });
});

describe("filtersFromParams / isDefaultFilters", () => {
  test("no params is the default: open risks, nothing else", () => {
    const f = filtersFromParams(params(""));
    expect(f).toEqual(DEFAULT_FILTERS);
    expect(f.status).toBe("open");
    expect(isDefaultFilters(f)).toBe(true);
  });

  test("a filter in the URL is read", () => {
    const f = filtersFromParams(params(`status=closed&owner=${OWNER}`));
    expect(f.status).toBe("closed");
    expect(f.owner).toBe(OWNER);
    expect(isDefaultFilters(f)).toBe(false);
  });
});

describe("registerQuery", () => {
  test("the default page sends only the page", () => {
    expect(
      registerQuery({
        filters: DEFAULT_FILTERS,
        sort: null,
        rollups: false,
        page: 1,
        pageSize: 50,
      }),
    ).toEqual({ limit: "50", offset: "0" });
  });

  test("filters, sort, roll-ups and page are sent", () => {
    const q = registerQuery({
      filters: {
        ...DEFAULT_FILTERS,
        status: ALL,
        owner: "none",
        treatment: "mitigate",
        category: "privacy",
        review_due: "overdue",
        review_due_from: "2026-01-01",
      },
      sort: "-title",
      rollups: true,
      page: 3,
      pageSize: 50,
    });
    expect(q).toEqual({
      status: ALL,
      owner: "none",
      treatment: "mitigate",
      category: "privacy",
      review_due: "overdue",
      review_due_from: "2026-01-01",
      sort: "-title",
      include_rollups: "true",
      limit: "50",
      offset: "100",
    });
  });

  test("an unknown sort sends the default (no sort)", () => {
    const q = registerQuery({
      filters: DEFAULT_FILTERS,
      sort: "severity:asc",
      rollups: false,
      page: 1,
      pageSize: 50,
    });
    expect(q.sort).toBeUndefined();
  });
});

describe("clampedPage", () => {
  test("a page past the end clamps to the last", () => {
    expect(clampedPage(5, 50, 120)).toBe(3);
    expect(clampedPage(2, 50, 0)).toBe(1);
  });

  test("a page in range is left alone", () => {
    expect(clampedPage(3, 50, 120)).toBeNull();
  });
});

describe("columns", () => {
  test("parseColumns keeps known columns in chooser order", () => {
    expect(parseColumns("themes,bogus,methodology")).toEqual([
      "methodology",
      "themes",
    ]);
    expect(parseColumns(null)).toEqual([]);
  });

  test("toggleColumn adds and removes", () => {
    expect(toggleColumn([], "level")).toBe("level");
    expect(toggleColumn(["level"], "methodology")).toBe("methodology,level");
    expect(toggleColumn(["methodology", "level"], "methodology")).toBe("level");
    expect(toggleColumn(["level"], "level")).toBe("");
  });

  test("shortRiskId is the first eight hex digits", () => {
    expect(shortRiskId(OWNER)).toBe("0b6f3a1e");
  });
});
