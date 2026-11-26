import { describe, expect, it } from "vitest";

import { formatValue } from "@/components/dashboards-metrics/format";

import { formatDateTick, fractionBounds, linearAxis, timeAxis } from "./axes";

const DAY = 24 * 60 * 60 * 1000;

describe("linearAxis", () => {
  it("widens to round steps that cover the data", () => {
    const a = linearAxis(0.62, 0.91);
    expect(a.min).toBeLessThanOrEqual(0.62);
    expect(a.max).toBeGreaterThanOrEqual(0.91);
    expect(a.ticks[0]).toBe(a.min);
    expect(a.ticks[a.ticks.length - 1]).toBeCloseTo(a.max);
    expect(a.ticks).toEqual([0.6, 0.7, 0.8, 0.9, 1]);
  });

  it("fits a flat series with a band around it", () => {
    const a = linearAxis(42, 42);
    expect(a.min).toBeLessThan(42);
    expect(a.max).toBeGreaterThan(42);
  });

  it("fits a flat zero series", () => {
    const a = linearAxis(0, 0);
    expect(a.min).toBeLessThan(0);
    expect(a.max).toBeGreaterThan(0);
  });

  it("keeps a flat 1.0 fraction series inside [0, 1] when bounded", () => {
    const a = linearAxis(1, 1, 5, [0, 1]);
    expect(a.max).toBe(1);
    expect(a.min).toBeLessThan(1);
    expect(a.min).toBeGreaterThanOrEqual(0);
    expect(Math.max(...a.ticks)).toBe(1);
  });

  it("keeps a flat 0 fraction series inside [0, 1] when bounded", () => {
    const a = linearAxis(0, 0, 5, [0, 1]);
    expect(a.min).toBe(0);
    expect(a.max).toBeGreaterThan(0);
    expect(a.max).toBeLessThanOrEqual(1);
  });

  it("orders a reversed range", () => {
    const a = linearAxis(10, 0);
    expect(a.min).toBe(0);
    expect(a.max).toBe(10);
  });

  it("falls back on non-finite input", () => {
    expect(linearAxis(NaN, 1)).toEqual({ min: 0, max: 1, ticks: [0, 0.5, 1] });
  });
});

describe("fractionBounds", () => {
  it("bounds a fraction-valued percent series", () => {
    expect(fractionBounds("percent", [0.5, 1])).toEqual([0, 1]);
    expect(fractionBounds("ratio", [0, 0.2])).toEqual([0, 1]);
  });
  it("leaves scaled percentages and other units unbounded", () => {
    expect(fractionBounds("percent", [45, 90])).toBeUndefined();
    expect(fractionBounds("count", [0.5])).toBeUndefined();
  });

  // Review B1 on #2345: a flat 1.0 series used to label its top ticks
  // "1.1%" above "100.0%".
  it("a flat 1.0 percent series has no tick above 100%", () => {
    const a = linearAxis(1, 1, 5, fractionBounds("percent", [1]));
    const labels = a.ticks.map((v) => formatValue(v, "percent"));
    expect(labels).not.toContain("1.1%");
    expect(labels[labels.length - 1]).toBe("100.0%");
    expect(Math.max(...a.ticks)).toBe(1);
  });
});

describe("timeAxis", () => {
  it("includes both endpoints", () => {
    const a = timeAxis(0, 90 * DAY, 4);
    expect(a.ticks).toHaveLength(4);
    expect(a.ticks[0]).toBe(0);
    expect(a.ticks[3]).toBe(90 * DAY);
  });

  it("gives a single point a one-day window", () => {
    const a = timeAxis(5 * DAY, 5 * DAY);
    expect(a.max - a.min).toBe(DAY);
  });
});

describe("formatDateTick", () => {
  const t = Date.UTC(2026, 10, 3, 14, 5);
  it("shows the date for long spans", () => {
    expect(formatDateTick(t, 30 * DAY)).toBe("Nov 3");
  });
  it("adds the hour for short spans", () => {
    expect(formatDateTick(t, DAY)).toBe("Nov 3 14:05");
  });
});
