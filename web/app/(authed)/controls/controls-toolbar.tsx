"use client";

// Slice 448 — selection bar + saved filter-views bar for the /controls
// list. Presentational + interaction wiring only; all the set/persist
// math lives in the pure `selection.ts`, `saved-views.ts` and
// `bulk-actions.ts` modules (node-env vitest-covered). This `.tsx` is
// covered by the Playwright tier per `web/testing.md` (no vitest JSX —
// slice 069 P0-A3).
//
//   1. SelectionBar — visible only when at least one row is selected.
//      Shows the live count, the bulk actions the caller may take (#2141:
//      assign owner to anyone by picker, evaluate now, link to an action
//      plan, request exception) and a "Clear" action. Which actions show
//      is decided by `bulkActionGates` from the live /v1/me and the
//      caller's modules; the platform re-checks every write. The cap is
//      mentioned only when the selection exceeds it, and then every
//      action is disabled.
//   2. BulkResultMessage — the last bulk action's outcome, rendered
//      outside the selection bar so it survives the selection-clear
//      (slice 745). A per-control action lists each refusal with its
//      reason.
//   3. ExportMenu (#2141) — the six export links, folded into one menu.
//      Each keeps its URL, params and testid.
//   4. SavedViewsBar — always visible. A native <select> (matching the
//      slice-098 FilterPills idiom) to load a saved view, a "Save
//      current filters" button opening a small inline name form, and a
//      delete control for the loaded view. Per-user; slice 468 moved the
//      persistence from client localStorage to the server-backed
//      (tenant, user)-scoped store (the SavedViewStore seam swap).

import { Menu } from "@base-ui/react/menu";
import Link from "next/link";
import { useState } from "react";

import { Button, buttonVariants } from "@/components/ui/button";
import { EntityPicker } from "@/components/ui/entity-picker";
import { Input } from "@/components/ui/input";
import type { ActionPlan } from "@/lib/api/action-plans";

import {
  exceptionRequestHref,
  type BulkActionGates,
  type BulkMessage,
} from "./bulk-actions";
import { SELECTION_CAP } from "./selection";
import { MAX_VIEW_NAME_LENGTH, type SavedView } from "./saved-views";

export type SelectionBarProps = {
  selectedIds: readonly string[];
  overCap: boolean;
  gates: BulkActionGates;
  onClear: () => void;
  onAssignOwner: (ownerUserId: string) => void;
  onEvaluate: () => void;
  /** The plans offered by the link picker; undefined while loading. */
  plans: ActionPlan[] | undefined;
  /** Asked for when the link picker opens, so plans load on demand. */
  onWantPlans: () => void;
  onLinkPlan: (planId: string) => void;
  /** The action in flight, if any; every action is disabled meanwhile. */
  busy: "assign" | "evaluate" | "link" | null;
};

type Panel = "none" | "assign" | "link";

/**
 * Selection summary + the bulk actions. Rendered by the page only when at
 * least one control is selected.
 */
export function SelectionBar({
  selectedIds,
  overCap,
  gates,
  onClear,
  onAssignOwner,
  onEvaluate,
  plans,
  onWantPlans,
  onLinkPlan,
  busy,
}: SelectionBarProps) {
  const [panel, setPanel] = useState<Panel>("none");
  const [owner, setOwner] = useState("");
  const [planId, setPlanId] = useState("");
  const disabled = overCap || busy !== null;
  const count = selectedIds.length;

  return (
    <div
      data-testid="controls-selection-bar"
      role="region"
      aria-label="Bulk actions for selected controls"
      className="mb-3 space-y-2 rounded-lg border bg-card px-3 py-2 text-sm"
    >
      <div className="flex flex-wrap items-center gap-3">
        <span data-testid="controls-selection-count" className="font-medium">
          {count} selected
        </span>
        {gates.assignOwner ? (
          <Button
            type="button"
            size="sm"
            variant={panel === "assign" ? "default" : "outline"}
            data-testid="controls-bulk-assign-owner"
            aria-expanded={panel === "assign"}
            disabled={disabled}
            onClick={() => setPanel(panel === "assign" ? "none" : "assign")}
          >
            {busy === "assign" ? "Assigning…" : "Assign owner"}
          </Button>
        ) : null}
        {gates.evaluate ? (
          <Button
            type="button"
            size="sm"
            variant="outline"
            data-testid="controls-bulk-evaluate"
            disabled={disabled}
            onClick={onEvaluate}
          >
            {busy === "evaluate" ? "Evaluating…" : "Evaluate now"}
          </Button>
        ) : null}
        {gates.linkPlan ? (
          <Button
            type="button"
            size="sm"
            variant={panel === "link" ? "default" : "outline"}
            data-testid="controls-bulk-link-plan"
            aria-expanded={panel === "link"}
            disabled={disabled}
            onClick={() => {
              if (panel !== "link") onWantPlans();
              setPanel(panel === "link" ? "none" : "link");
            }}
          >
            {busy === "link" ? "Linking…" : "Link to action plan"}
          </Button>
        ) : null}
        {gates.requestException ? (
          overCap ? (
            <Button
              type="button"
              size="sm"
              variant="outline"
              data-testid="controls-bulk-request-exception"
              disabled
            >
              Request exception
            </Button>
          ) : (
            <Link
              href={exceptionRequestHref(selectedIds)}
              className={buttonVariants({ variant: "outline", size: "sm" })}
              data-testid="controls-bulk-request-exception"
            >
              Request exception
            </Link>
          )
        ) : null}
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
      {overCap ? (
        <p
          data-testid="controls-selection-overcap"
          role="alert"
          className="text-xs font-medium text-destructive"
        >
          A bulk action takes at most {SELECTION_CAP} controls. Narrow your
          filters or deselect some before applying one.
        </p>
      ) : null}
      {panel === "assign" && gates.assignOwner ? (
        <div
          className="flex flex-wrap items-end gap-2"
          data-testid="controls-bulk-assign-panel"
        >
          <div className="min-w-64 space-y-1">
            <label
              htmlFor="controls-bulk-owner"
              className="text-xs font-medium"
            >
              New owner
            </label>
            <EntityPicker
              kinds={["user"]}
              id="controls-bulk-owner"
              value={owner}
              onChange={setOwner}
              testId="controls-bulk-owner"
              placeholder="Search people by name"
            />
          </div>
          <Button
            type="button"
            size="sm"
            data-testid="controls-bulk-assign-confirm"
            disabled={disabled || owner === ""}
            onClick={() => {
              onAssignOwner(owner);
              setPanel("none");
              setOwner("");
            }}
          >
            Assign {count} control{count === 1 ? "" : "s"}
          </Button>
        </div>
      ) : null}
      {panel === "link" && gates.linkPlan ? (
        <div
          className="flex flex-wrap items-end gap-2"
          data-testid="controls-bulk-link-panel"
        >
          <label className="space-y-1">
            <span className="block text-xs font-medium">Action plan</span>
            <select
              aria-label="Action plan"
              data-testid="controls-bulk-plan-select"
              value={planId}
              onChange={(e) => setPlanId(e.target.value)}
              className="min-w-64 rounded-md border bg-background px-2 py-1.5 text-sm"
            >
              <option value="">
                {plans === undefined ? "Loading plans…" : "Choose a plan"}
              </option>
              {(plans ?? []).map((p) => (
                <option key={p.id} value={p.id}>
                  {p.title}
                </option>
              ))}
            </select>
          </label>
          {plans !== undefined && plans.length === 0 ? (
            <span className="text-xs text-muted-foreground">
              No action plans yet.{" "}
              <Link href="/action-plans/new" className="underline">
                Create one
              </Link>
            </span>
          ) : null}
          <Button
            type="button"
            size="sm"
            data-testid="controls-bulk-link-confirm"
            disabled={disabled || planId === ""}
            onClick={() => {
              onLinkPlan(planId);
              setPanel("none");
              setPlanId("");
            }}
          >
            Link {count} control{count === 1 ? "" : "s"}
          </Button>
        </div>
      ) : null}
    </div>
  );
}

