// Issue #1741 — the /controls page's two views and the row mapping for
// each, as pure functions so they are vitest-pinned without React.
//
//   - "mine" (the default): the tenant's own active controls, from
//     `GET /v1/controls?include=state`. One row per control, keyed and
//     linked by the control id.
//   - "catalog": the SCF anchor catalog, from
//     `GET /v1/anchors?include=state`, with an optional server-side
//     `has_control` filter. A row links to the tenant control behind the
//     anchor when there is one, and is not a link otherwise.
//
// The page used to list only the catalog and link every row by anchor id,
// which the control detail page resolves as a control id, so every row
// opened the "no control in this tenant" empty state.

import type { AnchorWithState } from "@/lib/api/anchors";
import type { ControlWithState } from "@/lib/api/controls-list";

import { ALL, type AnchorRow, type AnchorRowState } from "./filters";

export type ControlsView = "mine" | "catalog";

/** URL key for the view toggle; absent means "mine". */
export const VIEW_PARAM = "view";

/** URL key for the catalog view's server-side "has control" filter. */
export const HAS_CONTROL_PARAM = "has_control";

export const VIEW_OPTIONS: { value: ControlsView; label: string }[] = [
  { value: "mine", label: "My controls" },
  { value: "catalog", label: "SCF catalog" },
];

export const HAS_CONTROL_OPTIONS: { value: string; label: string }[] = [
  { value: ALL, label: "All anchors" },
  { value: "true", label: "Has a control" },
  { value: "false", label: "No control" },
];

/** Any value other than "catalog" is the default "mine" view. */
export function parseControlsView(raw: string | null): ControlsView {
  return raw === "catalog" ? "catalog" : "mine";
}

/**
 * The `has_control` URL value as the boolean the BFF forwards, or
 * undefined (no filter) for an absent or unrecognised value.
 */
export function parseHasControl(raw: string | null): boolean | undefined {
  if (raw === "true") return true;
  if (raw === "false") return false;
  return undefined;
}

function toRowState(state: AnchorWithState["state"]): AnchorRowState | null {
  if (!state) return null;
  return {
    result: state.result,
    freshness_status: state.freshness_status,
    last_observed_at: state.last_observed_at,
  };
}

/**
 * One row per tenant control. The `anchor` slot carries the control's
 * own fields (title, family, SCF code) so the shared filters and columns
 * render it unchanged; its id is the control id.
 */
export function rowsFromControls(controls: ControlWithState[]): AnchorRow[] {
  return controls.map((c) => ({
    key: c.id,
    controlId: c.id,
    anchor: {
      id: c.id,
      scf_id: c.scf_id,
      family: c.control_family,
      name: c.title,
      description: "",
    },
    state: toRowState(c.state),
    frameworks: c.frameworks ?? [],
  }));
}

/** One row per catalog anchor, linked to its tenant control if any. */
export function rowsFromAnchors(anchors: AnchorWithState[]): AnchorRow[] {
  return anchors.map((a) => {
    const { state, frameworks, control_id, ...anchor } = a;
    return {
      key: anchor.id,
      controlId: control_id ?? null,
      anchor: {
        id: anchor.id,
        scf_id: anchor.scf_id,
        family: anchor.family,
        name: anchor.name,
        description: anchor.description,
      },
      state: toRowState(state),
      // Slice 226: older fixtures predate the column; the live BFF always
      // ships an array.
      frameworks: frameworks ?? [],
    };
  });
}

/** The control detail URL a row opens, or null when it has no control. */
export function controlHref(row: AnchorRow): string | null {
  return row.controlId
    ? `/controls/${encodeURIComponent(row.controlId)}`
    : null;
}

/**
 * The header count for each view (issue #1741). The "mine" view counts
 * the tenant's controls against the catalog size; the catalog view
 * counts anchors and how many of them the tenant has a control on.
 * Totals are only shown when the server reported them.
 */
export function controlsViewSummary(
  view: ControlsView,
  counts: {
    controls?: number;
    catalogTotal?: number;
    withControlTotal?: number;
  },
): string {
  const n = (v: number) => v.toLocaleString("en-US");
  if (view === "mine") {
    const controls = counts.controls ?? 0;
    const noun = controls === 1 ? "control" : "controls";
    const base = `${n(controls)} ${noun} in this tenant`;
    return counts.catalogTotal !== undefined
      ? `${base} · ${n(counts.catalogTotal)} SCF anchors in the catalog`
      : base;
  }
  if (counts.catalogTotal === undefined) return "";
  const withControl = counts.withControlTotal ?? 0;
  return `${n(withControl)} of ${n(
    counts.catalogTotal,
  )} SCF anchors have a control in this tenant`;
}
