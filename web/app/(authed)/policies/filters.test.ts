// Slice 101 — vitest coverage for the /policies filter logic.
//
// Tests cover the three visible filter pills (status + owner_role +
// ack_status) + the empty/unassigned-owner normalization branch +
// the slice 238 band predicate with null-rate handling. Neutral test
// data only — no vendor token prefixes (P0-A5).

import { describe, expect, test } from "vitest";

import type { Policy, PolicyAckRate } from "@/lib/api/policies";

import {
  ACK_STATUS_GE_95,
  ACK_STATUS_LT_50,
  ACK_STATUS_LT_95,
  ackStatusMatches,
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

function ackRate(percent: number | null, num = 50, den = 50): PolicyAckRate {
  return { numerator: num, denominator: den, percent };
}

describe("DEFAULT_FILTERS", () => {
  test("all three pills default to ALL", () => {
    expect(DEFAULT_FILTERS.status).toBe(ALL);
    expect(DEFAULT_FILTERS.owner_role).toBe(ALL);
    expect(DEFAULT_FILTERS.ack_status).toBe(ALL);
  });
});

describe("isDefault", () => {
  test("returns true when all three pills are ALL", () => {
    expect(isDefault({ status: ALL, owner_role: ALL, ack_status: ALL })).toBe(
      true,
    );
  });
  test("returns false when status is narrowed", () => {
    expect(
      isDefault({ status: "published", owner_role: ALL, ack_status: ALL }),
    ).toBe(false);
  });
  test("returns false when owner is narrowed", () => {
    expect(
      isDefault({ status: ALL, owner_role: "security_lead", ack_status: ALL }),
    ).toBe(false);
  });
  test("returns false when ack_status is narrowed", () => {
    expect(
      isDefault({ status: ALL, owner_role: ALL, ack_status: ACK_STATUS_GE_95 }),
    ).toBe(false);
  });
});

describe("applyFilters", () => {
  const rows: Policy[] = [
    makePolicy({ id: "p1", status: "published", owner_role: "security_lead" }),
    makePolicy({ id: "p2", status: "draft", owner_role: "people_ops" }),
    makePolicy({ id: "p3", status: "published", owner_role: "people_ops" }),
    makePolicy({ id: "p4", status: "retired", owner_role: "" }),
  ];

  test("ALL/ALL/ALL returns every row", () => {
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
      ack_status: ALL,
    });
    expect(narrowed.map((r) => r.id)).toEqual(["p1", "p3"]);
  });
  test("owner filter narrows by owner_role", () => {
    const narrowed = applyFilters(rows, {
      status: ALL,
      owner_role: "people_ops",
      ack_status: ALL,
    });
    expect(narrowed.map((r) => r.id)).toEqual(["p2", "p3"]);
  });
  test("owner filter normalizes empty owner to 'unassigned'", () => {
    const narrowed = applyFilters(rows, {
      status: ALL,
      owner_role: "unassigned",
      ack_status: ALL,
    });
    expect(narrowed.map((r) => r.id)).toEqual(["p4"]);
  });
  test("status + owner intersect correctly", () => {
    const narrowed = applyFilters(rows, {
      status: "published",
      owner_role: "people_ops",
      ack_status: ALL,
    });
    expect(narrowed.map((r) => r.id)).toEqual(["p3"]);
  });
});

