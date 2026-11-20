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
  canSetMetricTarget,
  DEFAULT_NO_DATA_REASON,
  measuredObservations,
  metricTargetError,
  metricTargetInputFromStored,
  metricTargetScale,
  metricTargetStoredFromInput,
  neutralBadgeLabel,
  noDataReason,
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

  // Slice 677 / ATLAS-023: a metric with NO target configured must NOT
  // render green "on target". Green is reserved for value-meets-target.
  // A 0.0% board KPI shown green is actively misleading to executives.
  // The badge is neutral until a target exists to measure against.
  test("no target row → neutral (no target to measure against)", () => {
    expect(thresholdBadgeColor(95, null)).toBe("neutral");
  });

  test("zero value, no target → neutral (NOT green for a 0% KPI)", () => {
    expect(thresholdBadgeColor(0, null)).toBe("neutral");
  });

  test("target row with empty target_value → neutral", () => {
    expect(
      thresholdBadgeColor(95, {
        target_value: undefined,
        warning_threshold: undefined,
        critical_threshold: undefined,
        direction: "higher_is_better",
      }),
    ).toBe("neutral");
  });
});

describe("neutralBadgeLabel — disambiguate the neutral cases (ATLAS-023)", () => {
  test("no observation (value undefined) → 'no data'", () => {
    expect(neutralBadgeLabel(undefined, null)).toBe("no data");
    expect(neutralBadgeLabel(undefined, { target_value: "95" })).toBe(
      "no data",
    );
  });

  test("value present, no target row → 'no target'", () => {
    expect(neutralBadgeLabel(0, null)).toBe("no target");
    expect(neutralBadgeLabel(94, null)).toBe("no target");
  });

  test("value present, target row but empty target_value → 'no target'", () => {
    expect(neutralBadgeLabel(0, { target_value: "" })).toBe("no target");
    expect(neutralBadgeLabel(0, { target_value: undefined })).toBe("no target");
  });

  test("value present with configured target_value → 'no data' default", () => {
    // Reaching neutral with a configured target_value implies the value
    // path didn't resolve a color — fall back to the data label.
    expect(neutralBadgeLabel(50, { target_value: "95" })).toBe("no data");
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

describe("noDataReason — the evaluators' empty marker (#2064)", () => {
  test("an empty-marked observation yields its reason", () => {
    expect(
      noDataReason({
        dimensions: {
          sample: "empty",
          no_data_reason: "No critical findings recorded in the last 90 days",
        },
      }),
    ).toBe("No critical findings recorded in the last 90 days");
  });

  test("an empty marker without a reason falls back to the default copy", () => {
    expect(noDataReason({ dimensions: { sample: "empty" } })).toBe(
      DEFAULT_NO_DATA_REASON,
    );
    expect(
      noDataReason({ dimensions: { sample: "empty", no_data_reason: " " } }),
    ).toBe(DEFAULT_NO_DATA_REASON);
  });

  test("a measured observation is not no-data", () => {
    expect(noDataReason({ dimensions: { sample: "all_controls" } })).toBeNull();
    expect(noDataReason({ dimensions: {} })).toBeNull();
    expect(noDataReason(undefined)).toBeNull();
  });

  test("measuredObservations drops empty-marked points", () => {
    const real = { id: "a", dimensions: { total_controls: "4" } };
    const empty = { id: "b", dimensions: { sample: "empty" } };
    expect(measuredObservations([empty, real, empty])).toEqual([real]);
  });
});

// #2341 — the metric-target dialog's guard and client-side check.
// The guard reads /v1/me's is_approver (authz.IsTenantApprover over the
// live roles: admin or grc_engineer, #2572), never the JWT's owner_roles
// claim, which goes stale after a revoke.
describe("canSetMetricTarget", () => {
  test.each([
    [{ is_approver: true }, true],
    [{ is_approver: false }, false],
    // An admin: /v1/me reports is_approver true for every tenant admin.
    [{ is_tenant_admin: true, is_approver: true }, true],
    // A stale claim still listing admin or grc_engineer is not enough.
    [{ is_approver: false, owner_roles: ["admin"] }, false],
    [{ is_approver: false, owner_roles: ["grc_engineer"] }, false],
    [{}, false],
    [undefined, false],
  ])("%j -> %s", (me, want) => {
    expect(canSetMetricTarget(me)).toBe(want);
  });
});

describe("metricTargetError", () => {
  const check = (
    direction: string,
    target_value: number,
    warning_threshold: number,
    critical_threshold: number,
  ) =>
    metricTargetError({
      direction,
      target_value,
      warning_threshold,
      critical_threshold,
    });

  test("ordered thresholds pass for each direction", () => {
    expect(check("higher_is_better", 95, 85, 70)).toBeNull();
    expect(check("higher_is_better", 90, 90, 90)).toBeNull();
    expect(check("lower_is_better", 7, 14, 30)).toBeNull();
    expect(check("target_is_better", 50, 45, 30)).toBeNull();
  });

  test("misordered thresholds are refused", () => {
    expect(check("higher_is_better", 80, 90, 70)).toContain(
      "critical ≤ warning ≤ target",
    );
    expect(check("lower_is_better", 30, 14, 7)).toContain(
      "target ≤ warning ≤ critical",
    );
    expect(check("target_is_better", 50, 30, 45)).toContain("critical band");
  });

  test("a value that is not a number, or no direction, is refused", () => {
    expect(check("higher_is_better", Number.NaN, 1, 0)).toBe(
      "Target must be a number.",
    );
    expect(check("higher_is_better", 2, 1, Number.NaN)).toBe(
      "Critical must be a number.",
    );
    expect(check("", 2, 1, 0)).toBe("Choose a direction.");
  });

  test("a percent metric's values must be 0..100; other units have no range", () => {
    const input = {
      direction: "higher_is_better",
      target_value: 120,
      warning_threshold: 80,
      critical_threshold: 60,
    };
    expect(metricTargetError(input, "percent")).toBe(
      "Target must be between 0 and 100.",
    );
    expect(
      metricTargetError(
        { ...input, target_value: 90, critical_threshold: -1 },
        "percent",
      ),
    ).toBe("Critical must be between 0 and 100.");
    expect(metricTargetError({ ...input, target_value: 90 }, "percent")).toBe(
      null,
    );
    expect(metricTargetError(input, "count")).toBeNull();
  });
});

// #2341 — a percent target is typed as a percentage and stored as the
// fraction the metric's observations use.
describe("metric target percent conversion", () => {
  test("stored fraction -> shown percentage", () => {
    expect(metricTargetInputFromStored("0.9", "percent")).toBe("90");
    expect(metricTargetInputFromStored("0.7", "percent")).toBe("70");
    expect(metricTargetInputFromStored("0.955", "percent")).toBe("95.5");
    expect(metricTargetInputFromStored("1", "percent")).toBe("100");
    expect(metricTargetInputFromStored("0", "percent")).toBe("0");
    // Already percentage points, as formatValue reads it.
    expect(metricTargetInputFromStored("85", "percent")).toBe("85");
    expect(metricTargetInputFromStored(undefined, "percent")).toBe("");
  });

  test("typed percentage -> stored fraction", () => {
    expect(metricTargetStoredFromInput(90, "percent")).toBe(0.9);
    expect(metricTargetStoredFromInput(70.1, "percent")).toBe(0.701);
    expect(metricTargetStoredFromInput(100, "percent")).toBe(1);
    expect(metricTargetStoredFromInput(0, "percent")).toBe(0);
  });

  test("the two directions round-trip", () => {
    for (const pct of [0, 12.5, 33.33, 70, 99.99, 100]) {
      const stored = metricTargetStoredFromInput(pct, "percent");
      expect(metricTargetInputFromStored(String(stored), "percent")).toBe(
        String(pct),
      );
    }
  });

  test("other units pass through unchanged", () => {
    expect(metricTargetInputFromStored("14", "days")).toBe("14");
    expect(metricTargetInputFromStored("0.5", "count")).toBe("0.5");
    expect(metricTargetStoredFromInput(14, "days")).toBe(14);
    expect(metricTargetStoredFromInput(14, "days", 30)).toBe(14);
  });
});

// #2572 — a percent target is stored on its observations' scale, picked
// from the latest observation, and a ratio is entered as a percentage like
// the percentage it is displayed as.
describe("metric target scale", () => {
  test.each([
    ["percent", undefined, "fraction"],
    ["percent", 0.92, "fraction"],
    ["percent", 1, "fraction"],
    ["percent", 85, "points"],
    ["ratio", undefined, "fraction"],
    ["ratio", 0.4, "fraction"],
    ["ratio", 40, "points"],
    ["count", 85, "raw"],
    ["days", undefined, "raw"],
  ] as const)("%s latest=%s -> %s", (unit, latest, want) => {
    expect(metricTargetScale(unit, latest)).toBe(want);
  });

  test.each([
    // A stored target above 1 is points, whatever the latest value.
    ["percent", 0, 90, "points"],
    ["percent", 0.5, 90, "points"],
    ["percent", undefined, 90, "points"],
    ["percent", 0, 0.9, "fraction"],
    ["ratio", 0, 40, "points"],
    ["count", 0, 90, "raw"],
  ] as const)("%s latest=%s stored=%s -> %s", (unit, latest, stored, want) => {
    expect(metricTargetScale(unit, latest, stored)).toBe(want);
  });

  test("points: latest 0, stored 90, re-saving keeps 90", () => {
    // The dialog shows the stored target, the operator saves it unchanged.
    const shown = metricTargetInputFromStored("90", "percent", 0, 90);
    expect(shown).toBe("90");
    expect(metricTargetStoredFromInput(Number(shown), "percent", 0, 90)).toBe(
      90,
    );
  });

  test("points: typed percentage is stored and shown as typed", () => {
    expect(metricTargetStoredFromInput(90, "percent", 85)).toBe(90);
    expect(metricTargetInputFromStored("90", "percent", 85)).toBe("90");
    // A target of 0.5 points is 0.5%, not 50%.
    expect(metricTargetInputFromStored("0.5", "percent", 85)).toBe("0.5");
  });

  test("points: the stored target grades a points observation", () => {
    const target = {
      target_value: String(metricTargetStoredFromInput(90, "percent", 85)),
      warning_threshold: String(metricTargetStoredFromInput(80, "percent", 85)),
      critical_threshold: String(
        metricTargetStoredFromInput(70, "percent", 85),
      ),
      direction: "higher_is_better",
    };
    expect(thresholdBadgeColor(85, target)).toBe("yellow");
  });

  test("ratio: a percentage is stored as a fraction and shown back", () => {
    expect(metricTargetStoredFromInput(40, "ratio")).toBe(0.4);
    expect(metricTargetInputFromStored("0.4", "ratio")).toBe("40");
    expect(
      metricTargetError(
        {
          target_value: 140,
          warning_threshold: 30,
          critical_threshold: 20,
          direction: "higher_is_better",
        },
        "ratio",
      ),
    ).toBe("Target must be between 0 and 100.");
  });
});
