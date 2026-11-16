// #2172 — unit tests for the module-access helpers.

import { describe, expect, test } from "vitest";

import type { FeatureFlag } from "@/lib/api/admin";
import {
  accessApplies,
  accessSummary,
  optionalModules,
} from "@/lib/admin/module-access";

const flag = (f: Partial<FeatureFlag>): FeatureFlag => ({
  key: "k",
  enabled: true,
  description: "",
  category: "",
  has_override: false,
  ...f,
});

describe("optionalModules", () => {
  test("lists each optional module's switch, not features or core", () => {
    const flags = [
      flag({
        key: "risk.enabled",
        module: "risk",
        module_name: "Risk",
        module_switch: true,
      }),
      flag({
        key: "risk.themes",
        module: "risk",
        module_name: "Risk",
        module_switch: false,
      }),
      flag({
        key: "controls.bundles",
        module: "controls",
        module_name: "Controls",
        module_core: true,
      }),
      flag({
        key: "policy.enabled",
        module: "policies",
        module_name: "Policies",
        module_switch: true,
      }),
      flag({ key: "legacy" }),
    ];
    expect(optionalModules(flags)).toEqual([
      { key: "risk", name: "Risk" },
      { key: "policies", name: "Policies" },
    ]);
  });
});

describe("accessApplies", () => {
  test("admins and auditors are never narrowed", () => {
    expect(accessApplies(["viewer"])).toBe(true);
    expect(accessApplies(["grc_engineer", "control_owner"])).toBe(true);
    expect(accessApplies(["viewer", "admin"])).toBe(false);
    expect(accessApplies(["auditor"])).toBe(false);
  });
});

describe("accessSummary", () => {
  const modules = [
    { key: "policies", name: "Policies" },
    { key: "questionnaires", name: "Questionnaires" },
  ];
  test("null is all modules; empty is core only; else names", () => {
    expect(accessSummary(null, modules)).toBe("All modules");
    expect(accessSummary(undefined, modules)).toBe("All modules");
    expect(accessSummary([], modules)).toBe("Core modules only");
    expect(accessSummary(["policies", "questionnaires"], modules)).toBe(
      "Policies, Questionnaires",
    );
  });
});
