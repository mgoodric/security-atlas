# 684 — Risks list count semantics (decisions log)

**Slice type:** JUDGMENT (counter semantics / copy)
**Surface:** `web/app/(authed)/risks` (Risks list page) — frontend-only.

- detection_tier_actual: manual_review
- detection_tier_target: manual_review

(The contradiction was a copy/semantics defect, the sibling of the /controls
finding slice 666 fixed — surfaced during slice 666 and captured as a follow-up
per the continuous-batch spillover policy. No automated tier would have flagged
"two truthful strings that contradict each other when read together"; manual
review is the correct detection tier for a copy inconsistency. This slice ADDS a
unit tier + a hermetic e2e tier so a future regression of the header phrasing is
caught earlier than manual review.)

## Context

The `/risks` header read **"Showing {visible.length} of {rows.length} risks"**
while the shared pagination footer (`<ListPagination>`) read **"Showing M–N of
TOTAL"** with Previous/Next controls. Both strings were individually correct, but
they shared the verb **"Showing"** while meaning two different things:

- Header `visible.length of rows.length` = filtered subset of the register total.
- Footer `paginationBounds` = the current page's row range of the filtered set.

Read together, the header implied all rows were on screen; the footer said only
the first page was. The contradiction is a copy/semantics defect, not a data bug
— the totals are correct (anti-criterion: no page-size or count change). This is
the identical defect slice 666 fixed on `/controls`; this slice mirrors that fix.

## Decisions made

### D1 — Page-local module, NOT a shared generic (the headline JUDGMENT call).

- **Options considered:**
  - (a) **Chosen:** add a page-local
    `web/app/(authed)/risks/count-label.ts` exporting a pure
    `risksCountLabel(filtered, total)`, mirroring slice 666's deliberate
    page-local seam and this page's existing per-page convention
    (`filters.ts`).
  - (b) Generalize into a shared `web/components/list/count-label.ts` consumed
    by BOTH /controls and /risks, refactoring /controls onto it — rejected.
- **Rationale:** The slice notes either is reasonable and says "if unsure, prefer
  (a)." Three reasons make (a) the better cost/benefit here, not merely the
  default:
  1. **Noun differs per page** ("risks" vs "SCF anchors"). A shared generic
     would need a noun parameter, so the "generalization" is really
     `countLabel(filtered, total, noun)` — a thin wrapper over string
     concatenation whose only shared logic is the clamp + the
     filtered/unfiltered branch (a few lines). Modest reuse benefit.
  2. **Blast radius.** (b) forces editing the just-merged slice-666
     `/controls` page, its test file, and its coverage-threshold key — churning
     a freshly-landed file and widening this XS slice's scope beyond the stated
     `web/app/(authed)/risks/` boundary for no functional gain.
  3. **Project convention.** The page-local pure-seam pattern
     (`filters.ts`, `selection.ts`, `saved-views.ts`) is the established shape,
     and slice 666 D3 explicitly chose page-local for exactly this label. Two
     consumers is the textbook "rule of three" under-threshold — extract on the
     third, not the second.
- **Forward note:** if a THIRD list page needs the same header (e.g. /policies,
  which already has its own `header-counts.ts`), that is the moment to lift a
  shared `web/components/list/count-label.ts` and migrate /controls + /risks
  onto it in one consolidation slice. Captured in "Revisit once in use."
- **Confidence:** high.

### D2 — The header drops the verb "Showing" and becomes a plain COUNT.

- **Chosen:** the footer keeps sole ownership of the "Showing M–N of TOTAL"
  page-range phrasing; the header becomes a plain count of the filtered register
  with the verb removed entirely:
  - unfiltered: `53 risks`
  - filtered: `42 of 53 risks`
- **Rationale:** identical to slice 666 D1. With the verb gone, the header reads
  unambiguously as a count and the footer reads as a range; they no longer
  collide (AC-1, AC-2). The filtered total drives the header (AC-3); the footer's
  TOTAL is that same filtered count, so the two are mutually consistent. The
  shared footer is untouched (anti-criterion), so no sibling list page changes.
- **Confidence:** high.

### D3 — Filtered-vs-unfiltered branch in the header copy.

- **Chosen:** when `filtered === total` (the common, no-filter case) the header
  collapses to the plain total ("53 risks") rather than the redundant
  "53 of 53 risks". When a filter narrows the set, it reads "N of M risks".
- **Rationale:** "53 of 53" is exactly the misleading phrasing; collapsing it to
  "53 risks" removes the "all of them are here" implication (AC-2) and is terser
  for the default view. The "N of M" form is retained only when it carries real
  information (a filter is active).
- **Confidence:** high.

### D4 — Defensive clamping in the label.

- **Chosen:** clamp negatives to 0 and `filtered > total` down to `total` so the
  label can never read "60 of 53" or a negative count, even though the page never
  produces `filtered > total` (`visible` is a subset of `rows`).
- **Rationale:** cheap belt-and-suspenders; keeps the pure function total over
  its input domain so the unit tests fully pin behaviour. Mirrors slice 666 D4.
- **Confidence:** high.

### D5 — Hermetic e2e mirroring slice 666, mocking 53 risks.

- **Chosen:** a new `web/e2e/risks-count-consistency.spec.ts` route-mocks
  `/api/risks` (53 risks) and `/api/risks-hierarchy/org-units` (empty) per the
  slice-594 shared-DB → hermetic-mock convention, then asserts the header reads
  "53 risks" with no "Showing" and the footer reads "Showing 1–50 of 53".
- **Rationale:** the existing `risks-list.spec.ts` is fully quarantined behind the
  slice-082 seed harness (commented assertions, `@playwright/test` import). A
  hermetic spec needs no seed and runs in CI today, exactly as the 666 spec does.
  53 was chosen to match 666 so the two specs read as siblings and the page-1
  range ("1–50 of 53") exercises the multi-page footer branch.
- **Confidence:** high.

## Revisit once in use

- **Third consumer triggers shared extraction.** If /policies (or any third list
  page) needs this header count, lift `risksCountLabel` + `controlsCountLabel`
  into a shared `web/components/list/count-label.ts` taking a `noun` parameter and
  migrate all three pages in one consolidation slice (the D1 forward note). Until
  then, the page-local copies are correct.
- **Plural/singular:** the noun is always "risks" (plural). A register with
  exactly 1 risk reads "1 risks". Most registers carry several risks so the
  singular case is low-priority, but if a deployment routinely shows exactly one
  risk, revisit pluralization (same caveat as slice 666's "1 SCF anchors").
- **Localization:** the "N of M" / "{total} risks" wording is English-only string
  concatenation. When i18n lands, this label needs a message-format entry rather
  than template-literal concatenation (shared with /controls — another reason a
  future shared module would carry its weight).

## Confidence summary

| Decision                                         | Confidence |
| ------------------------------------------------ | ---------- |
| D1 — page-local module, not a shared generic     | high       |
| D2 — header is a count, footer owns the range    | high       |
| D3 — collapse "53 of 53" → "53 risks"            | high       |
| D4 — defensive clamping                          | high       |
| D5 — hermetic e2e mirroring slice 666 (53 risks) | high       |
