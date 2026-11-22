// #2848 — the pure header-to-field helper behind the import mapping step.
//
// It knows nothing about risks: the caller passes the fields a column can
// map to and the columns the platform detected (each header with the field
// its alias detection gave it). The helper keeps one column per field,
// reports what the mapping is missing, and turns it into the upload's
// repeatable `column_map=field=Header` and `ignore_column=Header` fields.
// The risk import and the controls import (#2836) both use it.

/** A field a column can map to. */
export type MappingField = {
  name: string;
  label: string;
  required: boolean;
};

/** A detected header column. `field` is what detection mapped it to, ""
 *  when nothing; a column that is not `mappable` (a blank or repeated
 *  header, which a column map cannot name) keeps its detected field. */
export type MappingColumn = {
  index: number;
  header: string;
  field?: string;
  mappable: boolean;
};

/** The field chosen for each column, by column index; "" is unmapped. */
export type Assignments = Record<number, string>;

/** initialAssignments is detection's mapping. */
export function initialAssignments(columns: MappingColumn[]): Assignments {
  const out: Assignments = {};
  for (const c of columns) out[c.index] = c.field ?? "";
  return out;
}

/** assign maps one column to field ("" to unmap it). A field takes one
 *  column, so a column that held it before is unmapped. A column that is
 *  not mappable is left as it is. */
export function assign(
  columns: MappingColumn[],
  current: Assignments,
  index: number,
  field: string,
): Assignments {
  const col = columns.find((c) => c.index === index);
  if (!col?.mappable) return current;
  const out: Assignments = { ...current };
  if (field) {
    for (const c of columns) {
      if (c.index !== index && out[c.index] === field && c.mappable) {
        out[c.index] = "";
      }
    }
  }
  out[index] = field;
  return out;
}

/** missingRequired lists the required fields no column maps to. */
export function missingRequired(
  fields: MappingField[],
  a: Assignments,
): MappingField[] {
  const mapped = new Set(Object.values(a).filter(Boolean));
  return fields.filter((f) => f.required && !mapped.has(f.name));
}

/** unmappedColumns lists the columns no field takes, in column order:
 *  their values are kept on the import, not on the record. */
export function unmappedColumns(
  columns: MappingColumn[],
  a: Assignments,
): MappingColumn[] {
  return columns.filter((c) => !a[c.index]);
}

/** UploadMapping is the upload's mapping fields. */
export type UploadMapping = { columnMap: string[]; ignoreColumn: string[] };

/** uploadMapping names every mapped column explicitly and ignores every
 *  other one, so the upload maps exactly what the step showed: no alias
 *  maps a column the user left unmapped, including a second alias column
 *  of a field the user unmapped from the first. */
export function uploadMapping(
  columns: MappingColumn[],
  a: Assignments,
): UploadMapping {
  const columnMap: string[] = [];
  const ignoreColumn: string[] = [];
  for (const c of columns) {
    if (!c.mappable) continue;
    const field = a[c.index] ?? "";
    if (field) columnMap.push(`${field}=${c.header}`);
    else ignoreColumn.push(c.header);
  }
  return { columnMap, ignoreColumn };
}

/** appendMapping adds an UploadMapping to a multipart form. */
export function appendMapping(form: FormData, m: UploadMapping): void {
  for (const v of m.columnMap) form.append("column_map", v);
  for (const v of m.ignoreColumn) form.append("ignore_column", v);
}

/** fieldLabel names a field by its label, or as sent when not listed. */
export function fieldLabel(fields: MappingField[], name: string): string {
  return fields.find((f) => f.name === name)?.label ?? name;
}

/** previewTable is the first rows as they will be read: one column per
 *  mapped field, in field order, then the unmapped columns. `rows` holds
 *  one cell per entry of `columns`, in the same order. */
export function previewTable(
  fields: MappingField[],
  columns: MappingColumn[],
  a: Assignments,
  rows: string[][],
): { headings: string[]; mapped: boolean[]; cells: string[][] } {
  const pos = new Map(columns.map((c, i) => [c.index, i]));
  const order: { col: MappingColumn; heading: string; mapped: boolean }[] = [];
  for (const f of fields) {
    const col = columns.find((c) => a[c.index] === f.name);
    if (col) order.push({ col, heading: f.label, mapped: true });
  }
  for (const col of unmappedColumns(columns, a)) {
    order.push({ col, heading: col.header, mapped: false });
  }
  return {
    headings: order.map((o) => o.heading),
    mapped: order.map((o) => o.mapped),
    cells: rows.map((r) => order.map((o) => r[pos.get(o.col.index)!] ?? "")),
  };
}
