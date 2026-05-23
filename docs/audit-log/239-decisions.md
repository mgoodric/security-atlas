# 239 — /policies list header inline status counts · decisions log

**Slice:** `docs/issues/239-policies-list-header-missing-inline-status-counts.md`
**Branch:** `frontend/239-policies-header-counts`
**Type:** `AFK` (slice type; decisions log included per project convention for the audit trail)
**Author:** Claude (engineer subagent)
**Date:** 2026-05-23

The slice doc specifies the WHAT. This log records the small set of HOW
calls I made inline. The slice is mechanically verifiable (vitest
formatter — 13 cases covering AC-5(a)/(b)/(c)/(d) plus count-0 omission
plus alphabetical-tail ordering); these notes exist for the maintainer's
post-merge review rather than to gate the merge.

---

## D1 — Counts derived from `rows` (full set), not `visible` (filter-narrowed)

**Decision:** `statusCountsLabel(rows)` — feed the FULL policies list
the TanStack Query returns, not the filter-narrowed `visible` set.

**Why:** AC-1 is explicit ("uses `rows.length`-derived counts (not the
filtered `visible.length`), so the summary is a stable aggregate, not
a filter-sensitive figure"). Mirrors the slice 215 D1 precedent for
the same shell slot (`titleAdornment`). The "Showing N of M policies"
meta-text in the filter row already plays the filtered-readout role —
duplicating it in the header would be noise.

**Alternatives considered:**

| Approach                      | Why rejected                                                                                                                                      |
| ----------------------------- | ------------------------------------------------------------------------------------------------------------------------------------------------- |
| Counts on `visible`           | Re-frames the tally as a filter readout. The "Showing N of M" meta in the filter row is the correct surface for that. AC-1 forbids it explicitly. |
| Two tallies (full + filtered) | Visual noise. The mockup at `Plans/mockups/policies.html` line 111 shows one. Adds slot to the shell for one route's use.                         |

**Confidence:** `high`. AC-1 leaves no ambiguity.

---

## D2 — String format: `<N> published · <M> draft · <K> retired` + alphabetical tail

**Decision:** The formatter renders the canonical three statuses
(`published`, `draft`, `retired`) in the slice-prescribed order, then
appends any other status present in the data (`under_review`,
`approved`, or any future enum addition) in alphabetical order. Zero-
count statuses are omitted. Separator is " · " (U+00B7 MIDDLE DOT
with single spaces) — same as the slice 215 audits tally on the same
shell, and verbatim with `Plans/mockups/policies.html` line 111.

**Why:** AC-2 explicitly chose the "enumerated explicitly when >= 1
row" variant over an "X other" rollup ("the explicit-when-present
variant to avoid hiding signal"). The platform's policy status enum
today is {`published`, `draft`, `under_review`, `approved`,
`retired`} — the canonical three are the mockup-prominent ones; the
two intermediate states fall into the alphabetical tail when present.
Alphabetical tail (rather than insertion-order) keeps the rendering
deterministic regardless of input shape and matches slice 215 D3.

**Alternatives considered:**

| Approach                                                     | Why rejected                                                                                                                                                                                                                 |
| ------------------------------------------------------------ | ---------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- |
| `<N> published · <M> draft · <K> retired · <X> other` rollup | AC-2 explicitly rejects this — "rolled into a fourth `· <X> other` segment if any rows carry those statuses, OR they're enumerated explicitly" — and the AC explicitly chose the enumerated variant for signal preservation. |
| Render ALL canonical statuses always, even count-0           | Violates AC-4 ("`0 published · 0 draft · 0 retired` is NOT rendered"). Also mirrors slice 215's P0-215-1 — zero counts are noise.                                                                                            |
| Title-case the status strings ("Published")                  | Tone-discipline violation. Row pills, page mockup, slice 215 audits tally all render the platform enum literally lowercased. Consistency wins.                                                                               |
| Translate `under_review` → `under review` (space)            | Diverges from the row pill text on the same page (the existing `STATUS_OPTIONS` in `./page.tsx` shows the underscore form). Slice 215 D2 settled the same call for `in_progress`.                                            |

**Confidence:** `high` on the structure; `medium` on the underscore-
vs-space form for `under_review`. Same flip path as slice 215 D2 if
the maintainer prefers the prettier form after seeing it in context.

---

## D3 — `ListPage.titleAdornment` slot reuse (no shell change)

**Decision:** Render the tally via the existing
`titleAdornment` prop on `web/components/list/list-page.tsx` —
added by slice 215 (`e61d3154`) for the /audits page tally.

**Why:** The slot was designed exactly for this — `flex items-baseline`
row with the H1, semantic muted-text styling, opt-in via prop. Slice
215 D4 captures the rationale (mockup puts the inline value on the H1
baseline, not in the subtitle slot). The slot is additive — all other
list pages continue to render exactly as they did. No shell code
change in this slice; the wiring is pure consumption.

**Confidence:** `high`. Slot was purpose-built; this is its second
consumer.

---

## D4 — Tally hidden in loading + error states

**Decision:** The `titleAdornment` is only passed to the populated
`<ListPage>` branch. The loading-skeleton branch and the error-alert
branch render the page chrome WITHOUT the tally.

**Why:** AC-3 says "the summary is omitted when `rows.length === 0`"
— the loading branch has no rows yet (query is pending), so it falls
under that condition. The error branch has no trustworthy rows
(query failed); showing a stale tally would be wrong. The empty-
string sentinel from `statusCountsLabel([])` handles this
automatically because the renderer treats `""` as "do not render".
Mirrors slice 215 D5 exactly.

**Confidence:** `high`. Falls out of the formatter contract.

---

## Revisit once in use

Concrete items the maintainer should re-evaluate when real tenants
exist:

1. **`under_review` rendering (underscore vs space).** Same call as
   slice 215 D2's `in_progress`. If operators read the tally and the
   row pill side-by-side and the underscore looks jarring, switch via
   a single-line transform in `statusCountsLabel`. Low-effort flip.
2. **Tail status ordering.** If the policy status enum lifts and the
   alphabetical tail reads oddly with the real status mix (e.g. all-
   active-states-first looks more natural), tighten the rule. Today
   alphabetical is the cheapest deterministic choice.
3. **Tally-vs-meta consistency under filters.** Today the header
   tally is stable (always reflects `rows`); the filter-row meta
   ("Showing X of Y") is filter-sensitive. Validate operators don't
   misread them as two views of the same number. If so, label the
   tally explicitly (e.g. "library: 14 published · …") — would
   diverge from the mockup but help comprehension.

## Confidence summary

| Decision                                           | Confidence                                      |
| -------------------------------------------------- | ----------------------------------------------- |
| D1 — full-set vs filtered-set counts               | `high`                                          |
| D2 — string format (canonical + alphabetical tail) | `high` (structure) / `medium` (underscore form) |
| D3 — reuse `titleAdornment` slot, no shell change  | `high`                                          |
| D4 — tally hidden in loading + error states        | `high`                                          |

Top of the revisit list: D2's underscore-vs-space form for
`under_review`, paired with the same revisit on slice 215 D2.
