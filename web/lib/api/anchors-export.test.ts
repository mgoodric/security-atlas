// Slice 174 — unit tests for the anchors-export URL builder.

import { describe, expect, test } from "vitest";

import {
  ANCHORS_EXPORT_FORMATS,
  ANCHORS_EXPORT_FORMAT_LABELS,
  buildAnchorsExportURL,
} from "./anchors-export";

describe("buildAnchorsExportURL", () => {
  test("emits the canonical BFF path with the format query param", () => {
    expect(buildAnchorsExportURL("csv")).toBe("/api/anchors/export?format=csv");
    expect(buildAnchorsExportURL("json")).toBe(
      "/api/anchors/export?format=json",
    );
    expect(buildAnchorsExportURL("xlsx")).toBe(
      "/api/anchors/export?format=xlsx",
    );
  });
});

describe("ANCHORS_EXPORT_FORMATS", () => {
  test("matches the backend's internal/export.AllFormats list", () => {
    expect([...ANCHORS_EXPORT_FORMATS]).toEqual(["csv", "json", "xlsx"]);
  });
});

describe("ANCHORS_EXPORT_FORMAT_LABELS", () => {
  test("has a human-readable label for each format", () => {
    expect(ANCHORS_EXPORT_FORMAT_LABELS).toEqual({
      csv: "CSV",
      json: "JSON",
      xlsx: "XLSX",
    });
  });
});
