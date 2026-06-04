"use client";

// Slice 448 — selection bar + saved filter-views bar for the /controls
// list. Presentational + interaction wiring only; all the set/persist
// math lives in the pure `selection.ts` + `saved-views.ts` modules
// (node-env vitest-covered). This `.tsx` is covered by the Playwright
// tier per `web/testing.md` (no vitest JSX — slice 069 P0-A3).
//
// Two stacked bars:
//   1. SelectionBar — visible only when at least one row is selected.
//      Shows the live count, the cap state (AC-3), a "Clear" action, and
//      the bulk-assign-owner affordance. The action is future-state-
//      disclosed (no owner-assign mutation exists on `main` — see
//      bulk-assign-future.ts + decisions log D1); the selection itself
//      is real so the operator sees exactly what the action WILL target.
//   2. SavedViewsBar — always visible. A native <select> (matching the
//      slice-098 FilterPills idiom) to load a saved view, a "Save
//      current filters" button opening a small inline name form, and a
//      delete control for the loaded view. Per-user, persisted client-
//      side (decisions log D1).

import { useState } from "react";

import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";

import {
  BULK_ASSIGN_FUTURE_REASON,
  BULK_ASSIGN_FUTURE_TESTID,
} from "./bulk-assign-future";
import { SELECTION_CAP } from "./selection";
import { MAX_VIEW_NAME_LENGTH, type SavedView } from "./saved-views";

export type SelectionBarProps = {
  selectedCount: number;
  overCap: boolean;
  onClear: () => void;
};

/**
 * Selection summary + bulk-action affordance. Rendered by the page only
 * when `selectedCount > 0`.
 */
export function SelectionBar({
  selectedCount,
  overCap,
  onClear,
}: SelectionBarProps) {
  return (
    <div
      data-testid="controls-selection-bar"
      role="region"
      aria-label="Bulk actions for selected controls"
      className="mb-3 flex flex-wrap items-center gap-3 rounded-lg border bg-card px-3 py-2 text-sm"
    >
      <span data-testid="controls-selection-count" className="font-medium">
        {selectedCount} selected
      </span>
      <span className="text-xs text-muted-foreground">
        (cap {SELECTION_CAP} per bulk action)
      </span>
      {overCap ? (
        <span
          data-testid="controls-selection-overcap"
          role="alert"
          className="text-xs font-medium text-destructive"
        >
          Selection exceeds the {SELECTION_CAP}-control cap — narrow your
          filters or deselect before applying a bulk action.
        </span>
      ) : null}
      {/* Bulk assign-owner — future-state disclosure (no owner-assign
          mutation on main; see bulk-assign-future.ts). Non-button span
          carrying title + aria-label so the disclosure IS the
          affordance (slice 225 label-honesty pattern). */}
      <span
        data-testid={BULK_ASSIGN_FUTURE_TESTID}
        title={BULK_ASSIGN_FUTURE_REASON}
        aria-label={BULK_ASSIGN_FUTURE_REASON}
        className="cursor-help text-xs text-muted-foreground italic"
      >
        Bulk assign-owner (coming soon)
      </span>
      <Button
        type="button"
        variant="ghost"
        size="sm"
        onClick={onClear}
        data-testid="controls-selection-clear"
        className="ml-auto"
      >
        Clear selection
      </Button>
    </div>
  );
}

export type SavedViewsBarProps = {
  views: SavedView[];
  /** The id of the currently-loaded view, or "" when none is active. */
  activeViewId: string;
  /** True when the current filter set is non-default (worth saving). */
  canSave: boolean;
  onLoadView: (id: string) => void;
  onSaveView: (name: string) => { ok: true } | { ok: false; message: string };
  onDeleteView: (id: string) => void;
};

const NO_VIEW = "";

/**
 * Saved filter-views bar. Load via a native <select>; save the current
 * filter state via an inline name form; delete the loaded view.
 */
export function SavedViewsBar({
  views,
  activeViewId,
  canSave,
  onLoadView,
  onSaveView,
  onDeleteView,
}: SavedViewsBarProps) {
  const [saving, setSaving] = useState(false);
  const [name, setName] = useState("");
  const [error, setError] = useState<string | null>(null);

  const errorId = error ? "controls-save-view-error" : undefined;

  function submitSave() {
    const result = onSaveView(name);
    if (result.ok) {
      setName("");
      setSaving(false);
      setError(null);
    } else {
      setError(result.message);
    }
  }

  return (
    <div
      data-testid="controls-saved-views-bar"
      className="mb-3 flex flex-wrap items-center gap-2 text-xs"
    >
      <label className="flex items-center gap-1.5 rounded-md border bg-card px-2.5 py-1.5">
        <span className="text-muted-foreground">Saved view</span>
        <select
          aria-label="Saved view"
          data-testid="controls-saved-views-select"
          value={activeViewId}
          onChange={(e) => onLoadView(e.target.value)}
          className="bg-transparent font-medium text-foreground focus:outline-none"
        >
          <option value={NO_VIEW}>None</option>
          {views.map((v) => (
            <option key={v.id} value={v.id}>
              {v.name}
            </option>
          ))}
        </select>
      </label>

      {activeViewId !== NO_VIEW ? (
        <Button
          type="button"
          variant="ghost"
          size="sm"
          data-testid="controls-saved-views-delete"
          onClick={() => onDeleteView(activeViewId)}
        >
          Delete view
        </Button>
      ) : null}

      {saving ? (
        <span className="flex flex-wrap items-center gap-1.5">
          <Input
            aria-label="New view name"
            data-testid="controls-save-view-name"
            value={name}
            maxLength={MAX_VIEW_NAME_LENGTH}
            placeholder="View name"
            aria-describedby={errorId}
            aria-invalid={error ? true : undefined}
            onChange={(e) => {
              setName(e.target.value);
              if (error) setError(null);
            }}
            onKeyDown={(e) => {
              if (e.key === "Enter") {
                e.preventDefault();
                submitSave();
              }
            }}
            className="h-8 w-44"
          />
          <Button
            type="button"
            size="sm"
            data-testid="controls-save-view-confirm"
            onClick={submitSave}
          >
            Save
          </Button>
          <Button
            type="button"
            variant="ghost"
            size="sm"
            data-testid="controls-save-view-cancel"
            onClick={() => {
              setSaving(false);
              setName("");
              setError(null);
            }}
          >
            Cancel
          </Button>
          {error ? (
            <span
              id="controls-save-view-error"
              role="alert"
              aria-live="polite"
              data-testid="controls-save-view-error"
              className="font-medium text-destructive"
            >
              {error}
            </span>
          ) : null}
        </span>
      ) : (
        <Button
          type="button"
          variant="outline"
          size="sm"
          data-testid="controls-save-view-open"
          disabled={!canSave}
          title={
            canSave
              ? "Save the current filter set as a named view"
              : "Apply at least one filter before saving a view"
          }
          onClick={() => setSaving(true)}
        >
          Save current filters
        </Button>
      )}
    </div>
  );
}
