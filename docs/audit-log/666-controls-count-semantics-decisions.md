# 666 — Controls list count semantics (decisions log)

**Slice type:** JUDGMENT (counter semantics / copy)
**Surface:** `web/app/(authed)/controls` (Controls list page) — frontend-only.

- detection_tier_actual: manual_review
- detection_tier_target: manual_review

(The contradiction was a copy/semantics defect surfaced by the 2026-06-10
empty-tenant browser audit, item ATLAS-007 — a manual-review-tier finding. No
automated tier would have flagged "two truthful strings that contradict each
other when read together"; this is the correct detection tier for a copy
inconsistency. The slice ADDS a unit tier + a hermetic e2e tier so a future
regression of the header phrasing is caught earlier than manual review.)

## Context

The Controls header read **"Showing 53 of 53 SCF anchors"** while the
pagination footer read **"Showing 1–50 of 53"** with Previous/Next controls.
Both strings were individually correct, but they shared the verb **"Showing"**
while meaning two different things:

- Header `visible.length of rows.length` = filtered subset of the catalog total.
- Footer `paginationBounds` = the current page's row range of the filtered set.

Read together, the header implied all 53 were on screen; the footer said only
the first 50 were. The contradiction is a copy/semantics defect, not a data
bug — the total of 53 is correct (anti-criterion: no page-size or count change).

## Decisions made

### D1 — The header drops the verb "Showing" and becomes a plain COUNT.

- **Options considered:**
  - (a) Make the header say "Showing 53 of 53 SCF anchors (page 1 of 2)" —
    rejected: doubles down on the colliding verb and adds chrome the footer
    already owns.
  - (b) Make the header a page range too ("Showing 1–50 of 53 SCF anchors")
    and drop the footer summary — rejected: the footer (`<ListPagination>`) is
    a SHARED primitive used by /risks, /controls, /policies; changing it is
    out of scope and would touch sibling pages. Also, the header's job is to
    convey the _catalog/filter_ size, not the page slice.
  - (c) **Chosen:** the footer keeps sole ownership of the "Showing M–N of
    TOTAL" page-range phrasing; the header becomes a plain count of the
    filtered catalog with the verb removed entirely:
    - unfiltered: `53 SCF anchors`
    - filtered: `42 of 53 SCF anchors`
- **Rationale:** With the verb gone, the header reads unambiguously as a
  count and the footer reads as a range. They no longer collide. The filtered
  total ("42 of 53") drives the header (AC-3); the footer's TOTAL is that same
  filtered count ("Showing 1–50 of 42"), so the two are now mutually
  consistent. The footer is untouched, so no sibling list page changes.
- **Confidence:** high.

### D2 — Filtered-vs-unfiltered branch in the header copy.

- **Chosen:** when `filtered === total` (the common, no-filter case) the header
  collapses to the plain total ("53 SCF anchors") rather than the redundant
  "53 of 53 SCF anchors". When a filter narrows the set, it reads
  "N of M SCF anchors".
- **Rationale:** "53 of 53" is exactly the phrasing the audit flagged as
  misleading; collapsing it to "53 SCF anchors" removes the "all of them are
  here" implication (AC-2) and is terser for the default view. The "N of M"
  form is retained only when it carries real information (a filter is active).
- **Confidence:** high.

### D3 — Extract the label into a pure, unit-tested module.

- **Chosen:** the wording + the filtered/total branch live in
  `web/app/(authed)/controls/count-label.ts` (pure, `controlsCountLabel`),
  vitest-covered to 100%; the page renders the returned parts in styled spans.
- **Rationale:** mirrors the project's pure-logic-seam convention (filters.ts,
  selection.ts, saved-views.ts on this same page). It lets the semantics be
  unit-tested without a React tree and gives a greppable home for the copy.
  A coverage floor was added in the same PR per the ratchet contract.
- **Confidence:** high.

### D4 — Defensive clamping in the label.

- **Chosen:** clamp negatives to 0 and `filtered > total` down to `total` so
  the label can never read "60 of 53" or a negative count, even though the page
  never produces `filtered > total` (`visible` is a subset of `rows`).
- **Rationale:** cheap belt-and-suspenders; keeps the pure function total over
  its input domain so the unit tests fully pin behaviour.
- **Confidence:** high.

## Revisit once in use

- **Plural/singular:** the noun is always "SCF anchors" (plural). A catalog with
  exactly 1 anchor reads "1 SCF anchors". v1 catalogs are ~50–1,400 anchors so
  the singular case is effectively unreachable, but if a future deployment can
  show exactly one anchor, revisit pluralization. (Low priority.)
- **Sibling /risks page has the identical contradiction** ("Showing X of Y
  risks" header + "Showing M–N of TOTAL" footer). Out of scope for this slice
  (scope is `web/app/(authed)/controls`); captured as a follow-up spillover
  slice. Apply the same fix there when picked up.
- **Localization:** the "N of M" / "{total} SCF anchors" wording is English-only
  string concatenation. When i18n lands, this label needs a message-format
  entry rather than template-literal concatenation.

## Confidence summary

| Decision                                      | Confidence |
| --------------------------------------------- | ---------- |
| D1 — header is a count, footer owns the range | high       |
| D2 — collapse "53 of 53" → "53 SCF anchors"   | high       |
| D3 — pure module + unit tests                 | high       |
| D4 — defensive clamping                       | high       |
