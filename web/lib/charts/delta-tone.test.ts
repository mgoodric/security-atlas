import { describe, expect, it } from "vitest";

import {
  deltaAriaLabel,
  deltaArrow,
  deltaTone,
  deltaToneClass,
  type DeltaTone,
} from "./delta-tone";

// The matrix: value above/below target x change improving/worsening x
// direction higher/lower-is-better. The expected tone depends on the
// direction and the movement, never on the sign alone.
describe("deltaTone matrix (#2095)", () => {
  const cases: Array<{
    direction: "higher_is_better" | "lower_is_better";
    position: "above" | "below";
    delta: number;
    want: DeltaTone;
  }> = [
    // higher_is_better: rising improves wherever the value sits.
    {
      direction: "higher_is_better",
      position: "above",
      delta: 2,
      want: "improving",
    },
    {
      direction: "higher_is_better",
      position: "above",
      delta: -2,
      want: "worsening",
    },
    {
      direction: "higher_is_better",
      position: "below",
      delta: 2,
      want: "improving",
    },
    {
      direction: "higher_is_better",
      position: "below",
      delta: -2,
      want: "worsening",
    },
    // lower_is_better: a positive delta is the bad one.
    {
      direction: "lower_is_better",
      position: "above",
      delta: -2,
      want: "improving",
    },
    {
      direction: "lower_is_better",
      position: "above",
      delta: 2,
      want: "worsening",
    },
    {
      direction: "lower_is_better",
      position: "below",
      delta: -2,
      want: "improving",
    },
    {
      direction: "lower_is_better",
      position: "below",
      delta: 2,
      want: "worsening",
    },
  ];
  for (const c of cases) {
    it(`${c.direction}, value ${c.position} target, delta ${c.delta} -> ${c.want}`, () => {
      const value = c.position === "above" ? 90 : 60;
      expect(
        deltaTone(value, c.delta, {
          target_value: "75",
          direction: c.direction,
        }),
      ).toBe(c.want);
    });
  }

  it("a rising lower-is-better metric is not green despite the + sign", () => {
    const tone = deltaTone(10, 3, {
      target_value: "5",
      direction: "lower_is_better",
    });
    expect(tone).toBe("worsening");
    expect(deltaToneClass(tone)).toContain("destructive");
  });
});

describe("deltaTone target_is_better", () => {
  const target = { target_value: 50, direction: "target_is_better" };
  it("moving closer from above improves", () => {
    expect(deltaTone(55, -5, target)).toBe("improving");
  });
  it("moving closer from below improves", () => {
    expect(deltaTone(45, 5, target)).toBe("improving");
  });
  it("moving away worsens", () => {
    expect(deltaTone(60, 5, target)).toBe("worsening");
  });
  it("same distance either side is flat", () => {
    expect(deltaTone(55, 10, target)).toBe("flat");
  });
});

describe("deltaTone without a target", () => {
  it.each([
    [null],
    [{ direction: "higher_is_better" }],
    [{ target_value: "", direction: "higher_is_better" }],
    [{ target_value: "nope", direction: "higher_is_better" }],
  ])("is neutral for %j", (target) => {
    expect(deltaTone(80, 5, target)).toBe("neutral");
    expect(deltaTone(80, -5, target)).toBe("neutral");
    expect(deltaToneClass("neutral")).toBe("text-muted-foreground");
  });

  it.each([
    [NaN, 1],
    [1, NaN],
    [Infinity, 1],
    [1, -Infinity],
  ])("a non-finite value or delta (%s, %s) is neutral", (value, delta) => {
    expect(
      deltaTone(value, delta, {
        target_value: 1,
        direction: "higher_is_better",
      }),
    ).toBe("neutral");
  });

  it("an unknown direction is neutral", () => {
    expect(deltaTone(1, 1, { target_value: 2, direction: "sideways" })).toBe(
      "neutral",
    );
  });
});

describe("deltaTone zero change", () => {
  it("is flat with a target", () => {
    expect(
      deltaTone(80, 0, { target_value: 75, direction: "higher_is_better" }),
    ).toBe("flat");
  });
});

describe("non-colour cues", () => {
  it("arrows follow the sign", () => {
    expect(deltaArrow(1)).toBe("↑");
    expect(deltaArrow(-1)).toBe("↓");
    expect(deltaArrow(0)).toBe("→");
  });
  it("aria labels name the verdict", () => {
    expect(deltaAriaLabel("improving", "+2.0")).toBe(
      "+2.0, improving against target",
    );
    expect(deltaAriaLabel("worsening", "-2.0")).toBe(
      "-2.0, worsening against target",
    );
    expect(deltaAriaLabel("flat", "0.0")).toBe("0.0, unchanged");
    expect(deltaAriaLabel("neutral", "+2.0")).toBe("+2.0, no target set");
  });
});
