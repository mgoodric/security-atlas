import { describe, expect, it } from "vitest";

import { frameworkLabel } from "./framework-label";

describe("frameworkLabel (#2083)", () => {
  it.each([
    ["ISO/IEC 27001", "2022", "ISO/IEC 27001:2022"],
    ["ISO 27001", "2013", "ISO 27001:2013"],
    ["ISO/IEC 27001:2022", "2022", "ISO/IEC 27001:2022"],
    ["NIST CSF", "2.0", "NIST CSF 2.0"],
    ["NIST CSF v2.0", "2.0", "NIST CSF v2.0"],
    ["SCF", "2025.2", "SCF 2025.2"],
    ["SCF 2025.2", "2025.2", "SCF 2025.2"],
    ["PCI DSS", "4.0.1", "PCI DSS 4.0.1"],
    ["SOC 2", "", "SOC 2"],
    ["SOC 2", null, "SOC 2"],
    ["  SOC 2  ", " 2017 ", "SOC 2 2017"],
  ])("%s + %s → %s", (name, version, want) => {
    expect(frameworkLabel(name, version)).toBe(want);
  });

  it("never shows a bare version", () => {
    expect(frameworkLabel("", "2022")).toBe("Version 2022");
    expect(frameworkLabel(undefined, undefined)).toBe("—");
  });

  it("does not mistake a version that is only a suffix of a number", () => {
    // "PCI DSS 14.0" does not already carry version "4.0".
    expect(frameworkLabel("PCI DSS 14.0", "4.0")).toBe("PCI DSS 14.0 4.0");
  });
});
