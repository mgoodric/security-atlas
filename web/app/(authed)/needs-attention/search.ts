// #2135 — the /needs-attention page's URL state: `for` (the view),
// `type` (comma-separated item types) and `offset` (the page). Pure, so
// vitest covers it; the page reads and writes the URL through it.

import {
  NEEDS_ATTENTION_TYPES,
  type NeedsAttentionView,
} from "@/lib/api/needs-attention";

export type NeedsAttentionSearch = {
  // Undefined lets the server pick, as on the dashboard.
  view?: NeedsAttentionView;
  types: string[];
  offset: number;
};

const KNOWN_TYPES: readonly string[] = NEEDS_ATTENTION_TYPES;

// parseNeedsAttentionSearch reads the URL. Unknown types and a malformed
// offset are dropped rather than sent, so a stale link still loads.
export function parseNeedsAttentionSearch(
  sp: URLSearchParams,
): NeedsAttentionSearch {
  const f = sp.get("for");
  const view = f === "me" || f === "all" ? f : undefined;
  const types = (sp.get("type") ?? "")
    .split(",")
    .map((t) => t.trim())
    .filter((t, i, all) => KNOWN_TYPES.includes(t) && all.indexOf(t) === i);
  const n = Number(sp.get("offset") ?? "0");
  const offset = Number.isInteger(n) && n > 0 ? n : 0;
  return { view, types, offset };
}

// needsAttentionSearchString writes the URL state back, without empty
// keys.
export function needsAttentionSearchString(s: NeedsAttentionSearch): string {
  const q = new URLSearchParams();
  if (s.view) q.set("for", s.view);
  if (s.types.length > 0) q.set("type", s.types.join(","));
  if (s.offset > 0) q.set("offset", String(s.offset));
  const out = q.toString();
  return out ? `?${out}` : "";
}

// toggleType adds or removes one type filter, back on the first page.
export function toggleType(
  s: NeedsAttentionSearch,
  type: string,
): NeedsAttentionSearch {
  const types = s.types.includes(type)
    ? s.types.filter((t) => t !== type)
    : [...s.types, type];
  return { ...s, types, offset: 0 };
}
