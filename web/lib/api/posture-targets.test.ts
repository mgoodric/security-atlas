// #2136 — the posture-target helpers: who the tile offers "Set target" to,
// how a tile is graded against its target, and the client-side check that
// mirrors the platform's validation.

import { describe, expect, test } from "vitest";

import {
  canSetPostureTarget,
  postureTargetError,
  postureTileColor,
  type PostureTarget,
} from "./posture-targets";

const higher: PostureTarget = {
  framework_version_id: "fv",
  target_value: 90,
  warning_threshold: 80,
  critical_threshold: 60,
  direction: "higher_is_better",
};

describe("canSetPostureTarget", () => {
  test.each([
    [{ is_admin: true }, true],
    [{ owner_roles: ["admin"] }, true],
    [{ owner_roles: ["grc_engineer"] }, true],
    [{ owner_roles: ["viewer"] }, false],
    [{ owner_roles: ["auditor"] }, false],
    [{ owner_roles: ["control_owner"] }, false],
    [{}, false],
    [undefined, false],
  ])("%j -> %s", (me, want) => {
    expect(canSetPostureTarget(me)).toBe(want);
  });
});

describe("postureTileColor", () => {
  test("no target is neutral, whatever the coverage", () => {
    expect(postureTileColor(0, null)).toBe("neutral");
    expect(postureTileColor(100, undefined)).toBe("neutral");
  });

  test("no coverage (scope not set) is neutral", () => {
    expect(postureTileColor(null, higher)).toBe("neutral");
  });

  test("higher_is_better grades by the thresholds", () => {
    expect(postureTileColor(95, higher)).toBe("green");
    expect(postureTileColor(90, higher)).toBe("green");
    expect(postureTileColor(85, higher)).toBe("yellow");
    expect(postureTileColor(70, higher)).toBe("red");
    expect(postureTileColor(60, higher)).toBe("red");
  });

  test("a target below the value is never red", () => {
    expect(
      postureTileColor(50, {
        ...higher,
        target_value: 40,
        warning_threshold: 30,
        critical_threshold: 20,
      }),
    ).toBe("green");
  });

  test("lower_is_better grades the other way", () => {
    const lower = {
      ...higher,
      target_value: 10,
      warning_threshold: 20,
      critical_threshold: 40,
      direction: "lower_is_better",
    };
    expect(postureTileColor(5, lower)).toBe("green");
    expect(postureTileColor(15, lower)).toBe("yellow");
    expect(postureTileColor(50, lower)).toBe("red");
  });
});

describe("postureTargetError", () => {
  const base = {
    target_value: 90,
    warning_threshold: 80,
    critical_threshold: 60,
    direction: "higher_is_better",
  };

  test("an ordered target passes", () => {
    expect(postureTargetError(base)).toBeNull();
  });

  test.each([
    [{ ...base, warning_threshold: 95 }, /critical ≤ warning ≤ target/],
    [{ ...base, direction: "lower_is_better" }, /target ≤ warning ≤ critical/],
    [
      {
        target_value: 50,
        warning_threshold: 30,
        critical_threshold: 55,
        direction: "target_is_better",
      },
      /no further from the target/,
    ],
    [{ ...base, target_value: 101 }, /between 0 and 100/],
    [{ ...base, critical_threshold: -1 }, /between 0 and 100/],
    [{ ...base, target_value: Number.NaN }, /between 0 and 100/],
    [{ ...base, target_value: 90.125 }, /two decimal places/],
    [{ ...base, direction: "" }, /Choose a direction/],
  ])("%j is refused", (input, message) => {
    expect(postureTargetError(input)).toMatch(message);
  });
});
