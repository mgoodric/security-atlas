// Slice 448 — pure multi-select logic for the /controls list.
//
// All selection-set math lives here as pure functions so the node-env
// vitest runner (slice 069 P0-A3 — no JSX/DOM) covers the truth tables
// directly; the page is a thin React wrapper that holds the Set in
// state and renders the checkboxes. No React, no DOM here.
//
// THREAT MODEL (slice 448 §D — Denial of service): the selection that a
// single bulk request may carry is CAPPED. `SELECTION_CAP` is the
// per-request ceiling; the UI communicates it (AC-3) and `isOverCap` /
// `cappedSelection` enforce it before any mutation is dispatched. The
// server-backed bulk endpoint (slice-448 spillover) re-enforces the same
// cap server-side — the client cap is ergonomics, not the security
// boundary (the boundary is the server, per the threat model).

// Per-request selection ceiling (JUDGMENT — decisions log D2). 200 is a
// deliberate middle ground: comfortably above a realistic "assign every
// unowned control in my SOC 2 family" triage batch (the controls catalog
// is ~1,400 SCF anchors; a single family / framework slice is tens, not
// thousands), and low enough that one bulk mutation stays a bounded
// transaction. Above the cap the UI requires the operator to narrow the
// filter or deselect; it does not silently truncate.
export const SELECTION_CAP = 200;

/**
 * Toggle one id in a selection set, returning a NEW set (immutable — the
 * React state setter receives a fresh Set so the reference change
 * triggers a re-render). Adding an absent id selects it; toggling a
 * present id deselects it.
 */
export function toggleSelection(
  selected: ReadonlySet<string>,
  id: string,
): Set<string> {
  const next = new Set(selected);
  if (next.has(id)) {
    next.delete(id);
  } else {
    next.add(id);
  }
  return next;
}

/**
 * The tri-state of the "select all in view" header checkbox given the
 * currently-visible row ids and the selection set:
 *   - "none"  → no visible row is selected
 *   - "all"   → every visible row is selected (and there is at least one)
 *   - "some"  → a non-empty strict subset is selected (indeterminate)
 * Only the VISIBLE ids count — selecting all is scoped to the current
 * filtered page, never a hidden/global select-all (which would be the
 * unbounded-selection DoS the threat model rejects).
 */
export type SelectAllState = "none" | "some" | "all";

export function selectAllState(
  visibleIds: readonly string[],
  selected: ReadonlySet<string>,
): SelectAllState {
  if (visibleIds.length === 0) return "none";
  let selectedCount = 0;
  for (const id of visibleIds) {
    if (selected.has(id)) selectedCount += 1;
  }
  if (selectedCount === 0) return "none";
  if (selectedCount === visibleIds.length) return "all";
  return "some";
}

/**
 * Produce the next selection set when the header "select all in view"
 * checkbox is toggled. If every visible row is already selected, clears
 * the visible rows from the set (deselect-all-in-view); otherwise adds
 * every visible row to the set (select-all-in-view). Rows selected from
 * a prior (now-filtered-out) view are preserved on select, and only the
 * visible subset is removed on deselect — so toggling the header never
 * silently drops a selection the operator made under a different filter.
 */
export function toggleSelectAll(
  visibleIds: readonly string[],
  selected: ReadonlySet<string>,
): Set<string> {
  const next = new Set(selected);
  const state = selectAllState(visibleIds, selected);
  if (state === "all") {
    for (const id of visibleIds) next.delete(id);
  } else {
    for (const id of visibleIds) next.add(id);
  }
  return next;
}

/**
 * True when the selection exceeds the per-request cap. The UI blocks the
 * bulk action and surfaces the cap message (AC-3) when this is true.
 */
export function isOverCap(selected: ReadonlySet<string>): boolean {
  return selected.size > SELECTION_CAP;
}

/**
 * The selection narrowed to the cap, as a stable-ordered array. Used to
 * build the chunked request set; callers that hit the cap surface the
 * "narrow your selection" message rather than silently submitting the
 * first SELECTION_CAP ids — but the helper exists so the chunking math
 * is unit-pinned and the spillover server path reuses the same bound.
 */
export function cappedSelection(selected: ReadonlySet<string>): string[] {
  return Array.from(selected).slice(0, SELECTION_CAP);
}

/**
 * Drop any selected ids that are no longer present in the visible row
 * set. Called when the underlying row set changes (e.g. a fresh fetch
 * removed an anchor) so the selection never references a row the
 * operator can no longer see. Returns a new set; returns the same-shaped
 * set (a copy) when nothing changed.
 */
export function pruneSelection(
  selected: ReadonlySet<string>,
  presentIds: readonly string[],
): Set<string> {
  const present = new Set(presentIds);
  const next = new Set<string>();
  for (const id of selected) {
    if (present.has(id)) next.add(id);
  }
  return next;
}
