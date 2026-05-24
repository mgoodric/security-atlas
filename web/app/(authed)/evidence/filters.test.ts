// Slice 099 + 106 — unit tests for the /evidence filter logic.
//
// Slice 099 originally pinned the "Select a control…" prompt as the
// first control-pill option. Slice 106 makes the list default to
// tenant-wide, so the first option becomes "All controls" — the test
// suite below tracks that change AND adds coverage for the four new
// filter axes (kind, result, sourceActorType, sourceActorId) plus the
// `toFetchOptions` translator.

import { describe, expect, test } from "vitest";

import type { Anchor } from "@/lib/api";

import {
  ALL,
  NONE,
  SCOPE_CELL_CAP,
  SOURCE_DELIM,
  buildControlOptions,
  buildKindOptions,
  buildResultOptions,
  buildScopeCellOptions,
  buildSinceOptions,
  buildSourceOptions,
  clearFilters,
  DEFAULT_FILTERS,
  isDefault,
  isNoneSelected,
  scopeCellLabel,
  setFilter,
  sinceCutoff,
  toFetchOptions,
} from "./filters";

describe("evidence filters", () => {
  test("DEFAULT_FILTERS yields no narrowing on any axis", () => {
    expect(DEFAULT_FILTERS.controlId).toBe(NONE);
    expect(DEFAULT_FILTERS.kind).toBe(ALL);
    expect(DEFAULT_FILTERS.result).toBe(ALL);
    expect(DEFAULT_FILTERS.sourceActorType).toBe(ALL);
    expect(DEFAULT_FILTERS.sourceActorId).toBe(ALL);
    // Slice 234 — three new axes also default to ALL (no narrowing).
    expect(DEFAULT_FILTERS.scopeCellId).toBe(ALL);
    expect(DEFAULT_FILTERS.since).toBe(ALL);
    expect(isNoneSelected(DEFAULT_FILTERS)).toBe(true);
    expect(isDefault(DEFAULT_FILTERS)).toBe(true);
  });

  test("setFilter returns a new filter with the updated key (immutability)", () => {
    const next = setFilter(
      DEFAULT_FILTERS,
      "controlId",
      "33333333-3333-3333-3333-333333333333",
    );
    expect(next.controlId).toBe("33333333-3333-3333-3333-333333333333");
    expect(isNoneSelected(next)).toBe(false);
    expect(isDefault(next)).toBe(false);
    // immutability
    expect(DEFAULT_FILTERS.controlId).toBe(NONE);
  });

  test("setFilter on a new axis (result) updates only that axis", () => {
    const next = setFilter(DEFAULT_FILTERS, "result", "fail");
    expect(next.result).toBe("fail");
    expect(next.controlId).toBe(NONE);
    expect(next.kind).toBe(ALL);
    expect(isDefault(next)).toBe(false);
  });

  test("clearFilters returns the default (no narrowing on any axis)", () => {
    const filters = {
      controlId: "33333333-3333-3333-3333-333333333333",
      kind: "k.v1",
      result: "fail",
      sourceActorType: "connector",
      sourceActorId: "aws-connector",
      scopeCellId: "22222222-2222-2222-2222-222222222222",
      since: "7d",
    };
    const cleared = clearFilters();
    void filters;
    expect(cleared.controlId).toBe(NONE);
    expect(cleared.kind).toBe(ALL);
    expect(cleared.result).toBe(ALL);
    expect(cleared.sourceActorType).toBe(ALL);
    expect(cleared.sourceActorId).toBe(ALL);
    expect(cleared.scopeCellId).toBe(ALL);
    expect(cleared.since).toBe(ALL);
    expect(isDefault(cleared)).toBe(true);
  });

  test("ALL sentinel is distinct from NONE sentinel", () => {
    // The two sentinels overlap in intent but mean different things at
    // the URL-encoding boundary: ALL is the shared 098 shell's "no
    // narrowing" value, NONE is the empty string used as the
    // omit-from-URL marker for controlId. They must stay distinct.
    expect(ALL).not.toBe(NONE);
  });

  test("buildControlOptions prepends the 'All controls' sentinel (slice 106)", () => {
    const anchors: Anchor[] = [
      {
        id: "11111111-1111-1111-1111-111111111111",
        scf_id: "SCF:IAC-06",
        family: "IAC",
        name: "Multi-factor authentication",
        description: "",
      },
      {
        id: "22222222-2222-2222-2222-222222222222",
        scf_id: "SCF:CRY-04",
        family: "CRY",
        name: "Encryption at rest",
        description: "",
      },
    ];
    const opts = buildControlOptions(anchors);
    expect(opts).toHaveLength(3);
    // Slice 106: "All controls" replaces the slice-099 "Select a control…".
    expect(opts[0]).toEqual({ value: NONE, label: "All controls" });
    // Sorted alphabetically by scf_id (CRY before IAC).
    expect(opts[1]?.value).toBe("22222222-2222-2222-2222-222222222222");
    expect(opts[1]?.label).toBe("SCF:CRY-04 · Encryption at rest");
    expect(opts[2]?.value).toBe("11111111-1111-1111-1111-111111111111");
  });

  test("buildControlOptions handles an empty anchor list", () => {
    const opts = buildControlOptions([]);
    expect(opts).toHaveLength(1);
    expect(opts[0]).toEqual({ value: NONE, label: "All controls" });
  });

  test("buildResultOptions emits ALL + the four enum values", () => {
    const opts = buildResultOptions();
    expect(opts).toHaveLength(5);
    expect(opts[0]).toEqual({ value: ALL, label: "All results" });
    expect(opts.map((o) => o.value)).toEqual([
      ALL,
      "pass",
      "fail",
      "na",
      "inconclusive",
    ]);
  });

  test("buildKindOptions sorts unique kinds + prepends ALL", () => {
    const opts = buildKindOptions([
      "github.repo.v1",
      "aws.s3.v1",
      "github.repo.v1",
      "",
    ]);
    expect(opts).toEqual([
      { value: ALL, label: "All kinds" },
      { value: "aws.s3.v1", label: "aws.s3.v1" },
      { value: "github.repo.v1", label: "github.repo.v1" },
    ]);
  });

  test("toFetchOptions drops sentinel values", () => {
    const out = toFetchOptions(DEFAULT_FILTERS);
    expect(out).toEqual({});
  });

  test("toFetchOptions passes narrowing values through", () => {
    const filters = {
      controlId: "33333333-3333-3333-3333-333333333333",
      kind: "k.v1",
      result: "fail",
      sourceActorType: "connector",
      sourceActorId: "aws-connector",
      scopeCellId: ALL,
      since: ALL,
    };
    const out = toFetchOptions(filters);
    expect(out).toEqual({
      controlID: "33333333-3333-3333-3333-333333333333",
      kind: "k.v1",
      result: "fail",
      sourceActorType: "connector",
      sourceActorID: "aws-connector",
    });
  });

  // ----- slice 234 helpers -----

  test("buildSourceOptions emits ALL + observed (type, id) tuples deduped + sorted", () => {
    const opts = buildSourceOptions([
      { actor_type: "connector", actor_id: "aws-connector" },
      { actor_type: "user", actor_id: "alice@example.com" },
      { actor_type: "connector", actor_id: "aws-connector" }, // dup
      { actor_type: "connector" }, // no id — skipped
      { actor_id: "anon" }, // no type — skipped
    ]);
    expect(opts).toEqual([
      { value: ALL, label: "All sources" },
      {
        value: `connector${SOURCE_DELIM}aws-connector`,
        label: "connector · aws-connector",
      },
      {
        value: `user${SOURCE_DELIM}alice@example.com`,
        label: "user · alice@example.com",
      },
    ]);
  });

  test("buildSourceOptions handles an empty input", () => {
    expect(buildSourceOptions([])).toEqual([
      { value: ALL, label: "All sources" },
    ]);
  });

  test("scopeCellLabel: prefers explicit label; falls back to k=v summary; finally to id", () => {
    expect(
      scopeCellLabel({ id: "id-1", label: "prod-cell", dimensions: {} }),
    ).toBe("prod-cell");
    expect(
      scopeCellLabel({
        id: "id-1",
        label: "",
        dimensions: { env: "prod", cloud: "aws", region: "us-east" },
      }),
    ).toBe("cloud=aws / env=prod / region=us-east");
    // Empty label + empty dimensions falls back to the UUID.
    expect(scopeCellLabel({ id: "id-only", label: "", dimensions: {} })).toBe(
      "id-only",
    );
  });

  test("buildScopeCellOptions emits ALL + at most SCOPE_CELL_CAP cells in input order", () => {
    const cells = Array.from({ length: SCOPE_CELL_CAP + 5 }, (_, i) => ({
      id: `id-${i}`,
      label: `cell-${i}`,
      dimensions: {} as Record<string, string>,
    }));
    const opts = buildScopeCellOptions(cells);
    expect(opts[0]).toEqual({ value: ALL, label: "All cells" });
    // Cap + 1 (ALL sentinel) — extras are dropped.
    expect(opts).toHaveLength(SCOPE_CELL_CAP + 1);
    expect(opts[1]?.value).toBe("id-0");
    expect(opts[SCOPE_CELL_CAP]?.value).toBe(`id-${SCOPE_CELL_CAP - 1}`);
  });

  test("buildSinceOptions emits ALL + 24h / 7d / 30d / audit; audit label adapts to active period name", () => {
    const without = buildSinceOptions();
    expect(without.map((o) => o.value)).toEqual([
      ALL,
      "24h",
      "7d",
      "30d",
      "audit",
    ]);
    expect(without[4]?.label).toBe("Audit period (current)");

    const withActive = buildSinceOptions("Q2 2026");
    expect(withActive[4]?.label).toBe("Audit period (Q2 2026)");
  });

  test("sinceCutoff: presets are relative to `now`; audit reads the supplied period start", () => {
    // Fixed clock: 2026-05-23T10:00:00Z.
    const now = new Date("2026-05-23T10:00:00.000Z");
    expect(sinceCutoff("24h", now)).toBe("2026-05-22T10:00:00.000Z");
    expect(sinceCutoff("7d", now)).toBe("2026-05-16T10:00:00.000Z");
    expect(sinceCutoff("30d", now)).toBe("2026-04-23T10:00:00.000Z");
    expect(sinceCutoff("audit", now, "2026-04-01T00:00:00Z")).toBe(
      "2026-04-01T00:00:00Z",
    );
    // Audit with no active-period start: undefined (no override).
    expect(sinceCutoff("audit", now)).toBeUndefined();
    // Unknown key: undefined (no override).
    expect(sinceCutoff(ALL, now)).toBeUndefined();
    expect(sinceCutoff("bogus", now)).toBeUndefined();
  });

  test("toFetchOptions passes scope_cell_id + resolved since through", () => {
    const filters = {
      ...DEFAULT_FILTERS,
      scopeCellId: "22222222-2222-2222-2222-222222222222",
      since: "7d",
    };
    const out = toFetchOptions(filters, "2026-05-16T10:00:00.000Z");
    expect(out.scopeCellID).toBe("22222222-2222-2222-2222-222222222222");
    expect(out.since).toBe("2026-05-16T10:00:00.000Z");
  });

  test("toFetchOptions omits since when resolvedSince is undefined", () => {
    // E.g. since=audit with no active audit period; the page passes
    // undefined and the upstream default 30-day window applies.
    const filters = {
      ...DEFAULT_FILTERS,
      since: "audit",
    };
    const out = toFetchOptions(filters, undefined);
    expect(out.since).toBeUndefined();
  });
});
