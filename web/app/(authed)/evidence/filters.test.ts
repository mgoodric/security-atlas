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
  buildControlOptions,
  buildKindOptions,
  buildResultOptions,
  clearFilters,
  DEFAULT_FILTERS,
  isDefault,
  isNoneSelected,
  setFilter,
  toFetchOptions,
} from "./filters";

describe("evidence filters", () => {
  test("DEFAULT_FILTERS yields no narrowing on any axis", () => {
    expect(DEFAULT_FILTERS.controlId).toBe(NONE);
    expect(DEFAULT_FILTERS.kind).toBe(ALL);
    expect(DEFAULT_FILTERS.result).toBe(ALL);
    expect(DEFAULT_FILTERS.sourceActorType).toBe(ALL);
    expect(DEFAULT_FILTERS.sourceActorId).toBe(ALL);
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
    };
    const cleared = clearFilters();
    void filters;
    expect(cleared.controlId).toBe(NONE);
    expect(cleared.kind).toBe(ALL);
    expect(cleared.result).toBe(ALL);
    expect(cleared.sourceActorType).toBe(ALL);
    expect(cleared.sourceActorId).toBe(ALL);
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
});
