// Slice 660 — unit tests for the pure nav-gating helpers.

import { describe, expect, test } from "vitest";

import { APIError } from "@/lib/api/base";
import {
  FEATURE_DISABLED_MESSAGE,
  gateNavItems,
  isFeatureDisabledError,
  NAV_FEATURE_GATES,
} from "@/lib/feature-nav";

type Item = { href: string; label: string };

const NAV: Item[] = [
  { href: "/dashboard", label: "Dashboard" },
  { href: "/board-packs", label: "Board Packs" },
  { href: "/oscal/component-definitions", label: "Vendor Claims" },
  { href: "/settings", label: "Settings" },
];

describe("gateNavItems", () => {
  test("hides both gated items when both flags are off (Seed default)", () => {
    const out = gateNavItems(NAV, {
      "oscal.export": false,
      "board.reporting": false,
    });
    const hrefs = out.map((i) => i.href);
    expect(hrefs).toEqual(["/dashboard", "/settings"]);
  });

  test("hides a gated item when its flag key is absent (fail-closed)", () => {
    // Empty map: a missing key reads as off.
    const out = gateNavItems(NAV, {});
    expect(out.map((i) => i.href)).toEqual(["/dashboard", "/settings"]);
  });

  test("renders a gated item only when its flag is explicitly true", () => {
    const out = gateNavItems(NAV, {
      "oscal.export": true,
      "board.reporting": false,
    });
    expect(out.map((i) => i.href)).toEqual([
      "/dashboard",
      "/oscal/component-definitions",
      "/settings",
    ]);
  });

  test("renders both gated items when both flags are on", () => {
    const out = gateNavItems(NAV, {
      "oscal.export": true,
      "board.reporting": true,
    });
    expect(out.map((i) => i.href)).toEqual([
      "/dashboard",
      "/board-packs",
      "/oscal/component-definitions",
      "/settings",
    ]);
  });

  test("never gates an item with no mapping", () => {
    const out = gateNavItems([{ href: "/risks", label: "Risks" }], {});
    expect(out).toHaveLength(1);
  });

  test("NAV_FEATURE_GATES binds the two slice 660 modules", () => {
    expect(NAV_FEATURE_GATES["/oscal/component-definitions"]).toBe(
      "oscal.export",
    );
    expect(NAV_FEATURE_GATES["/board-packs"]).toBe("board.reporting");
  });
});

describe("isFeatureDisabledError", () => {
  test("true for a 404 APIError with the feature-disabled message", () => {
    expect(
      isFeatureDisabledError(new APIError(404, FEATURE_DISABLED_MESSAGE)),
    ).toBe(true);
  });

  test("false for a 404 with a different message", () => {
    expect(isFeatureDisabledError(new APIError(404, "not found"))).toBe(false);
  });

  test("false for the feature-disabled message at a non-404 status", () => {
    expect(
      isFeatureDisabledError(new APIError(403, FEATURE_DISABLED_MESSAGE)),
    ).toBe(false);
  });

  test("false for a plain Error or non-error value", () => {
    expect(isFeatureDisabledError(new Error("feature disabled"))).toBe(false);
    expect(isFeatureDisabledError(null)).toBe(false);
    expect(isFeatureDisabledError(undefined)).toBe(false);
  });
});
