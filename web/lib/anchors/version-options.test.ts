// Slice 484 — vitest coverage for the SCF anchor-detail version-option builder
// (web/lib/anchors/version-options.ts). Pure logic, no DOM.

import { describe, expect, test } from "vitest";

import { ALL_CURRENT, buildVersionOptions, pinFor } from "./version-options";

function detailWith(
  versions: Array<{ slug?: string; framework: string; version: string }>,
) {
  return {
    requirements: versions.map((v, i) => ({
      edge_id: `e${i}`,
      requirement_id: `r${i}`,
      code: "X",
      title: "",
      framework_slug: v.slug ?? v.framework.toLowerCase(),
      framework_name: v.framework,
      framework_version: v.version,
      framework_version_id: `fv-${v.framework}-${v.version}`,
      framework_version_status: "current",
      relationship_type: "equal",
      strength: 1,
      coverage: null,
      source_attribution: "scf-strm",
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

  test("the pin value is the row's framework_slug:version", () => {
    const opts = buildVersionOptions(
      detailWith([{ slug: "soc2", framework: "SOC 2", version: "2017" }]),
    );
    expect(opts[1]).toEqual({ value: "soc2:2017", label: "SOC 2 — 2017" });
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
