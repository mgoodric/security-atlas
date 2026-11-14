// #2063 — the drift panel's headline counts gains and losses from the same
// lists it renders, so it cannot pair a net gain with "nothing flipped out".
// The panel component itself is covered by the Playwright e2e
// (`web/e2e/dashboard.spec.ts`); vitest here is node-only.

import { describe, expect, test } from "vitest";

import type { DriftReport } from "@/lib/api/dashboard";

import { summarizeDrift } from "./recent-drift-summary";

function report(gains: string[], losses: string[]): DriftReport {
  return {
    since: "2026-11-07",
    through: "2026-11-14",
    delta: gains.length - losses.length,
    started_passing_count: gains.length,
    started_passing: gains.map((control_id) => ({ control_id })),
    flipped_out_count: losses.length,
    flipped_out: losses.map((control_id) => ({
      control_id,
      last_passing: "2026-11-10",
      current_result: "not_passing",
    })),
  };
}

describe("summarizeDrift", () => {
  test("3 gains and 1 loss reads as both counts, not a net delta", () => {
    expect(summarizeDrift(report(["a", "b", "c"], ["d"]))).toEqual({
      gains: 3,
      losses: 1,
      label: "+3 started passing · 1 stopped passing",
    });
  });

  test("gains with no losses still names both sides", () => {
    expect(summarizeDrift(report(["a", "b"], [])).label).toBe(
      "+2 started passing · 0 stopped passing",
    );
  });

  test("no change has no headline (the panel says so in one line)", () => {
    expect(summarizeDrift(report([], []))).toEqual({
      gains: 0,
      losses: 0,
      label: null,
    });
  });

  test("an upstream without started_passing counts zero gains", () => {
    const old = report([], ["d"]) as Partial<DriftReport>;
    delete old.started_passing;
    expect(summarizeDrift(old as DriftReport).label).toBe(
      "+0 started passing · 1 stopped passing",
    );
  });
});
