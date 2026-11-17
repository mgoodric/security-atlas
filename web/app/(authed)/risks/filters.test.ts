// Slice 100 — vitest unit coverage for /risks filter + formatter logic.
//
// Pure-data tests. No React, no DOM, no fetch. The per-row formatter
// helpers and category options from `./filters` (#2132 moved filtering to
// the server; see server-query.test.ts).
//
// All test fixtures use neutral identifiers — NO vendor token prefixes,
// NO real names that could look like seeded data (per slice 100 P0-A4
// and slice 069 hardening).

import { describe, expect, test } from "vitest";

import type { RiskCategory } from "@/lib/api/risks";

import {
  ALL,
  categoryDisplay,
  categoryOptions,
  fairScore,
  formatDollars,
  patternNoLongerMet,
  formatResidualScore,
  residualClass,
  residualSeverity,
  residualState,
  reviewDuePending,
  reviewOverdue,
  severityBand,
  severityBandLabel,
  severityClasses,
} from "./filters";

describe("categoryOptions / categoryDisplay (#1817)", () => {
  function cat(slug: string, name: string, archived = false): RiskCategory {
    return {
      id: `id-${slug}`,
      slug,
      name,
      description: "",
      archived,
      is_default: false,
      risk_count: 0,
      rule_count: 0,
    };
  }
  const cats = [
    cat("operational", "Operational"),
    cat("legacy", "Legacy", true),
    cat("cloud", "Cloud posture"),
  ];

  test("lists active categories by name, matched by slug", () => {
    expect(categoryOptions(cats, false, ALL)).toEqual([
      { value: ALL, label: "All categories" },
      { value: "cloud", label: "Cloud posture" },
      { value: "operational", label: "Operational" },
    ]);
  });

  test("show archived adds archived categories, marked", () => {
    expect(categoryOptions(cats, true, ALL)).toContainEqual({
      value: "legacy",
      label: "Legacy (archived)",
    });
  });

  test("keeps a selected archived category listed when hidden", () => {
    expect(
      categoryOptions(cats, false, "legacy").map((o) => o.value),
    ).toContain("legacy");
  });

  test("categoryDisplay prefers category_name and marks archived", () => {
    expect(
      categoryDisplay({ category: "legacy", category_name: "Old name" }, cats),
    ).toEqual({ name: "Old name", archived: true });
    expect(categoryDisplay({ category: "cloud" }, cats)).toEqual({
      name: "Cloud posture",
      archived: false,
    });
    expect(categoryDisplay({ category: "unknown" }, [])).toEqual({
      name: "unknown",
      archived: false,
    });
  });
});

describe("severityBand", () => {
  test("0 is the 'none' band (no numeric score)", () => {
    expect(severityBand(0)).toBe("none");
    // #2185: any value above 0 is low, so a small residual is not "none".
    expect(severityBand(0.4)).toBe("low");
    expect(severityBand(0.01)).toBe("low");
  });

  test("1..7 is the 'low' band", () => {
    expect(severityBand(1)).toBe("low");
    expect(severityBand(7)).toBe("low");
  });

  test("8..14 is the 'medium' band", () => {
    expect(severityBand(8)).toBe("medium");
    expect(severityBand(14)).toBe("medium");
  });

  test("15..25 is the 'high' band", () => {
    expect(severityBand(15)).toBe("high");
    expect(severityBand(25)).toBe("high");
  });

  test("severities above the 5x5 ceiling still bucket as 'high'", () => {
    expect(severityBand(99)).toBe("high");
  });
});

