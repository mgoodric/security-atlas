import { describe, expect, it } from "vitest";

import type { ControlStateEntry } from "@/lib/api/control-detail";
import {
  evaluationLanded,
  isManualImplementation,
  rollupResult,
} from "@/lib/attest-evaluation";

function entry(over: Partial<ControlStateEntry>): ControlStateEntry {
  return {
    scope_cell_id: null,
    result: "pass",
    freshness_status: "fresh",
    evidence_count_in_window: 1,
    last_observed_at: "2026-11-17T00:00:00Z",
    evaluated_at: "2026-11-17T00:00:00Z",
    freshness_class: "quarterly",
    trigger: "ingest",
    ...over,
  };
}

describe("evaluationLanded", () => {
  const since = "2026-11-17T10:00:00.500Z";

  it("waits while every entry predates the attestation", () => {
    expect(
      evaluationLanded(
        [entry({ evaluated_at: "2026-11-17T10:00:00.499Z" })],
        since,
      ),
    ).toBe(false);
  });

  it("lands on an entry evaluated at or after the attestation", () => {
    expect(evaluationLanded([entry({ evaluated_at: since })], since)).toBe(
      true,
    );
    expect(
      evaluationLanded(
        [
          entry({ evaluated_at: "2026-11-01T00:00:00Z" }),
          entry({ evaluated_at: "2026-11-17T10:00:01Z" }),
        ],
        since,
      ),
    ).toBe(true);
  });

  it("waits while the state has not loaded", () => {
    expect(evaluationLanded(undefined, since)).toBe(false);
    expect(evaluationLanded([], since)).toBe(false);
  });

  it("never waits on an unparseable timestamp", () => {
    expect(evaluationLanded(undefined, "not-a-time")).toBe(true);
  });
});

describe("rollupResult", () => {
  it("is null with no state", () => {
    expect(rollupResult(undefined)).toBeNull();
    expect(rollupResult([])).toBeNull();
  });

  it("fails on any fail", () => {
    expect(
      rollupResult([entry({ result: "pass" }), entry({ result: "fail" })]),
    ).toBe("fail");
  });

  it("follows pass, excepted, inconclusive, na", () => {
    expect(
      rollupResult([
        entry({ result: "inconclusive" }),
        entry({ result: "pass" }),
      ]),
    ).toBe("pass");
    expect(
      rollupResult([entry({ result: "na" }), entry({ result: "excepted" })]),
    ).toBe("excepted");
    expect(
      rollupResult([
        entry({ result: "na" }),
        entry({ result: "inconclusive" }),
      ]),
    ).toBe("inconclusive");
    expect(rollupResult([entry({ result: "na" })])).toBe("na");
  });
});

describe("isManualImplementation", () => {
  it("is true only for attested and periodic manual controls", () => {
    expect(isManualImplementation("manual_attested")).toBe(true);
    expect(isManualImplementation("manual_periodic")).toBe(true);
    expect(isManualImplementation("automated")).toBe(false);
    expect(isManualImplementation(undefined)).toBe(false);
  });
});
