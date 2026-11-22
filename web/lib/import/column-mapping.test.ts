// #2848 — the import mapping helper, with a field list that is not the
// risk import's, to pin that the helper takes its fields as input.

import { describe, expect, test } from "vitest";

import {
  type MappingColumn,
  type MappingField,
  appendMapping,
  assign,
  fieldLabel,
  initialAssignments,
  missingRequired,
  previewTable,
  unmappedColumns,
  uploadMapping,
} from "./column-mapping";

const FIELDS: MappingField[] = [
  { name: "id", label: "ID", required: false },
  { name: "name", label: "Name", required: true },
  { name: "kind", label: "Kind", required: true },
  { name: "notes", label: "Notes", required: false },
];

const COLS: MappingColumn[] = [
  { index: 0, header: "Ref", field: "id", mappable: true },
  { index: 1, header: "What", mappable: true },
  { index: 2, header: "Bucket", mappable: true },
  { index: 4, header: "column 5", mappable: false },
];

describe("column mapping", () => {
  test("starts from detection and reports the missing required fields", () => {
    const a = initialAssignments(COLS);
    expect(a).toEqual({ 0: "id", 1: "", 2: "", 4: "" });
    expect(missingRequired(FIELDS, a).map((f) => f.name)).toEqual([
      "name",
      "kind",
    ]);
    expect(unmappedColumns(COLS, a).map((c) => c.header)).toEqual([
      "What",
      "Bucket",
      "column 5",
    ]);
  });

  test("a file with no detected field maps fully", () => {
    let a = initialAssignments(COLS);
    a = assign(COLS, a, 1, "name");
    a = assign(COLS, a, 2, "kind");
    expect(missingRequired(FIELDS, a)).toEqual([]);
    expect(uploadMapping(COLS, a)).toEqual({
      columnMap: ["id=Ref", "name=What", "kind=Bucket"],
      ignoreColumn: [],
    });
  });

  test("a field takes one column, and a moved field unmaps its old column", () => {
    let a = assign(COLS, initialAssignments(COLS), 1, "name");
    a = assign(COLS, a, 2, "name");
    expect(a[1]).toBe("");
    expect(a[2]).toBe("name");
  });

  test("an unmapped column is ignored so no alias maps it again", () => {
    const a = assign(COLS, initialAssignments(COLS), 0, "");
    expect(uploadMapping(COLS, a)).toEqual({
      columnMap: [],
      ignoreColumn: ["Ref", "What", "Bucket"],
    });
  });

  test("a column that is not mappable stays as detected", () => {
    const a = initialAssignments(COLS);
    expect(assign(COLS, a, 4, "notes")).toBe(a);
    expect(uploadMapping(COLS, a).ignoreColumn).not.toContain("column 5");
  });

  test("appendMapping writes the repeatable form fields", () => {
    const form = new FormData();
    appendMapping(form, { columnMap: ["name=What"], ignoreColumn: ["Ref"] });
    expect(form.getAll("column_map")).toEqual(["name=What"]);
    expect(form.getAll("ignore_column")).toEqual(["Ref"]);
  });

  test("the preview puts mapped fields first, in field order", () => {
    let a = assign(COLS, initialAssignments(COLS), 2, "name");
    a = assign(COLS, a, 1, "kind");
    const t = previewTable(FIELDS, COLS, a, [["R-1", "risk", "Phish", "x"]]);
    expect(t.headings).toEqual(["ID", "Name", "Kind", "column 5"]);
    expect(t.mapped).toEqual([true, true, true, false]);
    expect(t.cells).toEqual([["R-1", "Phish", "risk", "x"]]);
  });

  test("fieldLabel falls back to the name", () => {
    expect(fieldLabel(FIELDS, "kind")).toBe("Kind");
    expect(fieldLabel(FIELDS, "other")).toBe("other");
  });
});
