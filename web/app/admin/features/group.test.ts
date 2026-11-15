import { describe, expect, it } from "vitest";

import type { FeatureFlag } from "@/lib/api/admin";

import { groupByModule } from "./group";

function flag(over: Partial<FeatureFlag>): FeatureFlag {
  return {
    key: "k",
    enabled: true,
    description: "",
    category: "",
    has_override: false,
    ...over,
  };
}

describe("groupByModule", () => {
  it("nests features under their module's switch, optional before core", () => {
    const groups = groupByModule([
      flag({
        key: "audit.workflow",
        module: "audit_periods",
        module_name: "Audit periods",
        module_core: true,
      }),
      flag({ key: "risk.themes", module: "risk", module_name: "Risk" }),
      flag({
        key: "risk.enabled",
        module: "risk",
        module_name: "Risk",
        module_switch: true,
      }),
      flag({ key: "decisions.log", module: "risk", module_name: "Risk" }),
      flag({
        key: "vendor.claims",
        module: "third_party_risk",
        module_name: "Third-party risk",
      }),
      flag({
        key: "vendor.enabled",
        module: "third_party_risk",
        module_name: "Third-party risk",
        module_switch: true,
      }),
    ]);
    expect(groups.map((g) => g.key)).toEqual([
      "risk",
      "third_party_risk",
      "audit_periods",
    ]);
    const risk = groups[0];
    expect(risk.moduleSwitch?.key).toBe("risk.enabled");
    expect(risk.features.map((f) => f.key)).toEqual([
      "decisions.log",
      "risk.themes",
    ]);
    const audit = groups[2];
    expect(audit.core).toBe(true);
    expect(audit.moduleSwitch).toBeUndefined();
    expect(audit.features.map((f) => f.key)).toEqual(["audit.workflow"]);
  });

  it("puts a flag in no module under Other, last", () => {
    const groups = groupByModule([
      flag({ key: "mystery" }),
      flag({
        key: "risk.enabled",
        module: "risk",
        module_name: "Risk",
        module_switch: true,
      }),
    ]);
    expect(groups.map((g) => g.name)).toEqual(["Risk", "Other"]);
    expect(groups[1].features.map((f) => f.key)).toEqual(["mystery"]);
  });
});
