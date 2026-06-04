// Slice 448 — bulk-assign-owner future-state disclosure (label-honesty).
//
// SCOPE DECISION (docs/audit-log/448-bulk-ops-saved-views-decisions.md,
// D1). The slice spec describes a bulk-assign-OWNER action (AC-2) that
// re-checks role + tenant per control (AC-7) and audits the set (AC-8).
// That presumes a single-item assign-owner mutation to reuse as the
// per-item authz — but NO such path exists on `main`: the `controls`
// table carries `owner_role` (a read-only TEXT role string; rendered
// read-only on the control-detail page, with no edit/assign affordance
// anywhere), there is no owner-USER FK, and the only control-mutation
// endpoint is `POST /v1/controls:upload-bundle` (whole-bundle replace).
//
// Building the bulk-assign endpoint faithfully therefore means FIRST
// inventing the single-item owner-assign mutation + its authz + audit,
// THEN the bulk path with the per-item amplifier defense (AC-11 is
// load-bearing), PLUS a saved-views table with RLS. That is a large
// internal/api + migration surface — exactly the "balloons into
// internal/api" case the slice directive says to file as spillover
// rather than inflate this frontend ergonomics slice.
//
// So v1 ships the genuinely useful, self-contained ergonomics — the
// multi-select machinery (real: selection, select-all-in-view, cap) and
// the saved filter-views (real: persisted client-side) — and the
// bulk-assign ACTION is disclosed as future-state, NOT a vapor button
// that POSTs to a nonexistent endpoint (UI-honesty discipline, slice
// 178 / 225). The selection bar still shows the live selected count +
// cap so the operator sees exactly what the action WILL operate on when
// the server path lands.
//
// Mirrors the slice 225 new-control-future label-honesty pattern: a
// non-button disclosure carrying title + aria-label + a stable test-id.
// Constants exported so vitest pins the testid + Playwright asserts the
// load-bearing substring. Reversal when the server path ships: this
// `<span>` flips to a working bulk-assign trigger and this module
// deletes (one PR).

/** Test-id token surfaced on the bulk-assign future-state disclosure. */
export const BULK_ASSIGN_FUTURE_TESTID = "controls-bulk-assign-future";

/**
 * Disclosure copy — rendered as visible text AND title + aria-label.
 * Single source of truth. Copy discipline mirrors slice 225:
 *   * Future-tense ("lands in"), names the capability ("bulk
 *     assign-owner"), not a shuffleable slice number.
 *   * Honest about WHY (the per-control owner-assign mutation the bulk
 *     path re-checks does not exist yet) without leaking implementation.
 *   * The substring "bulk assign-owner" is load-bearing — the Playwright
 *     contract asserts on it.
 */
export const BULK_ASSIGN_FUTURE_REASON =
  "Bulk assign-owner lands in a future slice once the per-control owner-assign action ships; the selection below is ready to act on.";
