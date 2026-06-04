// Slice 448 — per-user saved filter-views for the /controls list.
//
// SCOPE DECISION (recorded in docs/audit-log/448-bulk-ops-saved-views-
// decisions.md, D1): v1 persists saved views CLIENT-SIDE in
// localStorage, per-user-per-browser. The spec's AC-9 describes a
// server-backed, RLS-scoped (tenant, user) saved-views table + BFF
// route. That backend surface — together with the prerequisite single-
// item owner-assign mutation the bulk path is supposed to reuse (which
// does NOT exist on `main`: the controls table carries `owner_role`, a
// read-only TEXT role string, with no assign affordance) — is a large
// internal/api + migration surface filed as the slice-448 server-backed
// spillover. This module is the v1 client persistence the spillover
// later swaps for a fetch-backed store (the page injects the store, so
// the swap is a one-line change here).
//
// This module mirrors the slice-103 `settings/theme.ts` persistence
// contract: a pure `.ts` module with a `Store` interface (the page
// passes `window.localStorage`; tests pass an in-memory shim) so the
// node-env vitest runner (slice 069 P0-A3 — no JSX/DOM) can pin the
// contract directly. No React, no BFF, no `window` reference here.
//
// THREAT MODEL (slice 448 §I — Information disclosure): saved views
// persist user-entered FILTER CRITERIA only — never data rows, never
// cross-tenant IDs. The validated shape (below) is exactly the slice-224
// filter-pill keys; an arbitrary-JSON payload that could become a query
// is rejected on read (D3). Per-user/per-tenant isolation in the v1
// client store is the browser profile boundary; the spillover moves
// isolation to RLS on (tenant, user).

import { ALL, DEFAULT_FILTERS, type ControlFilters } from "./filters";

// Pinned storage key. Changing it silently orphans every user's saved
// views — the corresponding test fails on rename so the cost is visible
// (same discipline as THEME_STORAGE_KEY).
export const SAVED_VIEWS_STORAGE_KEY = "security-atlas.controls.saved-views";

// The filter keys a saved view is allowed to carry. This is the
// validation allow-list (threat-model T/I): a persisted view's payload
// is narrowed to exactly these keys on read, so no arbitrary JSON
// round-trips into the live filter state. Mirrors FILTER_KEYS in
// page.tsx; kept here so the validation is unit-testable without React.
export const SAVED_VIEW_FILTER_KEYS: (keyof ControlFilters)[] = [
  "framework",
  "family",
  "result",
  "freshness",
  "scope",
];

// Maximum number of saved views per user (v1 cap). A solo operator's
// weekly/triage/audit-prep views are a handful; the cap keeps the
// localStorage payload bounded and the list UI scannable. Saving past
// the cap is rejected by `addView` (the caller surfaces the message).
export const MAX_SAVED_VIEWS = 20;

// Maximum length of a user-supplied view name. Names are display-only
// labels; the cap bounds the stored payload and the list rendering.
export const MAX_VIEW_NAME_LENGTH = 60;

export type SavedView = {
  // Stable client-generated id (used as the React key + delete handle).
  id: string;
  // User-supplied display name (trimmed, length-capped, non-empty).
  name: string;
  // The filter-pill state this view restores. Validated to the
  // allow-list above on every read.
  filters: ControlFilters;
};

// Storage-shaped subset of the DOM Storage interface. The page passes
// `window.localStorage`; tests pass an in-memory shim.
export interface SavedViewStore {
  getItem(key: string): string | null;
  setItem(key: string, value: string): void;
}

/**
 * Coerce an unknown persisted filter object to a valid ControlFilters by
 * narrowing to the allow-list keys. Unknown keys are dropped (threat-
 * model T — no arbitrary JSON becomes live filter state); missing or
 * non-string values fall back to the `ALL` sentinel for that key.
 */
export function sanitizeFilters(raw: unknown): ControlFilters {
  const out: ControlFilters = { ...DEFAULT_FILTERS };
  if (typeof raw !== "object" || raw === null) return out;
  const obj = raw as Record<string, unknown>;
  for (const key of SAVED_VIEW_FILTER_KEYS) {
    const v = obj[key];
    out[key] = typeof v === "string" && v.length > 0 ? v : ALL;
  }
  return out;
}

