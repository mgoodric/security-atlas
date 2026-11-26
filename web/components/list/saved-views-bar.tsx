"use client";

// Saved filter-views bar, shared by the /controls list (slice 448) and the
// risk register (#2133); moved here from the controls toolbar (#2550). A
// native <select> (matching the slice-098 FilterPills idiom) loads a saved
// view, a "Save current filters" button opens a small inline name form,
// and a delete control removes the loaded view. Per-user; slice 468 moved
// the persistence to the server-backed (tenant, user)-scoped store. Each
// page owns its views and their persistence; this is presentational. The
// Playwright tier covers it (no vitest JSX, slice 069 P0-A3).

import { useState } from "react";

import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { MAX_VIEW_NAME_LENGTH } from "@/lib/api/saved-views";

export type SavedViewsBarProps = {
  views: { id: string; name: string }[];
  /** The id of the currently-loaded view, or "" when none is active. */
  activeViewId: string;
  /** True when the current filter set is non-default (worth saving). */
  canSave: boolean;
  onLoadView: (id: string) => void;
  // Slice 468 — save is now a server round-trip, so onSaveView is async.
  onSaveView: (
    name: string,
  ) => Promise<{ ok: true } | { ok: false; message: string }>;
  onDeleteView: (id: string) => void;
  /** Prefix of every test id, so a second list can host the bar (#2133). */
  testIdPrefix?: string;
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
  testIdPrefix = "controls",
}: SavedViewsBarProps) {
  const [saving, setSaving] = useState(false);
  const [name, setName] = useState("");
  const [error, setError] = useState<string | null>(null);
  const [submitting, setSubmitting] = useState(false);

  const errorId = error ? `${testIdPrefix}-save-view-error` : undefined;

  async function submitSave() {
    if (submitting) return;
    setSubmitting(true);
    try {
      const result = await onSaveView(name);
      if (result.ok) {
        setName("");
        setSaving(false);
        setError(null);
      } else {
        setError(result.message);
      }
    } finally {
      setSubmitting(false);
    }
  }

  return (
    <div
      data-testid={`${testIdPrefix}-saved-views-bar`}
      className="mb-3 flex flex-wrap items-center gap-2 text-xs"
    >
      <label className="flex items-center gap-1.5 rounded-md border bg-card px-2.5 py-1.5">
        <span className="text-muted-foreground">Saved view</span>
        <select
          aria-label="Saved view"
          data-testid={`${testIdPrefix}-saved-views-select`}
          value={activeViewId}
          onChange={(e) => onLoadView(e.target.value)}
          className="rounded-sm bg-transparent font-medium text-foreground focus-visible:outline-2 focus-visible:outline-offset-2 focus-visible:outline-ring"
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
          data-testid={`${testIdPrefix}-saved-views-delete`}
          onClick={() => onDeleteView(activeViewId)}
        >
          Delete view
        </Button>
      ) : null}

      {saving ? (
        <span className="flex flex-wrap items-center gap-1.5">
          <Input
            aria-label="New view name"
            data-testid={`${testIdPrefix}-save-view-name`}
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
                void submitSave();
              }
            }}
            className="h-8 w-44"
          />
          <Button
            type="button"
            size="sm"
            disabled={submitting}
            data-testid={`${testIdPrefix}-save-view-confirm`}
            onClick={() => void submitSave()}
          >
            {submitting ? "Saving…" : "Save"}
          </Button>
          <Button
            type="button"
            variant="ghost"
            size="sm"
            data-testid={`${testIdPrefix}-save-view-cancel`}
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
              id={`${testIdPrefix}-save-view-error`}
              role="alert"
              aria-live="polite"
              data-testid={`${testIdPrefix}-save-view-error`}
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
          data-testid={`${testIdPrefix}-save-view-open`}
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
