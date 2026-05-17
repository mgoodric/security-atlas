// Slice 097 — vitest seed coverage for the pure logic in lib/api/metrics.ts.
//
// Two functions are exercised here:
//
//   * reassembleCascade — flat (metric_id, parent_id, depth) list →
//     parent/child tree. Cycle resistance is provided by the platform
//     side (the recursive CTE caps at MaxCascadeDepth=6 and never
//     returns a node twice), so the function is a pure mapping.
//
//   * thresholdBadgeColor — value × target row → green/yellow/red/neutral.
//     AC-3 lays out the rules. Each direction (higher_is_better,
//     lower_is_better, target_is_better) has its own branch; tested
//     across the meaningful boundary conditions.
//
// These tests cover AC-13 ("Vitest unit tests for the cascade-tree
// reassembly logic and the threshold-badge color calculation"). They
// are pure-function tests — no React, no DOM, no network — so they run
// at the unit-test layer rather than vitest's component layer or
// Playwright.

import { describe, expect, test } from "vitest";

import {
  type CascadeNode,
  reassembleCascade,
  thresholdBadgeColor,
} from "./metrics";

describe("reassembleCascade", () => {
  test("empty input → empty roots", () => {
    expect(reassembleCascade([])).toEqual([]);
  });

  test("single root → one tree with no children", () => {
    const nodes: CascadeNode[] = [{ metric_id: "audit_readiness", depth: 0 }];
    const trees = reassembleCascade(nodes);
    expect(trees).toHaveLength(1);
    expect(trees[0].metric_id).toBe("audit_readiness");
    expect(trees[0].children).toEqual([]);
  });

  test("root + one child → tree of depth 1", () => {
    const nodes: CascadeNode[] = [
      { metric_id: "audit_readiness", depth: 0 },
      {
        metric_id: "per_framework_coverage",
        parent_id: "audit_readiness",
        depth: 1,
      },
    ];
    const trees = reassembleCascade(nodes);
    expect(trees).toHaveLength(1);
    expect(trees[0].children).toHaveLength(1);
    expect(trees[0].children[0].metric_id).toBe("per_framework_coverage");
    expect(trees[0].children[0].depth).toBe(1);
  });

  test("root with multiple children preserves upstream ordering", () => {
    const nodes: CascadeNode[] = [
      { metric_id: "root", depth: 0 },
      { metric_id: "child_a", parent_id: "root", depth: 1 },
      { metric_id: "child_b", parent_id: "root", depth: 1 },
      { metric_id: "child_c", parent_id: "root", depth: 1 },
    ];
    const trees = reassembleCascade(nodes);
    expect(trees[0].children.map((c) => c.metric_id)).toEqual([
      "child_a",
      "child_b",
      "child_c",
    ]);
  });

  test("three-level cascade — root → program → team", () => {
    const nodes: CascadeNode[] = [
      { metric_id: "audit_readiness", depth: 0 },
      {
        metric_id: "evidence_freshness",
        parent_id: "audit_readiness",
        depth: 1,
      },
      {
        metric_id: "freshness_iam",
        parent_id: "evidence_freshness",
        depth: 2,
      },
    ];
    const trees = reassembleCascade(nodes);
    expect(trees[0].children[0].children).toHaveLength(1);
    expect(trees[0].children[0].children[0].metric_id).toBe("freshness_iam");
    expect(trees[0].children[0].children[0].depth).toBe(2);
  });

  test("multi-parent child appears as a child of every parent in `nodes`", () => {
    // The platform's recursive CTE re-emits a node once per path, so the
    // flat list may contain duplicates. The reassembly should attach to
    // the parent each row names — the function is a straight pass.
    const nodes: CascadeNode[] = [
      { metric_id: "root_a", depth: 0 },
      { metric_id: "root_b", depth: 0 },
      { metric_id: "shared", parent_id: "root_a", depth: 1 },
      { metric_id: "shared", parent_id: "root_b", depth: 1 },
    ];
    const trees = reassembleCascade(nodes);
    // Both roots include the shared node; the function deduplicates by
    // map key, so both roots share the same `CascadeTreeNode` instance.
    expect(trees).toHaveLength(2);
    expect(trees[0].children).toHaveLength(1);
    expect(trees[1].children).toHaveLength(1);
    expect(trees[0].children[0].metric_id).toBe("shared");
    expect(trees[1].children[0].metric_id).toBe("shared");
  });

  test("orphan (parent_id points to a node not in `nodes`) is treated as a root", () => {
    // Defensive: a buggy upstream truncation could leave a child whose
    // parent_id is no longer in the page. The function falls back to
    // surfacing the orphan as a root so the user still sees it.
    const nodes: CascadeNode[] = [
      { metric_id: "orphan", parent_id: "missing_parent", depth: 2 },
    ];
    const trees = reassembleCascade(nodes);
    expect(trees).toHaveLength(1);
    expect(trees[0].metric_id).toBe("orphan");
  });
});

