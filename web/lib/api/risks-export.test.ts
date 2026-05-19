// Slice 136 — vitest coverage for the risks-export URL builder.

import { describe, expect, test } from "vitest";

import {
  RISK_EXPORT_FORMATS,
  RISK_EXPORT_FORMAT_LABELS,
  buildRiskExportURL,
} from "./risks-export";

describe("buildRiskExportURL", () => {
  test("CSV format produces /api/risks/export?format=csv", () => {
    expect(buildRiskExportURL("csv")).toBe("/api/risks/export?format=csv");
  });

  test("JSON format produces /api/risks/export?format=json", () => {
    expect(buildRiskExportURL("json")).toBe("/api/risks/export?format=json");
  });

  test("XLSX format produces /api/risks/export?format=xlsx", () => {
    expect(buildRiskExportURL("xlsx")).toBe("/api/risks/export?format=xlsx");
  });

  test("format constants match the backend wire format", () => {
    // Defense: keeps the frontend + backend format enums in lockstep.
    expect(RISK_EXPORT_FORMATS).toEqual(["csv", "json", "xlsx"]);
  });

  test("labels cover every format", () => {
    for (const fmt of RISK_EXPORT_FORMATS) {
      expect(RISK_EXPORT_FORMAT_LABELS[fmt]).toBeDefined();
      expect(RISK_EXPORT_FORMAT_LABELS[fmt].length).toBeGreaterThan(0);
    }
  });
});