export type BulkResultMessageProps = {
  /** The last bulk action's outcome, or null when there is none. */
  message: BulkMessage | null;
};

/**
 * Slice 745 — the bulk result, rendered by the page ABOVE the table and
 * INDEPENDENT of the selection, so it survives the selection-clear that
 * runs in the same batched update as the message set. #2141: a
 * per-control action lists each refused control with the platform's
 * reason. `role="status"` keeps the announcement non-interruptive.
 */
export function BulkResultMessage({ message }: BulkResultMessageProps) {
  if (!message) return null;
  return (
    <div
      data-testid="controls-bulk-assign-message"
      role="status"
      aria-live="polite"
      className={
        message.kind === "error"
          ? "mb-3 rounded-lg border border-destructive/40 bg-card px-3 py-2 text-sm font-medium text-destructive"
          : "mb-3 rounded-lg border bg-card px-3 py-2 text-sm font-medium text-muted-foreground"
      }
    >
      <p data-testid="controls-bulk-result-summary">{message.text}</p>
      {message.refusals && message.refusals.length > 0 ? (
        <ul
          className="mt-1 list-disc pl-5 text-xs font-normal"
          data-testid="controls-bulk-refusals"
        >
          {message.refusals.map((r, i) => (
            <li key={i} data-testid="controls-bulk-refusal">
              {r.label}: {r.reason}
            </li>
          ))}
        </ul>
      ) : null}
    </div>
  );
}

export type ExportLink = {
  href: string;
  label: string;
  testId: string;
};

/**
 * #2141 — every controls export in one menu. The links keep the URL,
 * params and testid each had as a header button; each is an
 * `<a download>` so the browser honours the backend's filename.
 */
export function ExportMenu({ links }: { links: readonly ExportLink[] }) {
  return (
    <Menu.Root>
      <Menu.Trigger
        className={buttonVariants({ variant: "outline", size: "sm" })}
        data-testid="controls-export-menu"
      >
        Export
        <span aria-hidden="true" className="ml-1 text-xs">
          ▾
        </span>
      </Menu.Trigger>
      <Menu.Portal>
        <Menu.Positioner align="end" sideOffset={4} className="z-50">
          <Menu.Popup
            className="min-w-48 rounded-lg bg-popover p-1 text-left text-popover-foreground shadow-md ring-1 ring-foreground/10 outline-none"
            data-testid="controls-export-menu-popup"
          >
            {links.map((l) => (
              <Menu.LinkItem
                key={l.testId}
                href={l.href}
                download
                rel="noopener"
                className="flex cursor-default rounded-md px-2 py-1.5 text-sm outline-none select-none data-highlighted:bg-muted"
                data-testid={l.testId}
              >
                {l.label}
              </Menu.LinkItem>
            ))}
          </Menu.Popup>
        </Menu.Positioner>
      </Menu.Portal>
    </Menu.Root>
  );
}

export type SavedViewsBarProps = {
  views: Pick<SavedView, "id" | "name">[];
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