describe("ackStatusMatches (slice 238 band predicate)", () => {
  test("ALL band includes every row regardless of rate", () => {
    expect(ackStatusMatches(makePolicy({}), ALL)).toBe(true);
    expect(ackStatusMatches(makePolicy({ ack_rate: ackRate(98) }), ALL)).toBe(
      true,
    );
    expect(ackStatusMatches(makePolicy({ ack_rate: ackRate(null) }), ALL)).toBe(
      true,
    );
    expect(ackStatusMatches(makePolicy({ ack_rate: null }), ALL)).toBe(true);
  });

  test("GE95 band includes rows at 95% and above", () => {
    expect(
      ackStatusMatches(makePolicy({ ack_rate: ackRate(95) }), ACK_STATUS_GE_95),
    ).toBe(true);
    expect(
      ackStatusMatches(
        makePolicy({ ack_rate: ackRate(100) }),
        ACK_STATUS_GE_95,
      ),
    ).toBe(true);
    expect(
      ackStatusMatches(makePolicy({ ack_rate: ackRate(94) }), ACK_STATUS_GE_95),
    ).toBe(false);
  });

  test("LT95 band includes rows strictly below 95%", () => {
    expect(
      ackStatusMatches(makePolicy({ ack_rate: ackRate(94) }), ACK_STATUS_LT_95),
    ).toBe(true);
    expect(
      ackStatusMatches(makePolicy({ ack_rate: ackRate(50) }), ACK_STATUS_LT_95),
    ).toBe(true);
    expect(
      ackStatusMatches(makePolicy({ ack_rate: ackRate(95) }), ACK_STATUS_LT_95),
    ).toBe(false);
  });

  test("LT50 band includes rows strictly below 50%", () => {
    expect(
      ackStatusMatches(makePolicy({ ack_rate: ackRate(49) }), ACK_STATUS_LT_50),
    ).toBe(true);
    expect(
      ackStatusMatches(makePolicy({ ack_rate: ackRate(0) }), ACK_STATUS_LT_50),
    ).toBe(true);
    expect(
      ackStatusMatches(makePolicy({ ack_rate: ackRate(50) }), ACK_STATUS_LT_50),
    ).toBe(false);
  });

  test("AC-2: null ack_rate is excluded from every non-ALL band", () => {
    const noRate = makePolicy({ ack_rate: null });
    expect(ackStatusMatches(noRate, ACK_STATUS_GE_95)).toBe(false);
    expect(ackStatusMatches(noRate, ACK_STATUS_LT_95)).toBe(false);
    expect(ackStatusMatches(noRate, ACK_STATUS_LT_50)).toBe(false);
  });

  test("AC-2: ack_rate.percent=null is excluded from every non-ALL band", () => {
    const zeroDenom = makePolicy({ ack_rate: ackRate(null, 0, 0) });
    expect(ackStatusMatches(zeroDenom, ACK_STATUS_GE_95)).toBe(false);
    expect(ackStatusMatches(zeroDenom, ACK_STATUS_LT_95)).toBe(false);
    expect(ackStatusMatches(zeroDenom, ACK_STATUS_LT_50)).toBe(false);
  });

  test("unknown band value falls back to ALL (forward-compat with stale URLs)", () => {
    const row = makePolicy({ ack_rate: ackRate(20) });
    expect(ackStatusMatches(row, "bogus-band-from-old-deploy")).toBe(true);
  });
});

describe("applyFilters — ack_status pill", () => {
  const rows: Policy[] = [
    // published rows with varying rates
    makePolicy({
      id: "p1",
      status: "published",
      owner_role: "security_lead",
      ack_rate: ackRate(98),
    }),
    makePolicy({
      id: "p2",
      status: "published",
      owner_role: "people_ops",
      ack_rate: ackRate(80),
    }),
    makePolicy({
      id: "p3",
      status: "published",
      owner_role: "people_ops",
      ack_rate: ackRate(30),
    }),
    // null-rate rows
    makePolicy({
      id: "p4",
      status: "draft",
      owner_role: "people_ops",
      ack_rate: null,
    }),
    makePolicy({
      id: "p5",
      status: "published",
      owner_role: "security_lead",
      ack_rate: ackRate(null, 0, 0),
    }),
  ];

  test("ack_status=ge95 keeps only the 98% row", () => {
    const narrowed = applyFilters(rows, {
      status: ALL,
      owner_role: ALL,
      ack_status: ACK_STATUS_GE_95,
    });
    expect(narrowed.map((r) => r.id)).toEqual(["p1"]);
  });

  test("ack_status=lt95 keeps the 80% and 30% rows", () => {
    const narrowed = applyFilters(rows, {
      status: ALL,
      owner_role: ALL,
      ack_status: ACK_STATUS_LT_95,
    });
    expect(narrowed.map((r) => r.id)).toEqual(["p2", "p3"]);
  });

  test("ack_status=lt50 keeps only the 30% row", () => {
    const narrowed = applyFilters(rows, {
      status: ALL,
      owner_role: ALL,
      ack_status: ACK_STATUS_LT_50,
    });
    expect(narrowed.map((r) => r.id)).toEqual(["p3"]);
  });

  test("ack_status pill intersects with status pill", () => {
    const narrowed = applyFilters(rows, {
      status: "published",
      owner_role: ALL,
      ack_status: ACK_STATUS_LT_95,
    });
    expect(narrowed.map((r) => r.id)).toEqual(["p2", "p3"]);
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
    const before: PolicyFilters = {
      status: ALL,
      owner_role: ALL,
      ack_status: ALL,
    };
    const after = setFilter(before, "status", "published");
    expect(after).toEqual({
      status: "published",
      owner_role: ALL,
      ack_status: ALL,
    });
    expect(before).toEqual({
      status: ALL,
      owner_role: ALL,
      ack_status: ALL,
    });
  });
  test("setFilter updates ack_status independently", () => {
    const before: PolicyFilters = {
      status: ALL,
      owner_role: ALL,
      ack_status: ALL,
    };
    const after = setFilter(before, "ack_status", ACK_STATUS_GE_95);
    expect(after.ack_status).toBe(ACK_STATUS_GE_95);
    expect(after.status).toBe(ALL);
    expect(after.owner_role).toBe(ALL);
  });
  test("clearFilters returns ALL/ALL/ALL", () => {
    expect(clearFilters()).toEqual({
      status: ALL,
      owner_role: ALL,
      ack_status: ALL,
    });
  });
});
