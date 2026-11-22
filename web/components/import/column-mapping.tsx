"use client";

// #2848 — the import mapping step: each detected header with the field it
// maps to (pre-filled from detection, changeable), the columns no field
// takes, and the first rows as they will be read.
//
// It takes its field list as input and knows nothing about the record
// being imported: the risk import passes the risk fields, the controls
// import its own (#2836). The mapping rules (one column per
// field, the required fields, the upload's column_map) are the pure helper
// in lib/import/column-mapping.ts.

import { useId } from "react";

import { Badge } from "@/components/ui/badge";
import { Select } from "@/components/ui/select";
import {
  type Assignments,
  type MappingColumn,
  type MappingField,
  assign,
  missingRequired,
  previewTable,
  unmappedColumns,
} from "@/lib/import/column-mapping";

export type ColumnMappingProps = {
  fields: MappingField[];
  columns: MappingColumn[];
  /** The first rows, one cell per entry of `columns`. */
  rows: string[][];
  value: Assignments;
  onChange: (next: Assignments) => void;
  /** Prefix for data-testid attributes. */
  testId?: string;
};

export function ColumnMapping({
  fields,
  columns,
  rows,
  value,
  onChange,
  testId = "column-mapping",
}: ColumnMappingProps) {
  const id = useId();
  const missing = missingRequired(fields, value);
  const unmapped = unmappedColumns(columns, value);
  const preview = previewTable(fields, columns, value, rows);

  return (
    <div className="space-y-6" data-testid={testId}>
      <section className="space-y-2">
        <h2 className="text-sm font-medium">Columns</h2>
        <div className="overflow-x-auto rounded-lg border">
          <table className="w-full text-sm">
            <thead className="bg-muted/50 text-left text-xs text-muted-foreground">
              <tr>
                <th className="px-3 py-2 font-medium">Header in the file</th>
                <th className="px-3 py-2 font-medium">Maps to</th>
                <th className="px-3 py-2 font-medium">First value</th>
              </tr>
            </thead>
            <tbody>
              {columns.map((c) => {
                const pos = columns.indexOf(c);
                const selectID = `${id}-col-${c.index}`;
                return (
                  <tr
                    key={c.index}
                    className="border-t"
                    data-testid={`${testId}-row`}
                    data-header={c.header}
                  >
                    <td className="px-3 py-2">
                      <label htmlFor={selectID}>{c.header}</label>
                    </td>
                    <td className="px-3 py-2">
                      <Select
                        id={selectID}
                        value={value[c.index] ?? ""}
                        disabled={!c.mappable}
                        title={
                          c.mappable
                            ? undefined
                            : "A blank or repeated header cannot be mapped; rename it in the file"
                        }
                        onChange={(e) =>
                          onChange(
                            assign(columns, value, c.index, e.target.value),
                          )
                        }
                        data-testid={`${testId}-select`}
                      >
                        <option value="">Not imported</option>
                        {fields.map((f) => (
                          <option key={f.name} value={f.name}>
                            {f.label}
                            {f.required ? " (required)" : ""}
                          </option>
                        ))}
                      </Select>
                    </td>
                    <td className="max-w-48 truncate px-3 py-2 text-muted-foreground">
                      {rows.find((r) => (r[pos] ?? "").trim() !== "")?.[pos] ??
                        ""}
                    </td>
                  </tr>
                );
              })}
            </tbody>
          </table>
        </div>
        {missing.length > 0 ? (
          <p
            className="text-sm text-destructive"
            data-testid={`${testId}-missing`}
          >
            Map a column to {missing.map((f) => f.label).join(" and ")} to
            continue.
          </p>
        ) : null}
      </section>

      <section className="space-y-2 text-sm">
        <h2 className="font-medium">
          Unmapped columns{" "}
          <Badge variant={unmapped.length > 0 ? "warning" : "pass"}>
            {unmapped.length}
          </Badge>
        </h2>
        {unmapped.length > 0 ? (
          <p data-testid={`${testId}-unmapped`}>
            {unmapped.map((c) => c.header).join(", ")}
            <span className="text-muted-foreground">
              {" "}
              — kept on the import for review, not imported into a field.
            </span>
          </p>
        ) : (
          <p className="text-muted-foreground">Every column maps to a field.</p>
        )}
      </section>

      <section className="space-y-2">
        <h2 className="text-sm font-medium">Preview of the first rows</h2>
        <div className="overflow-x-auto rounded-lg border">
          <table className="w-full text-sm" data-testid={`${testId}-preview`}>
            <thead className="bg-muted/50 text-left text-xs">
              <tr>
                {preview.headings.map((h, i) => (
                  <th
                    key={`${h}-${i}`}
                    className={
                      preview.mapped[i]
                        ? "px-3 py-2 font-medium"
                        : "px-3 py-2 font-normal text-muted-foreground"
                    }
                  >
                    {h}
                  </th>
                ))}
              </tr>
            </thead>
            <tbody>
              {preview.cells.map((r, i) => (
                <tr key={i} className="border-t">
                  {r.map((cell, j) => (
                    <td
                      key={j}
                      className={
                        preview.mapped[j]
                          ? "max-w-48 truncate px-3 py-2"
                          : "max-w-48 truncate px-3 py-2 text-muted-foreground"
                      }
                    >
                      {cell}
                    </td>
                  ))}
                </tr>
              ))}
            </tbody>
          </table>
        </div>
      </section>
    </div>
  );
}
