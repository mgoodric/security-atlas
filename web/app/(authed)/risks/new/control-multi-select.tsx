"use client";

// Slice 151 — ControlMultiSelect: control-link picker for the risk-create form.
//
// Renders a search-filterable checkbox list bound to the slice-151 BFF
// at `/api/controls-list` (which proxies the slice-151 backend
// `GET /v1/controls`). Selected ids flow up to the parent via `onChange`;
// the parent (risk-form.tsx) gates rendering on `treatment === 'mitigate'`
// and surfaces a field-level error when the form is submitted with zero
// selections.
//
// D-151-2 — native checkbox list, not shadcn Command/Popover. Slice 105
// established the "no new shadcn primitives for the risk form" precedent
// (vendor-form note in risk-form.tsx). A bordered fieldset + a text
// input + scrollable list of checkboxes hits the AC ("multi-select with
// selection persistence") without expanding the dependency surface.
// shadcn Command + Popover can be a follow-on slice if the picker UX
// needs to graduate to a typeahead pattern.
//
// Empty-state handling: if the tenant has zero active controls the
// component renders a help message that points at the controls upload
// path rather than presenting an empty checkbox list — surfaced as a
// muted note so the form remains submittable for non-mitigate treatments.
//
// Loading state: fetch happens on mount via TanStack Query (the same
// data-fetching layer the rest of `web/app/(authed)/` uses). A skeleton
// row renders while in flight; an error banner renders on a non-2xx
// upstream.

import { useQuery } from "@tanstack/react-query";
import { useMemo, useState } from "react";

import { fetchTenantControlsList, TenantControl } from "@/lib/api";

type Props = {
  selectedIds: string[];
  onChange: (ids: string[]) => void;
  // When true (parent form was submitted with zero links and treatment=mitigate),
  // render a field-level error message under the picker. The parent owns the
  // gating logic so the validation surface is testable in isolation.
  showRequiredError?: boolean;
};

export function ControlMultiSelect({
  selectedIds,
  onChange,
  showRequiredError,
}: Props) {
  const [filter, setFilter] = useState("");

  const { data, isLoading, isError, error } = useQuery({
    queryKey: ["risks", "control-link-picker"],
    queryFn: fetchTenantControlsList,
    // The picker is opened transiently; refetch on remount picks up
    // newly uploaded controls without an explicit invalidate.
    staleTime: 30_000,
  });

  // Memoize the controls list so the filtered useMemo's dep array is
  // referentially stable across renders (eslint react-hooks/exhaustive-deps).
  const controls: TenantControl[] = useMemo(() => data ?? [], [data]);

  const filtered = useMemo(() => {
    const needle = filter.trim().toLowerCase();
    if (!needle) return controls;
    return controls.filter(
      (c) =>
        c.title.toLowerCase().includes(needle) ||
        c.scf_id.toLowerCase().includes(needle) ||
        c.control_family.toLowerCase().includes(needle) ||
        c.bundle_id.toLowerCase().includes(needle),
    );
  }, [controls, filter]);

  function toggle(id: string) {
    if (selectedIds.includes(id)) {
      onChange(selectedIds.filter((x) => x !== id));
    } else {
      onChange([...selectedIds, id]);
    }
  }

  function clearAll() {
    onChange([]);
  }

  const SELECT_INPUT =
    "h-9 w-full rounded-md border border-input bg-background px-3 py-1 text-sm shadow-sm focus-visible:outline-none focus-visible:ring-1 focus-visible:ring-ring";

  return (
    <fieldset
      className="rounded-md border border-input p-4 space-y-3"
      data-testid="risks-create-control-multi-select"
    >
      <legend className="px-2 text-sm font-medium text-foreground">
        Linked controls <span className="text-destructive">*</span>
      </legend>
      <p className="text-xs text-muted-foreground">
        Required when treatment is <span className="font-mono">mitigate</span>.
        Select one or more controls that mitigate this risk.
      </p>

      <input
        type="text"
        placeholder="Search by title, SCF code, family, or bundle id"
        value={filter}
        onChange={(e) => setFilter(e.target.value)}
        className={SELECT_INPUT}
        data-testid="risks-create-control-multi-select-filter"
      />

      <div className="flex items-center justify-between text-xs text-muted-foreground">
        <span data-testid="risks-create-control-multi-select-summary">
          {selectedIds.length} selected
          {filter && ` · ${filtered.length} of ${controls.length} visible`}
          {!filter && ` · ${controls.length} total`}
        </span>
        {selectedIds.length > 0 && (
          <button
            type="button"
            onClick={clearAll}
            className="text-xs underline hover:text-foreground"
            data-testid="risks-create-control-multi-select-clear"
          >
            Clear selection
          </button>
        )}
      </div>

      {isLoading && (
        <div
          className="text-sm text-muted-foreground"
          data-testid="risks-create-control-multi-select-loading"
        >
          Loading controls…
        </div>
      )}

      {isError && (
        <div
          className="rounded-md border border-destructive bg-destructive/10 p-3 text-sm text-destructive"
          data-testid="risks-create-control-multi-select-error"
        >
          Could not load controls: {(error as Error).message}
        </div>
      )}

      {!isLoading && !isError && controls.length === 0 && (
        <div
          className="rounded-md border border-input bg-muted/40 p-3 text-sm text-muted-foreground"
          data-testid="risks-create-control-multi-select-empty"
        >
          No active controls in this tenant yet. Upload a control bundle via{" "}
          <span className="font-mono">/v1/controls:upload-bundle</span> or
          switch treatment to <span className="font-mono">accept</span> /{" "}
          <span className="font-mono">transfer</span> /{" "}
          <span className="font-mono">avoid</span>.
        </div>
      )}

      {!isLoading && !isError && controls.length > 0 && (
        <div
          className="max-h-64 overflow-y-auto rounded-md border border-input"
          data-testid="risks-create-control-multi-select-list"
        >
          {filtered.length === 0 && (
            <div className="p-3 text-sm text-muted-foreground">
              No controls match your filter.
            </div>
          )}
          {filtered.map((c) => {
            const checked = selectedIds.includes(c.id);
            return (
              <label
                key={c.id}
                className={
                  "flex items-start gap-3 border-b border-input/60 px-3 py-2 last:border-b-0 cursor-pointer hover:bg-accent/40 " +
                  (checked ? "bg-accent/30" : "")
                }
                data-testid={`risks-create-control-multi-select-option-${c.id}`}
              >
                <input
                  type="checkbox"
                  checked={checked}
                  onChange={() => toggle(c.id)}
                  className="mt-1"
                  data-testid={`risks-create-control-multi-select-checkbox-${c.id}`}
                />
                <span className="flex flex-col gap-0.5 text-sm">
                  <span className="font-medium text-foreground">{c.title}</span>
                  <span className="text-xs text-muted-foreground">
                    {c.scf_id && (
                      <>
                        <span className="font-mono">{c.scf_id}</span>
                        {" · "}
                      </>
                    )}
                    {c.control_family}
                    {" · "}
                    <span className="font-mono">{c.bundle_id}</span>
                  </span>
                </span>
              </label>
            );
          })}
        </div>
      )}

      {showRequiredError && (
        <p
          className="text-sm text-destructive"
          data-testid="risks-create-control-multi-select-required-error"
        >
          Select at least one control when treatment is mitigate.
        </p>
      )}
    </fieldset>
  );
}
