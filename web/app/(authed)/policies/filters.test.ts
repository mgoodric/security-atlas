// Slice 101 — vitest coverage for the /policies filter logic.
//
// Tests cover the two visible filter pills (status + owner_role) +
// the empty/unassigned-owner normalization branch. Neutral test data
// only — no vendor token prefixes (P0-A5).

import { describe, expect, test } from "vitest";

import type { Policy } from "@/lib/api";

import {
  ALL,
  applyFilters,
  clearFilters,
  DEFAULT_FILTERS,
  isDefault,
  setFilter,
  uniqueOwners,
  type PolicyFilters,
} from "./filters";

function makePolicy(over: Partial<Policy>): Policy {
  return {
    id: "00000000-0000-0000-0000-000000000000",
    title: "test policy",
    version: "v1.0",
    body_md: "",
    owner_role: "security_lead",
    approver_role: "cto",
    linked_control_ids: [],
    acknowledgment_required_roles: [],
    status: "published",
    source_attribution: "in_house",
    created_by: "user-1",
    created_at: "2026-01-01T00:00:00Z",
    updated_at: "2026-01-01T00:00:00Z",
    ...over,
  };
}

describe("DEFAULT_FILTERS", () => {
  test("both pills default to ALL", () => {
    expect(DEFAULT_FILTERS.status).toBe(ALL);
    expect(DEFAULT_FILTERS.owner_role).toBe(ALL);
  });
});

describe("isDefault", () => {
  test("returns true when both pills are ALL", () => {
    expect(isDefault({ status: ALL, owner_role: ALL })).toBe(true);
  });
  test("returns false when status is narrowed", () => {
    expect(isDefault({ status: "published", owner_role: ALL })).toBe(false);
  });
  test("returns false when owner is narrowed", () => {
    expect(isDefault({ status: ALL, owner_role: "security_lead" })).toBe(false);
  });
});

describe("applyFilters", () => {
  const rows: Policy[] = [
    makePolicy({ id: "p1", status: "published", owner_role: "security_lead" }),
    makePolicy({ id: "p2", status: "draft", owner_role: "people_ops" }),
    makePolicy({ id: "p3", status: "published", owner_role: "people_ops" }),
    makePolicy({ id: "p4", status: "retired", owner_role: "" }),
  ];

  test("ALL/ALL returns every row", () => {
    expect(applyFilters(rows, DEFAULT_FILTERS).map((r) => r.id)).toEqual([
      "p1",
      "p2",
      "p3",
      "p4",
    ]);
  });
  test("status filter narrows by status", () => {
    const narrowed = applyFilters(rows, {
      status: "published",
      owner_role: ALL,
    });
    expect(narrowed.map((r) => r.id)).toEqual(["p1", "p3"]);
  });
  test("owner filter narrows by owner_role", () => {
    const narrowed = applyFilters(rows, {
      status: ALL,
      owner_role: "people_ops",
    });
    expect(narrowed.map((r) => r.id)).toEqual(["p2", "p3"]);
  });
  test("owner filter normalizes empty owner to 'unassigned'", () => {
    const narrowed = applyFilters(rows, {
      status: ALL,
      owner_role: "unassigned",
    });
    expect(narrowed.map((r) => r.id)).toEqual(["p4"]);
  });
  test("status + owner intersect correctly", () => {
    const narrowed = applyFilters(rows, {
      status: "published",
      owner_role: "people_ops",
    });
    expect(narrowed.map((r) => r.id)).toEqual(["p3"]);
  });
});

describe("uniqueOwners", () => {
  test("returns alphabetized owner_role values", () => {
    const rows = [
      makePolicy({ owner_role: "security_lead" }),
      makePolicy({ owner_role: "people_ops" }),
      makePolicy({ owner_role: "security_lead" }),
      makePolicy({ owner_role: "cto" }),
    ];
    expect(uniqueOwners(rows)).toEqual(["cto", "people_ops", "security_lead"]);
  });
  test("pins 'unassigned' last when present", () => {
    const rows = [
      makePolicy({ owner_role: "" }),
      makePolicy({ owner_role: "people_ops" }),
      makePolicy({ owner_role: "" }),
    ];
    expect(uniqueOwners(rows)).toEqual(["people_ops", "unassigned"]);
  });
  test("returns empty list on empty input", () => {
    expect(uniqueOwners([])).toEqual([]);
  });
});

describe("setFilter / clearFilters", () => {
  test("setFilter returns a new object (immutable)", () => {
    const before: PolicyFilters = { status: ALL, owner_role: ALL };
    const after = setFilter(before, "status", "published");
    expect(after).toEqual({ status: "published", owner_role: ALL });
    expect(before).toEqual({ status: ALL, owner_role: ALL });
  });
  test("clearFilters returns ALL/ALL", () => {
    expect(clearFilters()).toEqual({ status: ALL, owner_role: ALL });
  });
});
