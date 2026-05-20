// Slice 137 — vitest coverage for the controls-export URL builder.

import { describe, expect, test } from "vitest";

import {
  CONTROLS_EXPORT_FORMATS,
  CONTROLS_EXPORT_FORMAT_LABELS,
  buildControlsExportURL,
} from "./controls-export";

describe("buildControlsExportURL", () => {
  test("CSV format produces /api/controls/export?format=csv", () => {
    expect(buildControlsExportURL("csv")).toBe(
      "/api/controls/export?format=csv",
    );
  });

  test("JSON format produces /api/controls/export?format=json", () => {
    expect(buildControlsExportURL("json")).toBe(
      "/api/controls/export?format=json",
    );
  });

  test("XLSX format produces /api/controls/export?format=xlsx", () => {
    expect(buildControlsExportURL("xlsx")).toBe(
      "/api/controls/export?format=xlsx",
    );
  });

  test("format constants match the backend wire format", () => {
    // Defense: keeps the frontend + backend format enums in lockstep
    // with internal/export.AllFormats.
    expect(CONTROLS_EXPORT_FORMATS).toEqual(["csv", "json", "xlsx"]);
  });

  test("labels cover every format", () => {
    for (const fmt of CONTROLS_EXPORT_FORMATS) {
      expect(CONTROLS_EXPORT_FORMAT_LABELS[fmt]).toBeDefined();
      expect(CONTROLS_EXPORT_FORMAT_LABELS[fmt].length).toBeGreaterThan(0);
    }
  });
});
