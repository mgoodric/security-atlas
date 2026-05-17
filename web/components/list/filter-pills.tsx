"use client";

// Slice 098 — generic horizontal filter pill row.
//
// Per `Plans/canvas/12-ui-fill-in-design-decisions.md` §8: "horizontal
// pill row above the table (not a left filter sidebar)". Each filter is
// a `<select>` styled like a chip — label + value in one element.
//
// Used by all five list-view slices (098/099/100/101/102). The consuming
// page declares the filter set; this component renders them and surfaces
// onChange callbacks so the page owns the state shape.
//
// A right-aligned meta slot (e.g. "Showing 7 of 47") sits in the same
// row so the user always sees the active filter set count.

import type { ReactNode } from "react";

export type FilterPill = {
  /** Stable identifier; passed back to onChange. */
  id: string;
  /** Short label shown to the left of the chip value. */
  label: string;
  /** Currently selected value. */
  value: string;
  /** Available choices (rendered as <option>). */
  options: { value: string; label: string }[];
};

export type FilterPillsProps = {
  pills: FilterPill[];
  onChange: (id: string, value: string) => void;
  /** Optional right-aligned meta line ("Showing N of M"). */
  meta?: ReactNode;
};

export function FilterPills({ pills, onChange, meta }: FilterPillsProps) {
  return (
    <div
      data-testid="list-filter-pills"
      className="flex items-center gap-2 flex-wrap"
    >
      {pills.map((pill) => (
        <label
          key={pill.id}
          data-testid={`list-filter-pill-${pill.id}`}
          className="flex items-center gap-1.5 px-2.5 py-1.5 bg-card border rounded-md text-xs"
        >
          <span className="text-muted-foreground">{pill.label}</span>
          <select
            aria-label={pill.label}
            value={pill.value}
            onChange={(e) => onChange(pill.id, e.target.value)}
            className="bg-transparent text-foreground font-medium focus:outline-none"
          >
            {pill.options.map((opt) => (
              <option key={opt.value} value={opt.value}>
                {opt.label}
              </option>
            ))}
          </select>
        </label>
      ))}
      {meta ? (
        <div
          data-testid="list-filter-meta"
          className="ml-auto text-xs text-muted-foreground"
        >
          {meta}
        </div>
      ) : null}
    </div>
  );
}
