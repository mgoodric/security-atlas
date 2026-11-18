// #2138 — controlLiveState: the worst cell wins, as on /controls.

import { describe, expect, it } from "vitest";

import { controlLiveState } from "./control-state";

describe("controlLiveState", () => {
  it("is null for a control with no evaluation", () => {
    expect(controlLiveState([])).toBeNull();
  });

  it("takes the worst result and the worst freshness across cells", () => {
    expect(
      controlLiveState([
        { result: "pass", freshness_status: "fresh" },
        { result: "fail", freshness_status: "fresh" },
        { result: "inconclusive", freshness_status: "stale" },
      ]),
    ).toEqual({ result: "fail", freshness: "stale" });
  });

  it("ranks excepted above pass, so a waiver never reads as passing", () => {
    expect(
      controlLiveState([
        { result: "pass", freshness_status: "fresh" },
        { result: "excepted", freshness_status: "fresh" },
      ])?.result,
    ).toBe("excepted");
  });

  it("ignores a value it does not know", () => {
    expect(
      controlLiveState([
        { result: "pass", freshness_status: "fresh" },
        { result: "future_value", freshness_status: "future_value" },
      ]),
    ).toEqual({ result: "pass", freshness: "fresh" });
  });
});
