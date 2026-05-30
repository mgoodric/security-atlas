// Slice 239 — pure formatter for the /policies list header status tally.
//
// Derives an inline "14 published · 2 draft · 1 retired" string from the
// same policies list the table renders. Pure data → string; no React,
// no DOM (vitest-unit-testable without a tree).
//
// The mockup at `Plans/mockups/policies.html` lines 110-111 carries an
// inline header tally on the same baseline as the H1; the live page
// (`web/app/(authed)/policies/page.tsx`) renders only the title +
// subtitle today, which is the gap slice 239 closes. The shared
// `ListPage` shell already exposes a `titleAdornment` slot (added by
// slice 215 for the /audits tally — same shape, same render position),
// so this slice consumes that slot without modifying the shell.
//
// Rendering rules (mirror the slice 239 ACs):
//   - AC-1: shape is `<N> published · <M> draft · <K> retired`,
//     counts derived from the full `rows` list (NOT the filtered
//     `visible` set — the tally is the one-glance "this is the right
//     tenant" check, stable as the operator fiddles with filters).
//   - AC-2: status bins shown are `published`, `draft`, `retired`
//     (the canonical three). Any other status that appears in the data
//     (`under_review`, `approved`, or anything else the backend lifts
//     later) is enumerated EXPLICITLY — appended after the canonical
//     three in alphabetical order — rather than rolled into a
//     "X other" bucket. The slice text explicitly chose this variant
//     to avoid hiding signal.
//   - AC-3: empty input → empty string. The caller uses the empty
//     string as a render-or-skip flag (e.g. `tally ? <span>{tally}</span>
//     : null`).
//   - AC-4: count-0 statuses are OMITTED. The string "0 published"
//     never renders.
//
// Constitutional anti-criterion P0-239-3 honored: status-count header
// only — no filter-pill or pagination scope creep here.
//
// Separator is " · " (U+00B7 MIDDLE DOT with single spaces) to match
// the mockup at `Plans/mockups/policies.html` line 111 verbatim and to
// stay consistent with the slice 215 audits tally on the same shell.

import type { Policy } from "@/lib/api/policies";

/**
 * Canonical status order for the policies header tally. Mirrors the
 * mockup string "14 published · 2 draft · 1 retired" verbatim.
 *
 * The platform enum (per `STATUS_OPTIONS` in `./page.tsx`) is
 * `published`, `draft`, `under_review`, `approved`, `retired`. The
 * mockup highlights three of those; `under_review` and `approved` are
 * the in-flight intermediate states that fall through to the
 * alphabetical tail per AC-2 when at least one row carries them.
 */
export const TALLY_STATUS_ORDER: readonly string[] = [
  "published",
  "draft",
  "retired",
];

/**
 * Build the inline status-count string for the policies header.
 *
 * Pure function: counts rows by `status`, renders the canonical three
 * in the prescribed order, then appends any non-canonical statuses
 * (with count >= 1) in alphabetical order. Zero-count entries are
 * always omitted. Empty input returns "" so the caller can skip
 * rendering entirely (matches slice 215 contract).
 *
 * @example
 *   statusCountsLabel([])
 *   // ""
 *
 *   statusCountsLabel([
 *     ...published(14), ...draft(2), ...retired(1)
 *   ])
 *   // "14 published · 2 draft · 1 retired"
 *
 *   statusCountsLabel([
 *     ...published(14), ...draft(2), ...retired(1),
 *     ...underReview(3),
 *   ])
 *   // "14 published · 2 draft · 1 retired · 3 under_review"
 */
export function statusCountsLabel(rows: Policy[]): string {
  if (rows.length === 0) return "";

  // Count per status — only statuses present in the data appear in the
  // map, so the renderer never invents statuses.
  const counts = new Map<string, number>();
  for (const r of rows) {
    counts.set(r.status, (counts.get(r.status) ?? 0) + 1);
  }

  // Canonical statuses first, in the slice-prescribed order. The map
  // only contains statuses we observed, so count-0 entries can't
  // appear here — but guard explicitly for clarity.
  const canonical: string[] = [];
  for (const status of TALLY_STATUS_ORDER) {
    const n = counts.get(status);
    if (n && n > 0) {
      canonical.push(`${n} ${status}`);
      counts.delete(status);
    }
  }

  // Remaining (non-canonical) statuses — sort alphabetically for
  // deterministic output. Examples today: `under_review`, `approved`.
  // Future statuses fall here naturally without a code change.
  const remaining = [...counts.entries()]
    .sort((a, b) => a[0].localeCompare(b[0]))
    .map(([status, n]) => `${n} ${status}`);

  return [...canonical, ...remaining].join(" · ");
}