describe("thresholdBadgeColor — neutral / no-target paths", () => {
  test("value undefined → neutral (no observation yet)", () => {
    expect(thresholdBadgeColor(undefined, null)).toBe("neutral");
  });

  test("no target row → green (nothing to fail against)", () => {
    expect(thresholdBadgeColor(95, null)).toBe("green");
  });

  test("target row with empty target_value → green", () => {
    expect(
      thresholdBadgeColor(95, {
        target_value: undefined,
        warning_threshold: undefined,
        critical_threshold: undefined,
        direction: "higher_is_better",
      }),
    ).toBe("green");
  });
});

describe("thresholdBadgeColor — higher_is_better", () => {
  const target = {
    target_value: "95",
    warning_threshold: "90",
    critical_threshold: "80",
    direction: "higher_is_better",
  };

  test("value at or above target → green", () => {
    expect(thresholdBadgeColor(95, target)).toBe("green");
    expect(thresholdBadgeColor(97, target)).toBe("green");
  });

  test("value between warning and target → yellow", () => {
    expect(thresholdBadgeColor(92, target)).toBe("yellow");
    expect(thresholdBadgeColor(90, target)).toBe("yellow");
  });

  test("value at or below critical → red", () => {
    expect(thresholdBadgeColor(80, target)).toBe("red");
    expect(thresholdBadgeColor(70, target)).toBe("red");
  });

  test("value between critical and warning (no man's land) → red", () => {
    expect(thresholdBadgeColor(85, target)).toBe("red");
  });
});

describe("thresholdBadgeColor — lower_is_better", () => {
  const target = {
    target_value: "7", // e.g. P1 patch median days
    warning_threshold: "14",
    critical_threshold: "30",
    direction: "lower_is_better",
  };

  test("value at or below target → green", () => {
    expect(thresholdBadgeColor(5, target)).toBe("green");
    expect(thresholdBadgeColor(7, target)).toBe("green");
  });

  test("value between target and warning → yellow", () => {
    expect(thresholdBadgeColor(10, target)).toBe("yellow");
    expect(thresholdBadgeColor(14, target)).toBe("yellow");
  });

  test("value at or above critical → red", () => {
    expect(thresholdBadgeColor(30, target)).toBe("red");
    expect(thresholdBadgeColor(45, target)).toBe("red");
  });
});

describe("thresholdBadgeColor — target_is_better", () => {
  const target = {
    target_value: "100",
    warning_threshold: "105", // inner band half-width via |w - t| = 5
    critical_threshold: "120", // outer band via |c - t| = 20
    direction: "target_is_better",
  };

  test("at target → green", () => {
    expect(thresholdBadgeColor(100, target)).toBe("green");
  });

  test("within inner band on either side → green", () => {
    expect(thresholdBadgeColor(97, target)).toBe("green");
    expect(thresholdBadgeColor(103, target)).toBe("green");
  });

  test("beyond inner but within outer → yellow", () => {
    expect(thresholdBadgeColor(110, target)).toBe("yellow");
    expect(thresholdBadgeColor(85, target)).toBe("yellow");
  });

  test("outside outer band → red", () => {
    expect(thresholdBadgeColor(125, target)).toBe("red");
    expect(thresholdBadgeColor(75, target)).toBe("red");
  });

  test("with no warning/critical, falls back to +/-5% and +/-15% defaults", () => {
    const fallback = {
      target_value: "100",
      warning_threshold: undefined,
      critical_threshold: undefined,
      direction: "target_is_better",
    };
    expect(thresholdBadgeColor(102, fallback)).toBe("green"); // within 5%
    expect(thresholdBadgeColor(108, fallback)).toBe("yellow"); // within 15%
    expect(thresholdBadgeColor(120, fallback)).toBe("red"); // beyond 15%
  });
});