/**
 * Validate + normalize a single persisted view entry. Returns null when
 * the entry is structurally invalid (so a corrupt/partial localStorage
 * blob degrades to "fewer views" rather than throwing). A valid entry
 * has a non-empty string id, a non-empty trimmed name, and a sanitized
 * filter set.
 */
export function parseView(raw: unknown): SavedView | null {
  if (typeof raw !== "object" || raw === null) return null;
  const obj = raw as Record<string, unknown>;
  const id = typeof obj.id === "string" ? obj.id : "";
  const name = typeof obj.name === "string" ? obj.name.trim() : "";
  if (id.length === 0 || name.length === 0) return null;
  return {
    id,
    name: name.slice(0, MAX_VIEW_NAME_LENGTH),
    filters: sanitizeFilters(obj.filters),
  };
}

/**
 * Read + validate the persisted view list. Returns `[]` when nothing is
 * stored or the stored blob is invalid JSON / not an array. Each entry
 * is run through `parseView`; structurally invalid entries are dropped
 * (the list never throws on a corrupt payload).
 */
export function readViews(store: SavedViewStore): SavedView[] {
  const raw = store.getItem(SAVED_VIEWS_STORAGE_KEY);
  if (!raw) return [];
  let parsed: unknown;
  try {
    parsed = JSON.parse(raw);
  } catch {
    return [];
  }
  if (!Array.isArray(parsed)) return [];
  const out: SavedView[] = [];
  for (const entry of parsed) {
    const v = parseView(entry);
    if (v) out.push(v);
  }
  return out;
}

/**
 * Persist a view list. Idempotent. Throws only if the underlying Storage
 * implementation throws (quota) — the caller decides how to surface it.
 */
export function writeViews(store: SavedViewStore, views: SavedView[]): void {
  store.setItem(SAVED_VIEWS_STORAGE_KEY, JSON.stringify(views));
}

// Result of an add attempt. `ok=false` carries a human message the UI
// surfaces (empty name, duplicate name, or cap reached).
export type AddViewResult =
  | { ok: true; views: SavedView[] }
  | { ok: false; reason: "empty-name" | "duplicate-name" | "cap-reached" };

/**
 * Produce the next view list with a new view appended. Pure — does NOT
 * touch storage (the caller persists the returned list). Enforces:
 *   - non-empty trimmed name (empty-name)
 *   - no case-insensitive duplicate of an existing name (duplicate-name)
 *   - the MAX_SAVED_VIEWS cap (cap-reached)
 * The id is supplied by the caller (the page uses crypto.randomUUID) so
 * this stays deterministic + node-testable.
 */
export function addView(
  existing: SavedView[],
  id: string,
  name: string,
  filters: ControlFilters,
): AddViewResult {
  const trimmed = name.trim();
  if (trimmed.length === 0) return { ok: false, reason: "empty-name" };
  if (existing.length >= MAX_SAVED_VIEWS) {
    return { ok: false, reason: "cap-reached" };
  }
  const lower = trimmed.toLowerCase();
  if (existing.some((v) => v.name.toLowerCase() === lower)) {
    return { ok: false, reason: "duplicate-name" };
  }
  const view: SavedView = {
    id,
    name: trimmed.slice(0, MAX_VIEW_NAME_LENGTH),
    filters: sanitizeFilters(filters),
  };
  return { ok: true, views: [...existing, view] };
}

/**
 * Produce the next view list with the view of the given id removed.
 * Pure — the caller persists the returned list. A no-op (returns a copy)
 * when the id is not present.
 */
export function removeView(existing: SavedView[], id: string): SavedView[] {
  return existing.filter((v) => v.id !== id);
}

/**
 * Look up a saved view by id. Returns null when not found.
 */
export function findView(views: SavedView[], id: string): SavedView | null {
  return views.find((v) => v.id === id) ?? null;
}
