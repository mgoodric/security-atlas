// Slice 660 / #2170 — unit tests for the pure module-gating helpers.

import { describe, expect, test } from "vitest";

import { APIError } from "@/lib/api/base";
import {
  type EffectiveModule,
  FEATURE_DISABLED_MESSAGE,
  gateNavItems,
  isFeatureDisabledError,
  moduleForPage,
  resolveFeatureGate,
} from "@/lib/feature-nav";

type Item = { href: string; label: string };

const NAV: Item[] = [
  { href: "/dashboard", label: "Dashboard" },
  { href: "/board-packs", label: "Board Packs" },
  { href: "/oscal/component-definitions", label: "Vendor Claims" },
  { href: "/questionnaires", label: "Questionnaires" },
  { href: "/settings", label: "Settings" },
];

function mod(
  key: string,
  page: string,
  state: EffectiveModule["state"],
): EffectiveModule {
  return { key, name: key, state, pages: [page] };
}

const board = (s: EffectiveModule["state"]) => mod("board", "/board-packs", s);
const claims = (s: EffectiveModule["state"]) =>
  mod("claims", "/oscal/component-definitions", s);
const questionnaires = (s: EffectiveModule["state"]) =>
  mod("questionnaires", "/questionnaires", s);

describe("gateNavItems", () => {
  test("hides the entries of every switched-off module", () => {
    const out = gateNavItems(NAV, [
      board("switched_off"),
      claims("switched_off"),
      questionnaires("on"),
    ]);
    expect(out.map((i) => i.href)).toEqual([
      "/dashboard",
      "/questionnaires",
      "/settings",
    ]);
  });

  test("renders a module's entry only when it is on", () => {
    const out = gateNavItems(NAV, [
      board("switched_off"),
      claims("on"),
      questionnaires("switched_off"),
    ]);
    expect(out.map((i) => i.href)).toEqual([
      "/dashboard",
      "/oscal/component-definitions",
      "/settings",
    ]);
  });

  test("never gates an entry no module owns (core)", () => {
    const out = gateNavItems([{ href: "/risks", label: "Risks" }], []);
    expect(out).toHaveLength(1);
  });

  test("gates nothing when the modules are unknown", () => {
    expect(gateNavItems(NAV, null)).toHaveLength(NAV.length);
  });
});

describe("moduleForPage", () => {
  const modules = [questionnaires("on")];

  test("a module owns its page and the pages below it", () => {
    expect(moduleForPage(modules, "/questionnaires")?.key).toBe(
      "questionnaires",
    );
    expect(moduleForPage(modules, "/questionnaires/abc")?.key).toBe(
      "questionnaires",
    );
  });

  test("a module does not own a page that only shares a prefix", () => {
    expect(moduleForPage(modules, "/questionnaires-x")).toBeUndefined();
  });

  test("no module when the modules are unknown", () => {
    expect(moduleForPage(null, "/questionnaires")).toBeUndefined();
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

describe("resolveFeatureGate", () => {
  test("module on renders the page for anyone", () => {
    const modules = [board("on")];
    expect(resolveFeatureGate(modules, "/board-packs", true)).toBe("on");
    expect(resolveFeatureGate(modules, "/board-packs", false)).toBe("on");
  });

  test("module off tells an admin where to enable it", () => {
    expect(
      resolveFeatureGate(
        [questionnaires("switched_off")],
        "/questionnaires",
        true,
      ),
    ).toBe("off-admin");
  });

  test("module off tells a non-admin to ask an administrator", () => {
    expect(
      resolveFeatureGate([board("switched_off")], "/board-packs", false),
    ).toBe("off-member");
  });

  test("unknown modules read as off (fail-closed)", () => {
    expect(resolveFeatureGate(null, "/board-packs", false)).toBe("off-member");
    expect(resolveFeatureGate([], "/board-packs", true)).toBe("off-admin");
  });
});
