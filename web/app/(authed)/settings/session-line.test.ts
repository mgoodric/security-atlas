// Slice 162 — vitest for the session-line render helper.
//
// Covers the matrix of presence / absence on the four augmented fields:
//   - all present (the mockup-faithful render)
//   - none present (pre-migration row; line empty)
//   - partial — UA only, IP only, geo only
//   - geo edge cases (country without city, city without country)
//   - UA truncation at the UI cap
//
// P0-162-1: every "absent" case must produce no fabricated placeholder.

import { describe, expect, it } from "vitest";

import {
  UA_DISPLAY_MAX,
  geoLine,
  sessionLine,
  truncateUA,
} from "./session-line";

describe("sessionLine — full coverage", () => {
  it("renders all four fields joined by middot", () => {
    expect(
      sessionLine({
        user_agent: "Mozilla/5.0 Safari",
        ip_address: "192.0.2.18",
        geo_country: "US",
        geo_city: "San Francisco",
      }),
    ).toBe("Mozilla/5.0 Safari · 192.0.2.18 · San Francisco, US");
  });

  it("returns an empty string when every field is absent (P0-162-1)", () => {
    expect(sessionLine({})).toBe("");
  });

  it("returns an empty string when every field is empty string (P0-162-1)", () => {
    expect(
      sessionLine({
        user_agent: "",
        ip_address: "",
        geo_country: "",
        geo_city: "",
      }),
    ).toBe("");
  });

  it("renders only the user-agent when alone", () => {
    expect(sessionLine({ user_agent: "Firefox/123" })).toBe("Firefox/123");
  });

  it("renders only the IP when alone", () => {
    expect(sessionLine({ ip_address: "203.0.113.42" })).toBe("203.0.113.42");
  });

  it("renders only the geo when alone (city + country)", () => {
    expect(sessionLine({ geo_country: "GB", geo_city: "London" })).toBe(
      "London, GB",
    );
  });

  it("renders UA + IP without geo when geo is absent (partial)", () => {
    expect(sessionLine({ user_agent: "Mozilla", ip_address: "10.0.0.1" })).toBe(
      "Mozilla · 10.0.0.1",
    );
  });

  it("renders IP + geo without UA (partial)", () => {
    expect(
      sessionLine({
        ip_address: "10.0.0.1",
        geo_country: "DE",
        geo_city: "Berlin",
      }),
    ).toBe("10.0.0.1 · Berlin, DE");
  });

  it("renders country alone when city is absent (geo edge)", () => {
    expect(sessionLine({ geo_country: "JP" })).toBe("JP");
  });

  it("renders city alone when country is absent (geo edge)", () => {
    expect(sessionLine({ geo_city: "Tokyo" })).toBe("Tokyo");
  });

  it("treats whitespace-only fields as absent (no garbage line)", () => {
    expect(
      sessionLine({
        user_agent: "   ",
        ip_address: "\t",
        geo_city: " ",
      }),
    ).toBe("");
  });
});

describe("truncateUA", () => {
  it("passes a short UA through unchanged", () => {
    const ua = "Firefox/123";
    expect(truncateUA(ua)).toBe(ua);
  });

  it("truncates a long UA to UA_DISPLAY_MAX and appends ellipsis", () => {
    const long = "X".repeat(UA_DISPLAY_MAX + 30);
    const got = truncateUA(long);
    expect(got.length).toBe(UA_DISPLAY_MAX);
    expect(got.endsWith("…")).toBe(true);
    expect(long.startsWith(got.slice(0, UA_DISPLAY_MAX - 1))).toBe(true);
  });

  it("does not truncate at the exact boundary", () => {
    const exact = "Y".repeat(UA_DISPLAY_MAX);
    expect(truncateUA(exact)).toBe(exact);
  });
});

describe("geoLine", () => {
  it("returns empty string when both absent", () => {
    expect(geoLine(undefined, undefined)).toBe("");
    expect(geoLine("", "")).toBe("");
  });

  it("returns country alone when city absent", () => {
    expect(geoLine("US", undefined)).toBe("US");
    expect(geoLine("US", "")).toBe("US");
  });

  it("returns city alone when country absent", () => {
    expect(geoLine(undefined, "Paris")).toBe("Paris");
    expect(geoLine("", "Paris")).toBe("Paris");
  });

  it("returns 'City, Country' when both present", () => {
    expect(geoLine("FR", "Paris")).toBe("Paris, FR");
  });
});
