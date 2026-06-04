// Slice 254 — pure helpers for the control-detail tab strip.
//
// Two responsibilities:
//   1. The TabKey type + validator (`isTabKey`). The seven tabs are
//      ordered to match the mockup (`Plans/_archive/mockups/control.html`
//      lines 143-149). The URL surface (`?tab=<key>`) is the
//      canonical source of truth; the validator narrows arbitrary
//      strings to the literal union so the page-level renderer can
//      switch exhaustively.
//
//   2. `formatTabCount(count)` renders a count chip per AC-2 +
//      JUDGMENT D3:
//        - integer counts render with comma thousands separators
//          (`1,247` rather than `1.2k`) — matches the mockup's
//          `847` literal + extends honestly past 999.
//        - null / undefined renders "—" (loading or errored query —
//          AC-2 explicitly: NOT a placeholder integer).
//
// Both helpers are pure (no React, no DOM, no router) so vitest
// covers them as a sibling test. The page-level component delegates
// so render tests don't have to re-derive the literal-union math.

export const CONTROL_TABS = [
  { key: "overview", label: "Overview" },
  { key: "evidence", label: "Evidence" },
  { key: "mappings", label: "Mappings" },
  { key: "scope", label: "Effective scope" },
  { key: "policies", label: "Policies" },
  { key: "risks", label: "Risks" },
  { key: "history", label: "History" },
] as const;

export type TabKey = (typeof CONTROL_TABS)[number]["key"];

const TAB_KEY_SET = new Set<string>(CONTROL_TABS.map((t) => t.key));

/**
 * isTabKey narrows an unknown string to the literal `TabKey` union.
 * Returns false for null / unrecognised values so the page-level
 * caller falls through to the default tab (`overview` per D2).
 */
export function isTabKey(raw: string | null | undefined): raw is TabKey {
  if (raw === null || raw === undefined) return false;
  return TAB_KEY_SET.has(raw);
}

/**
 * formatTabCount renders the count chip per AC-2 + D3.
 *
 * @param count  the backing query's integer count, or null/undefined
 *               when loading or errored. The page-level renderer
 *               passes null/undefined in both loading AND error
 *               branches so the chip is honest about "data not
 *               available" without distinguishing the two surfaces
 *               (the chip is a hint, not a diagnostic).
 *
 * @returns      "—" for null/undefined/NaN — NEVER a placeholder
 *               integer (AC-2). Comma-separated thousands for finite
 *               counts ("1,247", "847", "0"). Negative counts —
 *               which the backend never returns but defends against
 *               in case of a future bug — render "—".
 */
export function formatTabCount(count: number | null | undefined): string {
  if (count === null || count === undefined) return "—";
  if (!Number.isFinite(count)) return "—";
  if (count < 0) return "—";
  return Math.trunc(count).toLocaleString("en-US");
}
