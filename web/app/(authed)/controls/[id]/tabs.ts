// Slice 254 — pure helpers for the control-detail tab strip.
//
// #2140 (front door, #2072) cut the seven tabs to three. The overview
// is the page: the state strip, then Evidence, Coverage and Linked
// sections, which absorbed the Evidence, Mappings, Effective scope,
// Policies and Risks tabs. The framework graph moved to its own
// "Framework map" tab, and History keeps the evaluation history.
//
// The URL surface (`?tab=<key>`) is the canonical source of truth; the
// validator narrows arbitrary strings to the literal union so the
// page-level renderer can switch exhaustively. A retired key
// (`?tab=evidence`, `?tab=policies`, ...) is not a key, so an old link
// lands on the overview, which now holds that content.
//
// Pure (no React, no DOM, no router) so vitest covers it as a sibling
// test.

export const CONTROL_TABS = [
  { key: "overview", label: "Overview" },
  { key: "map", label: "Framework map" },
  { key: "history", label: "History" },
] as const;

export type TabKey = (typeof CONTROL_TABS)[number]["key"];

const TAB_KEY_SET = new Set<string>(CONTROL_TABS.map((t) => t.key));

/**
 * isTabKey narrows an unknown string to the literal `TabKey` union.
 * Returns false for null / unrecognised values so the page-level
 * caller falls through to the default tab (`overview`).
 */
export function isTabKey(raw: string | null | undefined): raw is TabKey {
  if (raw === null || raw === undefined) return false;
  return TAB_KEY_SET.has(raw);
}