describe("formatResidualScore", () => {
  test("formats a 5x5 score on the 1..25 inherent scale (#2058)", () => {
    expect(formatResidualScore({ likelihood: 4, impact: 5 })).toBe("20");
    expect(formatResidualScore({ likelihood: 2, impact: 3 })).toBe("6");
  });

  test("formats a backend-derived {score, inherent_score, ...} residual (#2058)", () => {
    // internal/risk/residual_store.go residualBlob: score = inherent ×
    // (1 − effectiveness), already on 1..25.
    expect(
      formatResidualScore({
        score: 12,
        inherent_score: 20,
        weighted_control_effectiveness: 0.4,
        breakdown: [],
      }),
    ).toBe("12");
    expect(formatResidualScore({ score: 7.2, inherent_score: 12 })).toBe("7.2");
    expect(formatResidualScore({ severity: 16 })).toBe("16");
  });

  test("formats a deriver {score} on a FAIR risk as dollars", () => {
    expect(formatResidualScore({ score: 60000 }, "fair")).toBe("$60,000");
  });

  test("renders '—' for null / undefined / non-object", () => {
    expect(formatResidualScore(null)).toBe("—");
    expect(formatResidualScore(undefined)).toBe("—");
    expect(formatResidualScore("not an object")).toBe("—");
    expect(formatResidualScore(42)).toBe("—");
  });

  test("renders '—' for a score missing likelihood or impact", () => {
    expect(formatResidualScore({})).toBe("—");
    expect(formatResidualScore({ likelihood: 3 })).toBe("—");
    expect(formatResidualScore({ impact: 3 })).toBe("—");
  });

  test("renders '—' for a score with non-numeric components", () => {
    expect(formatResidualScore({ likelihood: "three", impact: 4 })).toBe("—");
    expect(formatResidualScore({ likelihood: 3, impact: "four" })).toBe("—");
  });

  test("does not clamp a score above the 5x5 ceiling", () => {
    // L=6, I=6 is the operator's problem to spot, not the renderer's.
    expect(formatResidualScore({ likelihood: 6, impact: 6 })).toBe("36");
  });

  test("formats FAIR annualized loss exposure as dollars", () => {
    expect(
      formatResidualScore({
        loss_event_frequency: 2,
        loss_magnitude: 50000,
        annualized_loss_exposure: 100000,
      }),
    ).toBe("$100,000");
  });

  test("renders '—' when the 5x5 product is not finite", () => {
    expect(formatResidualScore({ likelihood: Infinity, impact: 2 })).toBe("—");
  });
});

describe("residualSeverity (#2058)", () => {
  test("reads score, then severity, then likelihood × impact", () => {
    expect(residualSeverity({ score: 12, severity: 3 })).toBe(12);
    expect(residualSeverity({ severity: 16 })).toBe(16);
    expect(residualSeverity({ likelihood: 3, impact: 4 })).toBe(12);
  });

  test("returns null when no shape carries a finite number", () => {
    expect(residualSeverity({})).toBeNull();
    expect(residualSeverity(null)).toBeNull();
    expect(residualSeverity({ score: Number.NaN })).toBeNull();
    expect(residualSeverity({ likelihood: 3 })).toBeNull();
  });
});

describe("fairScore", () => {
  test("derives exposure when only frequency and magnitude are present", () => {
    expect(
      fairScore({ loss_event_frequency: 1.5, loss_magnitude: 40000 }),
    ).toEqual({
      loss_event_frequency: 1.5,
      loss_magnitude: 40000,
      annualized_loss_exposure: 60000,
    });
  });

  test("accepts legacy lef/lm aliases", () => {
    expect(fairScore({ lef: 2, lm: 50000 })?.annualized_loss_exposure).toBe(
      100000,
    );
  });

  test("rejects an exposure that is not a finite number", () => {
    expect(
      fairScore({
        loss_event_frequency: 1,
        loss_magnitude: 2,
        annualized_loss_exposure: Number.NaN,
      }),
    ).toBeNull();
    expect(
      fairScore({ loss_event_frequency: Infinity, loss_magnitude: 2 }),
    ).toBeNull();
  });
});

describe("formatDollars", () => {
  test("formats a finite number as whole USD", () => {
    expect(formatDollars(60000)).toBe("$60,000");
  });

  test("renders '—' for a non-finite number", () => {
    expect(formatDollars(Number.NaN)).toBe("—");
    expect(formatDollars(Infinity)).toBe("—");
  });
});

describe("severityClasses + residualClass", () => {
  test("severityClasses maps each band to the expected palette", () => {
    expect(severityClasses("high")).toContain("rose");
    expect(severityClasses("medium")).toContain("amber");
    expect(severityClasses("low")).toContain("emerald");
    expect(severityClasses("none")).toContain("muted");
  });

  test("residualClass bands a 1..25 residual like inherent severity (#2058)", () => {
    expect(residualClass("20")).toBe(severityClasses("high"));
    expect(residualClass("12")).toBe(severityClasses("medium"));
    expect(residualClass("7.2")).toBe(severityClasses("low"));
    expect(residualClass("—")).toContain("muted");
  });

  test("severityBandLabel names every band", () => {
    expect(severityBandLabel("high")).toBe("High");
    expect(severityBandLabel("medium")).toBe("Medium");
    expect(severityBandLabel("low")).toBe("Low");
    expect(severityBandLabel("none")).toBe("None");
  });

  test("residualClass mutes a string that does not parse as a number", () => {
    // A FAIR row's formatted "$60,000" is not parseFloat-able — the
    // residual column renders it neutrally rather than banding it.
    expect(residualClass("$60,000")).toContain("muted");
  });
});

