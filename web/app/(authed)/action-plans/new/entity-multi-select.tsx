"use client";

// Slice 384 — EntityMultiSelect: a generic search-filterable checkbox-list
// picker, reused for BOTH the risk picker and the control picker on the
// action-plan create form (AC-24, max 50 each). Mirrors the slice-151
// ControlMultiSelect shape (native checkbox list, not a shadcn
// Command/Popover — the established "no new shadcn primitives for the
// risk-register forms" precedent) but is entity-agnostic: the caller
// supplies the option list + the cap + the labels.

import { useMemo, useState } from "react";

export type SelectOption = {
  id: string;
  label: string;
  /** Optional secondary text shown muted next to the label (e.g. status). */
  hint?: string;
};

type Props = {
  /** All selectable options (already fetched by the parent). */
  options: SelectOption[];
  selectedIds: string[];
  onChange: (ids: string[]) => void;
  /** Per-entity selection cap (P0-384-7 = 50). */
  max: number;
  /** Field legend, e.g. "Linked risks". */
  legend: string;
  /** Placeholder for the search box. */
  searchPlaceholder: string;
  /** testid prefix so the parent's tests can target rows. */
  testIdPrefix: string;
  isLoading?: boolean;
  isError?: boolean;
  errorMessage?: string;
  /** Copy shown when the tenant has zero options of this kind. */
  emptyHint: string;
};

export function EntityMultiSelect({
  options,
  selectedIds,
  onChange,
  max,
  legend,
  searchPlaceholder,
  testIdPrefix,
  isLoading,
  isError,
  errorMessage,
  emptyHint,
}: Props) {
  const [filter, setFilter] = useState("");

  const filtered = useMemo(() => {
    const needle = filter.trim().toLowerCase();
    if (!needle) return options;
    return options.filter(
      (o) =>
        o.label.toLowerCase().includes(needle) ||
        o.id.toLowerCase().includes(needle),
    );
  }, [options, filter]);

  const atCap = selectedIds.length >= max;

  const toggle = (id: string) => {
    if (selectedIds.includes(id)) {
      onChange(selectedIds.filter((x) => x !== id));
      return;
    }
    if (atCap) return; // P0-384-7: do not exceed the cap client-side.
    onChange([...selectedIds, id]);
  };

  return (
    <fieldset
      className="rounded-md border p-3 space-y-2"
      data-testid={`${testIdPrefix}-fieldset`}
    >
      <legend className="px-1 text-sm font-medium">
        {legend}{" "}
        <span className="text-xs text-muted-foreground">
          ({selectedIds.length}/{max})
        </span>
      </legend>

      <input
        type="text"
        value={filter}
        onChange={(e) => setFilter(e.target.value)}
        placeholder={searchPlaceholder}
        className="w-full rounded-md border bg-background px-2 py-1 text-sm"
        data-testid={`${testIdPrefix}-search`}
        aria-label={searchPlaceholder}
      />

      {isLoading ? (
        <p className="text-xs text-muted-foreground">Loading…</p>
      ) : isError ? (
        <p
          className="text-xs text-rose-600"
          data-testid={`${testIdPrefix}-error`}
        >
          {errorMessage ?? "Could not load options."}
        </p>
      ) : options.length === 0 ? (
        <p className="text-xs text-muted-foreground">{emptyHint}</p>
      ) : (
        <ul
          className="max-h-48 space-y-1 overflow-y-auto"
          data-testid={`${testIdPrefix}-list`}
        >
          {filtered.map((o) => {
            const checked = selectedIds.includes(o.id);
            const disabled = !checked && atCap;
            return (
              <li key={o.id}>
                <label
                  className={
                    "flex items-center gap-2 text-sm " +
                    (disabled ? "opacity-50" : "cursor-pointer")
                  }
                >
                  <input
                    type="checkbox"
                    checked={checked}
                    disabled={disabled}
                    onChange={() => toggle(o.id)}
                    data-testid={`${testIdPrefix}-option-${o.id}`}
                  />
                  <span className="truncate">{o.label}</span>
                  {o.hint ? (
                    <span className="text-xs text-muted-foreground">
                      {o.hint}
                    </span>
                  ) : null}
                </label>
              </li>
            );
          })}
          {filtered.length === 0 ? (
            <li className="text-xs text-muted-foreground">No matches.</li>
          ) : null}
        </ul>
      )}

      {atCap ? (
        <p
          className="text-xs text-amber-600"
          data-testid={`${testIdPrefix}-cap`}
        >
          Maximum of {max} reached.
        </p>
      ) : null}
    </fieldset>
  );
}
