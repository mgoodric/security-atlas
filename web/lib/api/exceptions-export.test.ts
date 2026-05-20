// Slice 177 — vitest coverage for the exceptions-export URL builder.

import { describe, expect, test } from "vitest";

import {
  EXCEPTIONS_EXPORT_FORMATS,
  EXCEPTIONS_EXPORT_FORMAT_LABELS,
  buildExceptionsExportURL,
} from "./exceptions-export";

describe("buildExceptionsExportURL", () => {
  test("CSV format produces /api/admin/exceptions/export?format=csv", () => {
    expect(buildExceptionsExportURL("csv")).toBe(
      "/api/admin/exceptions/export?format=csv",
    );
  });

  test("JSON format produces /api/admin/exceptions/export?format=json", () => {
    expect(buildExceptionsExportURL("json")).toBe(
      "/api/admin/exceptions/export?format=json",
    );
  });

  test("XLSX format produces /api/admin/exceptions/export?format=xlsx", () => {
    expect(buildExceptionsExportURL("xlsx")).toBe(
      "/api/admin/exceptions/export?format=xlsx",
    );
  });

  test("format constants match the backend wire format", () => {
    // Defense: keeps the frontend + backend format enums in lockstep
    // with internal/export.AllFormats and the slice 138 export handler.
    expect(EXCEPTIONS_EXPORT_FORMATS).toEqual(["csv", "json", "xlsx"]);
  });

  test("labels cover every format", () => {
    for (const fmt of EXCEPTIONS_EXPORT_FORMATS) {
      expect(EXCEPTIONS_EXPORT_FORMAT_LABELS[fmt]).toBeDefined();
      expect(EXCEPTIONS_EXPORT_FORMAT_LABELS[fmt].length).toBeGreaterThan(0);
    }
  });
});