describe("residualState (slice 680 / ATLAS-029)", () => {
  test("a brand-new risk (empty {} residual) is pending, not scored", () => {
    // internal/risk/store.go defaultResidual stores `{}` when the create
    // path omits residual_score — this is the dominant new-risk case.
    expect(residualState({})).toBe("pending");
  });

  test("null / undefined / non-object residual is pending", () => {
    expect(residualState(null)).toBe("pending");
    expect(residualState(undefined)).toBe("pending");
    expect(residualState("not an object")).toBe("pending");
    expect(residualState(42)).toBe("pending");
  });

  test("a partial residual (missing a component) is pending", () => {
    expect(residualState({ likelihood: 3 })).toBe("pending");
    expect(residualState({ impact: 3 })).toBe("pending");
    expect(residualState({ likelihood: "three", impact: 4 })).toBe("pending");
  });

  test("a valid {likelihood, impact} residual is scored", () => {
    expect(residualState({ likelihood: 2, impact: 3 })).toBe("scored");
    expect(residualState({ likelihood: 4, impact: 5 })).toBe("scored");
  });

  test("a backend-derived {score, ...} residual is scored, not pending (#2058)", () => {
    expect(
      residualState({
        score: 12,
        inherent_score: 20,
        weighted_control_effectiveness: 0.4,
        breakdown: [],
      }),
    ).toBe("scored");
    // A zero residual (fully effective controls) is a real residual.
    expect(residualState({ score: 0, inherent_score: 9 })).toBe("scored");
    expect(residualState({ severity: 16 })).toBe("scored");
    expect(residualState({ score: "12" })).toBe("pending");
  });

  test("a FAIR residual is scored", () => {
    expect(
      residualState({ loss_event_frequency: 1.5, loss_magnitude: 40000 }),
    ).toBe("scored");
  });

  test("a residual whose 5x5 product is not finite is pending", () => {
    expect(residualState({ likelihood: Infinity, impact: 2 })).toBe("pending");
  });
});

describe("reviewDuePending (slice 680 / ATLAS-029)", () => {
  test("absent or empty review_due_at is pending", () => {
    expect(reviewDuePending(undefined)).toBe(true);
    expect(reviewDuePending("")).toBe(true);
    expect(reviewDuePending("   ")).toBe(true);
  });

  test("a real ISO date is not pending", () => {
    expect(reviewDuePending("2026-09-30T00:00:00Z")).toBe(false);
  });
});

describe("reviewOverdue (#2059)", () => {
  const now = new Date("2026-11-14T10:00:00Z");

  test("a due day before today (UTC) is overdue", () => {
    expect(reviewOverdue("2026-11-13T23:59:59Z", now)).toBe(true);
  });

  test("a review due today is not overdue yet, even past its hour", () => {
    expect(reviewOverdue("2026-11-14T00:00:00Z", now)).toBe(false);
  });

  test("a future due day is not overdue", () => {
    expect(reviewOverdue("2027-11-14T00:00:00Z", now)).toBe(false);
  });

  test("an absent or unparseable date is never overdue", () => {
    expect(reviewOverdue(undefined, now)).toBe(false);
    expect(reviewOverdue("not a date", now)).toBe(false);
  });
});

describe("patternNoLongerMet (#1867)", () => {
  test("a rule-generated meta-risk with 0 children is flagged", () => {
    expect(
      patternNoLongerMet({ rule_generated: true, child_count: 0, severity: 0 }),
    ).toBe(true);
  });
  test("a meta-risk that still has children is not", () => {
    expect(patternNoLongerMet({ rule_generated: true, child_count: 2 })).toBe(
      false,
    );
  });
  test("a manual risk or a malformed score is not", () => {
    expect(patternNoLongerMet({ likelihood: 1, impact: 1 })).toBe(false);
    expect(patternNoLongerMet({ child_count: 0 })).toBe(false);
    expect(patternNoLongerMet(null)).toBe(false);
    expect(patternNoLongerMet("x")).toBe(false);
  });
});
