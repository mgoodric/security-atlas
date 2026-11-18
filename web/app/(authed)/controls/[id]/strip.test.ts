// #2140 — the control-detail state strip's derived values.

import { describe, expect, it } from "vitest";

import type { ActionPlanRef } from "@/lib/api/action-plans";
import type { ControlStateEntry } from "@/lib/api/control-detail";
import type { Exception } from "@/lib/api/exceptions";

import {
  activeExceptions,
  exceptionsForControl,
  isExcepted,
  openActionPlans,
  worstFreshness,
} from "./strip";

function cell(result: string, freshness: string): ControlStateEntry {
  return {
    scope_cell_id: null,
    result,
    freshness_status: freshness,
    evidence_count_in_window: 1,
    last_observed_at: null,
    evaluated_at: "2026-11-10T00:00:00Z",
    freshness_class: "daily",
    trigger: "scheduled",
  };
}

describe("worstFreshness", () => {
  it("is null without state", () => {
    expect(worstFreshness(undefined)).toBeNull();
    expect(worstFreshness([])).toBeNull();
  });

  it("reports the stalest cell", () => {
    expect(worstFreshness([cell("pass", "fresh"), cell("fail", "stale")])).toBe(
      "stale",
    );
    expect(
      worstFreshness([cell("pass", "expired"), cell("pass", "stale")]),
    ).toBe("expired");
    expect(
      worstFreshness([cell("pass", "fresh"), cell("pass", "no_evidence")]),
    ).toBe("no_evidence");
  });

  it("never lets an unknown value hide a known one", () => {
    expect(worstFreshness([cell("pass", "weird"), cell("pass", "fresh")])).toBe(
      "fresh",
    );
    expect(worstFreshness([cell("pass", "weird")])).toBeNull();
  });
});

describe("isExcepted", () => {
  it("is true when any cell is excepted", () => {
    expect(isExcepted([cell("pass", "fresh"), cell("excepted", "fresh")])).toBe(
      true,
    );
    expect(isExcepted([cell("pass", "fresh")])).toBe(false);
    expect(isExcepted(undefined)).toBe(false);
  });
});

describe("exceptionsForControl", () => {
  it("keeps only this control's exceptions, case-insensitively", () => {
    const ex = (id: string, control_id: string) =>
      ({ id, control_id }) as Exception;
    expect(
      exceptionsForControl(
        [ex("a", "AAAA-1"), ex("b", "bbbb-2"), ex("c", "aaaa-1")],
        "aaaa-1",
      ).map((e) => e.id),
    ).toEqual(["a", "c"]);
    expect(exceptionsForControl(undefined, "aaaa-1")).toEqual([]);
  });
});

describe("activeExceptions", () => {
  it("keeps only the active exceptions", () => {
    const ex = (id: string, status: Exception["status"]) =>
      ({ id, status }) as Exception;
    expect(
      activeExceptions([
        ex("a", "active"),
        ex("b", "requested"),
        ex("c", "expired"),
      ]).map((e) => e.id),
    ).toEqual(["a"]);
    expect(activeExceptions(undefined)).toEqual([]);
  });
});

describe("openActionPlans", () => {
  it("drops completed and verified plans", () => {
    const plan = (id: string, status: ActionPlanRef["status"]) => ({
      id,
      title: id,
      status,
    });
    expect(
      openActionPlans([
        plan("a", "draft"),
        plan("b", "in_progress"),
        plan("c", "blocked"),
        plan("d", "completed"),
        plan("e", "verified"),
      ]).map((p) => p.id),
    ).toEqual(["a", "b", "c"]);
    expect(openActionPlans(undefined)).toEqual([]);
  });
});
