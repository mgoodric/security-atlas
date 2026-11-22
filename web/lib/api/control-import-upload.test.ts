// #2836 — the control import upload's mapping helpers, and the shared
// column-mapping helper run over the controls import's field set.

import { describe, expect, test } from "vitest";

import {
  assign,
  initialAssignments,
  missingRequired,
  previewTable,
  uploadMapping,
} from "@/lib/import/column-mapping";

import {
  type ControlImportPreview,
  controlMappingFields,
  uploadForm,
} from "./control-import-upload";

// The fields controlimport.PreviewFile returns, in its order.
const PLATFORM_FIELDS: ControlImportPreview["fields"] = [
  { name: "id", required: true },
  { name: "title", required: true },
  { name: "description", required: true },
  { name: "tested_under", required: false },
  { name: "implementation_type", required: false },
  { name: "applicability", required: false },
];

const preview = (
  over: Partial<ControlImportPreview> = {},
): ControlImportPreview => ({
  header_row: 1,
  header_detected: true,
  columns: [],
  fields: PLATFORM_FIELDS,
  rows: [],
  data_rows: 0,
  ...over,
});

describe("control import upload helpers", () => {
  test("controlMappingFields labels and orders the controls fields", () => {
    const got = controlMappingFields(
      preview({
        fields: [
          { name: "applicability", required: false },
          { name: "brand_new_field", required: false },
          ...PLATFORM_FIELDS.slice(0, 3),
        ],
      }),
    );
    expect(got).toEqual([
      { name: "id", label: "Control ID", required: true },
      { name: "title", label: "Title", required: true },
      { name: "description", label: "Description", required: true },
      { name: "applicability", label: "Applicability", required: false },
      { name: "brand_new_field", label: "brand_new_field", required: false },
    ]);
  });

  test("alias-detected headers are pre-filled; the rest are unmapped", () => {
    const p = preview({
      columns: [
        { index: 0, header: "Control ID", field: "id", mappable: true },
        { index: 1, header: "Title", field: "title", mappable: true },
        { index: 2, header: "Prose", mappable: true },
      ],
    });
    const fields = controlMappingFields(p);
    const a = initialAssignments(p.columns);
    expect(a).toEqual({ 0: "id", 1: "title", 2: "" });
    expect(missingRequired(fields, a).map((f) => f.label)).toEqual([
      "Description",
    ]);
  });

  test("a sheet with no alias headers maps by hand to the upload's column map", () => {
    const p = preview({
      header_detected: false,
      columns: [
        { index: 0, header: "Key", mappable: true },
        { index: 1, header: "Headline", mappable: true },
        { index: 2, header: "Body", mappable: true },
        { index: 3, header: "Kind", mappable: true },
      ],
      rows: [["X-1", "Backups", "Nightly backups", "manual"]],
    });
    const fields = controlMappingFields(p);
    let a = initialAssignments(p.columns);
    expect(missingRequired(fields, a)).toHaveLength(3);

    a = assign(p.columns, a, 0, "id");
    a = assign(p.columns, a, 1, "title");
    a = assign(p.columns, a, 2, "description");
    expect(missingRequired(fields, a)).toEqual([]);

    expect(uploadMapping(p.columns, a)).toEqual({
      columnMap: ["id=Key", "title=Headline", "description=Body"],
      ignoreColumn: ["Kind"],
    });
    expect(previewTable(fields, p.columns, a, p.rows)).toEqual({
      headings: ["Control ID", "Title", "Description", "Kind"],
      mapped: [true, true, true, false],
      cells: [["X-1", "Backups", "Nightly backups", "manual"]],
    });
  });

  test("uploadForm carries the file, the framework and the mapping", () => {
    const f = uploadForm(new File(["x"], "c.csv"), "soc2", {
      columnMap: ["title=Headline"],
      ignoreColumn: ["Kind"],
    });
    expect((f.get("file") as File).name).toBe("c.csv");
    expect(f.get("framework")).toBe("soc2");
    expect(f.getAll("column_map")).toEqual(["title=Headline"]);
    expect(f.getAll("ignore_column")).toEqual(["Kind"]);

    // The preview reads no framework.
    const p = uploadForm(new File(["x"], "c.csv"));
    expect(p.has("framework")).toBe(false);
    expect(p.has("column_map")).toBe(false);
  });
});
