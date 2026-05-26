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
//
// Slice 281 — mobile-aware list-table collapse.
//
// At `< md` (375px-768px) the existing table layout horizontal-scrolls
// when there are 5+ columns. Slice 277's `docs/responsive-audit.md`
// flagged `/controls`, `/risks`, and `/evidence` as `no` verdicts on
// that ground. The fix lives in this primitive: an additive
// `mobileMode` prop. When set to `"cards"` AND the viewport is `< md`,
// each row renders as a card with the column headers as inline
// `<dt>` labels and the cell content as `<dd>` values (semantic
// description list).
//
// Discipline (slice 281 P0):
//   * `mobileMode` defaults to `"table"` so every non-opted-in caller
//     (audits, exceptions, policies, future tables) renders today's
//     shape unchanged at every viewport (P0-281-1, P0-281-3 — no new
//     dep, P0-281-4 — only the 3 priority pages opt in).
//   * The card branch reuses the SAME `cell(row)` renderers from the
//     same `columns` array. There is no second wire shape and no
//     `<MobileListTable>` component tree (P0-281-2).
//   * Outer prop shape (columns / rows / rowKey / onRowClick /
//     emptyFallback) is unchanged. Only `mobileMode` is added
//     (P0-281-5).
//   * At `≥ md` the card branch is hidden via Tailwind `md:hidden`
//     and the table branch is visible via `hidden md:block` — the
//     desktop DOM is byte-identical to the pre-281 baseline aside
//     from the visibility classes on the outer wrapper (P0-281-1).

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

/**
 * Slice 281 — mobile-mode prop.
 *
 *   * `"table"` (default) — render the legacy table at every viewport;
 *     no card branch is mounted. Byte-identical to the pre-281
 *     rendering for callers that do not opt in.
 *   * `"cards"` — render BOTH the legacy table (visible at `≥ md` via
 *     `hidden md:block`) AND a card stack (visible at `< md` via
 *     `block md:hidden`). Each card is one row; each cell is rendered
 *     as a `<dt>` label + `<dd>` value pair, with the column
 *     `header` as the label and the column `cell(row)` as the value.
 */
export type ListTableMobileMode = "table" | "cards";

export type ListTableProps<Row> = {
  columns: ListColumn<Row>[];
  rows: Row[];
  /** Unique-key resolver per row (stable across re-renders). */
  rowKey: (row: Row) => string;
  /** Optional click handler — renders the row as clickable when set. */
  onRowClick?: (row: Row) => void;
  /** Optional render-when-empty fallback (typically an <EmptyState>). */
  emptyFallback?: ReactNode;
  /**
   * Slice 281 — opt-in mobile rendering mode. Defaults to `"table"` so
   * existing callers (and any future callers that do not pass the
   * prop) render today's shape unchanged. See `ListTableMobileMode`
   * above for the per-mode behaviour.
   */
  mobileMode?: ListTableMobileMode;
};

/**
 * Slice 281 — pure decision helper for the mobile-mode rendering branch.
 *
 * Lives at module scope (not inside the component body) so vitest can
 * pin the branch math without instantiating React — the web workspace's
 * vitest runs in `node` env with no DOM and no `@testing-library/react`
 * dependency per `web/vitest.config.ts`. The component below calls
 * this helper at render time; the unit test covers the truth table
 * directly.
 *
 * Semantics:
 *   * Mode `"table"` (the legacy default) → tableBranch on, cardsBranch
 *     off at every viewport. The cards branch is not mounted at all,
 *     so the desktop DOM is byte-identical to pre-281.
 *   * Mode `"cards"` (the slice 281 opt-in) → both branches mounted;
 *     CSS visibility classes pick exactly one per viewport. At `≥ md`
 *     the table is visible; at `< md` the cards are visible.
 *
 * The function returns the Tailwind classes the outer `<div>`s carry
 * so the component body can stay a thin JSX wrapper around the
 * decision.
 */
export function listTableBranchClasses(mode: ListTableMobileMode): {
  /** Class for the table-branch outer wrap. `null` means do not mount. */
  tableWrap: string | null;
  /** Class for the cards-branch outer wrap. `null` means do not mount. */
  cardsWrap: string | null;
} {
  if (mode === "cards") {
    return {
      tableWrap: "hidden md:block",
      cardsWrap: "block md:hidden",
    };
  }
  // Default `"table"` mode: legacy rendering, no cards branch mounted.
  return {
    tableWrap: "",
    cardsWrap: null,
  };
}

export function ListTable<Row>({
  columns,
  rows,
  rowKey,
  onRowClick,
  emptyFallback,
  mobileMode = "table",
}: ListTableProps<Row>) {
  if (rows.length === 0 && emptyFallback) {
    return <>{emptyFallback}</>;
  }

  const branchClasses = listTableBranchClasses(mobileMode);

  // Legacy table branch — mounted in every mode. At `mobileMode="cards"`
  // it is hidden at `< md` via `hidden md:block`; at `mobileMode="table"`
  // it carries no visibility class and renders at every viewport (the
  // pre-281 default).
  const tableBranch = (
    <div
      data-testid="list-table-wrap"
      className={[
        "rounded-xl border bg-card overflow-hidden",
        branchClasses.tableWrap ?? "",
      ]
        .filter(Boolean)
        .join(" ")}
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

  // Slice 281 — cards branch. Mounted ONLY when `mobileMode === "cards"`.
  // Visible at `< md` via `block md:hidden` so the desktop layout is
  // untouched. Each row is a `<div>` carrying the same
  // `data-testid="list-table-row"` token the table branch uses (so
  // existing row-count assertions still work at desktop; the test that
  // is mobile-scoped resolves the count at the visible card branch
  // via `getByTestId("list-card-row")`).
  //
  // Semantic shape: one `<dl>` (description list) per card; each
  // column is one `<dt>` (header label) + `<dd>` (cell content) pair.
  // The right-aligned columns the page may have configured are
  // honoured by stacking the `<dd>` to the right via `text-right`.
  const cardsBranch =
    branchClasses.cardsWrap !== null ? (
      <div
        data-testid="list-cards-wrap"
        className={["space-y-2", branchClasses.cardsWrap]
          .filter(Boolean)
          .join(" ")}
      >
        {rows.map((row) => {
          const key = rowKey(row);
          return (
            <div
              key={key}
              data-testid="list-card-row"
              onClick={onRowClick ? () => onRowClick(row) : undefined}
              className={[
                "rounded-xl border bg-card p-3",
                onRowClick ? "cursor-pointer" : "",
              ]
                .filter(Boolean)
                .join(" ")}
            >
              <dl className="grid grid-cols-[max-content_1fr] gap-x-3 gap-y-1.5 text-sm">
                {columns.map((col) => (
                  <div
                    key={col.id}
                    data-testid={`list-card-cell-${col.id}`}
                    className="contents"
                  >
                    <dt className="text-[11px] uppercase tracking-wider text-muted-foreground self-center">
                      {col.header}
                    </dt>
                    <dd
                      className={[
                        "min-w-0 break-words",
                        col.align === "right" ? "text-right" : "",
                      ]
                        .filter(Boolean)
                        .join(" ")}
                    >
                      {col.cell(row)}
                    </dd>
                  </div>
                ))}
              </dl>
            </div>
          );
        })}
      </div>
    ) : null;

  return (
    <>
      {tableBranch}
      {cardsBranch}
    </>
  );
}
