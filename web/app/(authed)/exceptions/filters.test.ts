// Slice 177 — vitest coverage for the /exceptions filter logic.

import { describe, expect, test } from "vitest";

import type { Exception } from "@/lib/api/exceptions";

import {
  ALL,
  applyFilters,
  clearFilters,
  DEFAULT_FILTERS,
  isDefault,
  setFilter,
  toFetchOptions,
  uniqueControlIDs,
} from "./filters";

function mkRow(over: Partial<Exception>): Exception {
  return {
    id: "11111111-1111-1111-1111-111111111111",
    control_id: "22222222-2222-2222-2222-222222222222",
    scope_cell_predicate: {},
    justification: "j",
    compensating_controls: [],
    requested_by: "alice",
    requested_at: "2026-05-01T00:00:00Z",
    expires_at: "2026-08-01T00:00:00Z",
    status: "active",
    created_at: "2026-05-01T00:00:00Z",
    updated_at: "2026-05-01T00:00:00Z",
    ...over,
  };
}

describe("DEFAULT_FILTERS / isDefault", () => {
  test("default has ALL on every key", () => {
    expect(DEFAULT_FILTERS.status).toBe(ALL);
    expect(DEFAULT_FILTERS.control_id).toBe(ALL);
  });

  test("isDefault is true for default", () => {
    expect(isDefault(DEFAULT_FILTERS)).toBe(true);
  });

  test("isDefault is false when status narrowed", () => {
    expect(isDefault({ ...DEFAULT_FILTERS, status: "active" })).toBe(false);
  });

  test("isDefault is false when control_id narrowed", () => {
    expect(isDefault({ ...DEFAULT_FILTERS, control_id: "ctrl-1" })).toBe(false);
  });
});

describe("applyFilters", () => {
  const rows: Exception[] = [
    mkRow({ id: "a", status: "requested", control_id: "c1" }),
    mkRow({ id: "b", status: "approved", control_id: "c1" }),
    mkRow({ id: "c", status: "active", control_id: "c2" }),
    mkRow({ id: "d", status: "expired", control_id: "c2" }),
    mkRow({ id: "e", status: "denied", control_id: "c3" }),
  ];

  test("default filter returns every row", () => {
    expect(applyFilters(rows, DEFAULT_FILTERS).map((r) => r.id)).toEqual([
      "a",
      "b",
      "c",
      "d",
      "e",
    ]);
  });

  test("status narrow", () => {
    expect(
      applyFilters(rows, { ...DEFAULT_FILTERS, status: "active" }).map(
        (r) => r.id,
      ),
    ).toEqual(["c"]);
  });

  test("control_id narrow", () => {
    expect(
      applyFilters(rows, { ...DEFAULT_FILTERS, control_id: "c2" }).map(
        (r) => r.id,
      ),
    ).toEqual(["c", "d"]);
  });

  test("both narrow", () => {
    expect(
      applyFilters(rows, {
        status: "active",
        control_id: "c2",
      }).map((r) => r.id),
    ).toEqual(["c"]);
  });

  test("filter that matches nothing yields empty result", () => {
    expect(
      applyFilters(rows, {
        ...DEFAULT_FILTERS,
        status: "active",
        control_id: "c3",
      }),
    ).toEqual([]);
  });
});

describe("uniqueControlIDs", () => {
  test("dedupe + sort", () => {
    const rows: Exception[] = [
      mkRow({ control_id: "ctrl-zeta" }),
      mkRow({ control_id: "ctrl-alpha" }),
      mkRow({ control_id: "ctrl-zeta" }),
      mkRow({ control_id: "ctrl-mu" }),
    ];
    expect(uniqueControlIDs(rows)).toEqual([
      "ctrl-alpha",
      "ctrl-mu",
      "ctrl-zeta",
    ]);
  });

  test("empty input → empty output", () => {
    expect(uniqueControlIDs([])).toEqual([]);
  });
});

describe("setFilter / clearFilters", () => {
  test("setFilter returns a new object", () => {
    const next = setFilter(DEFAULT_FILTERS, "status", "active");
    expect(next).not.toBe(DEFAULT_FILTERS);
    expect(next.status).toBe("active");
    expect(next.control_id).toBe(ALL);
  });

  test("clearFilters returns the default", () => {
    const cleared = clearFilters();
    expect(cleared).toEqual(DEFAULT_FILTERS);
    expect(cleared).not.toBe(DEFAULT_FILTERS);
  });
});

describe("toFetchOptions", () => {
  test("default → empty object (BFF treats absent params as no filter)", () => {
    expect(toFetchOptions(DEFAULT_FILTERS)).toEqual({});
  });

  test("status concrete → status key set", () => {
    expect(toFetchOptions({ ...DEFAULT_FILTERS, status: "active" })).toEqual({
      status: "active",
    });
  });

  test("control_id concrete → controlId key set", () => {
    expect(
      toFetchOptions({ ...DEFAULT_FILTERS, control_id: "ctrl-1" }),
    ).toEqual({ controlId: "ctrl-1" });
  });

  test("both concrete → both keys set", () => {
    expect(toFetchOptions({ status: "expired", control_id: "ctrl-1" })).toEqual(
      { status: "expired", controlId: "ctrl-1" },
    );
  });
});
