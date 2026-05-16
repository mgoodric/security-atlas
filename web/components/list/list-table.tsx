"use client";

// Slice 098 — generic list-view data table.
//
// Wraps the shadcn `<Table>` primitive (web/components/ui/table.tsx) with
// a column-defs + rows interface so the consuming page declares the
// column shape once and the table handles header rendering, row hover
// states, and optional row-click navigation.
//
// Used by all five list-view slices (098/099/100/101/102). The shell
// stays domain-agnostic: column cells render whatever the page supplies
// (string, JSX, badge, link, etc.). Sort + paginate stay out-of-scope
// for v1 — file as spillover slices if a downstream list needs them.
//
// Constitutional commitment: NO controls-specific imports, types, or
// strings here. The page is the only place that knows about anchors,
// controls, evidence, risks, etc.

import type { ReactNode } from "react";

import {
  Table,
  TableBody,
  TableCell,
  TableHead,
  TableHeader,
  TableRow,
} from "@/components/ui/table";

export type ListColumn<Row> = {
  /** Stable identifier — also used for the test-id and React key. */
  id: string;
  /** Header label. */
  header: ReactNode;
  /** Cell renderer. Returns a node for one row's column cell. */
  cell: (row: Row) => ReactNode;
  /** Optional align hint. Defaults to "left". */
  align?: "left" | "right";
  /** Optional Tailwind class to add to the <th> + <td>. */
  className?: string;
};

export type ListTableProps<Row> = {
  columns: ListColumn<Row>[];
  rows: Row[];
  /** Unique-key resolver per row (stable across re-renders). */
  rowKey: (row: Row) => string;
  /** Optional click handler — renders the row as clickable when set. */
  onRowClick?: (row: Row) => void;
  /** Optional render-when-empty fallback (typically an <EmptyState>). */
  emptyFallback?: ReactNode;
};

export function ListTable<Row>({
  columns,
  rows,
  rowKey,
  onRowClick,
  emptyFallback,
}: ListTableProps<Row>) {
  if (rows.length === 0 && emptyFallback) {
    return <>{emptyFallback}</>;
  }

  return (
    <div
      data-testid="list-table-wrap"
      className="rounded-xl border bg-card overflow-hidden"
    >
      <Table>
        <TableHeader>
          <TableRow className="bg-muted/50 text-[11px] uppercase tracking-wider text-muted-foreground">
            {columns.map((col) => (
              <TableHead
                key={col.id}
                className={
                  (col.align === "right" ? "text-right " : "") +
                  (col.className ?? "")
                }
              >
                {col.header}
              </TableHead>
            ))}
          </TableRow>
        </TableHeader>
        <TableBody>
          {rows.map((row) => {
            const key = rowKey(row);
            return (
              <TableRow
                key={key}
                data-testid="list-table-row"
                onClick={onRowClick ? () => onRowClick(row) : undefined}
                className={onRowClick ? "cursor-pointer" : undefined}
              >
                {columns.map((col) => (
                  <TableCell
                    key={col.id}
                    data-testid={`list-cell-${col.id}`}
                    className={
                      (col.align === "right" ? "text-right " : "") +
                      (col.className ?? "")
                    }
                  >
                    {col.cell(row)}
                  </TableCell>
                ))}
              </TableRow>
            );
          })}
        </TableBody>
      </Table>
    </div>
  );
}
