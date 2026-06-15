// Slice 484 — vitest coverage for the SCF anchor-detail version-option builder
// (web/lib/anchors/version-options.ts). Pure logic, no DOM.

import { describe, expect, test } from "vitest";

import { ALL_CURRENT, buildVersionOptions, pinFor } from "./version-options";

function detailWith(versions: Array<{ framework: string; version: string }>) {
  return {
    requirements: versions.map((v, i) => ({
      requirement: {
        id: `r${i}`,
        framework_version_id: `fv${i}`,
        code: "X",
        text: "",
      },
      framework_version: {
        id: `fv-${v.framework}-${v.version}`,
        framework: v.framework,
        version: v.version,
      },
      strm_type: "equal" as const,
      strength: 1,
    })),
  };
}

describe("buildVersionOptions", () => {
  test("leads with the ALL_CURRENT default", () => {
    const opts = buildVersionOptions(
      detailWith([{ framework: "SOC2", version: "2017" }]),
    );
    expect(opts[0]).toEqual({
      value: ALL_CURRENT,
      label: "All current versions",
    });
  });

  test("one option per distinct framework version, sorted by label", () => {
    const opts = buildVersionOptions(
      detailWith([
        { framework: "SOC2", version: "2017" },
        { framework: "ISO", version: "2022" },
        { framework: "SOC2", version: "2017" }, // duplicate collapses
      ]),
    );
    // [default, ISO — 2022, SOC2 — 2017]
    expect(opts.map((o) => o.label)).toEqual([
      "All current versions",
      "ISO — 2022",
      "SOC2 — 2017",
    ]);
  });

  test("reconstructs slug:version pin value, honoring slugFor override", () => {
    const opts = buildVersionOptions(
      detailWith([{ framework: "SOC 2", version: "2017" }]),
      { "SOC 2": "soc2" },
    );
    expect(opts[1].value).toBe("soc2:2017");
  });

  test("falls back to lowercased framework as slug when no override", () => {
    const opts = buildVersionOptions(
      detailWith([{ framework: "iso", version: "2022" }]),
    );
    expect(opts[1].value).toBe("iso:2022");
  });

  test("empty / null detail yields only the default", () => {
    expect(buildVersionOptions(null).map((o) => o.value)).toEqual([
      ALL_CURRENT,
    ]);
    expect(buildVersionOptions(undefined).map((o) => o.value)).toEqual([
      ALL_CURRENT,
    ]);
    expect(buildVersionOptions(detailWith([])).map((o) => o.value)).toEqual([
      ALL_CURRENT,
    ]);
  });
});

describe("pinFor", () => {
  test("ALL_CURRENT maps to undefined (no pin)", () => {
    expect(pinFor(ALL_CURRENT)).toBeUndefined();
  });

  test("a real value passes through unchanged", () => {
    expect(pinFor("soc2:2017")).toBe("soc2:2017");
  });
});
