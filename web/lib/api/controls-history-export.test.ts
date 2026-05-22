// Slice 175 — vitest coverage for the controls-history-export URL builder.

import { describe, expect, test } from "vitest";

import {
  CONTROLS_HISTORY_EXPORT_FORMATS,
  CONTROLS_HISTORY_EXPORT_FORMAT_LABELS,
  buildControlsHistoryExportURL,
} from "./controls-history-export";

describe("buildControlsHistoryExportURL", () => {
  test("CSV format produces /api/controls/history/export?format=csv", () => {
    expect(buildControlsHistoryExportURL("csv")).toBe(
      "/api/controls/history/export?format=csv",
    );
  });

  test("JSON format produces /api/controls/history/export?format=json", () => {
    expect(buildControlsHistoryExportURL("json")).toBe(
      "/api/controls/history/export?format=json",
    );
  });

  test("XLSX format produces /api/controls/history/export?format=xlsx", () => {
    expect(buildControlsHistoryExportURL("xlsx")).toBe(
      "/api/controls/history/export?format=xlsx",
    );
  });

  test("format constants match the backend wire format", () => {
    expect(CONTROLS_HISTORY_EXPORT_FORMATS).toEqual(["csv", "json", "xlsx"]);
  });

  test("format labels are human-readable strings", () => {
    expect(CONTROLS_HISTORY_EXPORT_FORMAT_LABELS.csv).toBe("CSV");
    expect(CONTROLS_HISTORY_EXPORT_FORMAT_LABELS.json).toBe("JSON");
    expect(CONTROLS_HISTORY_EXPORT_FORMAT_LABELS.xlsx).toBe("XLSX");
  });
});
