# 239 — Policies list: header missing inline "N published · M draft · K retired" counts

**Cluster:** policies (UI parity)
**Estimate:** 0.25d
**Type:** AFK
**Status:** `merged` (status reconciled 2026-06-03 — backlog drained per \_STATUS.md SoR; loop terminated batch 184)
**Parent:** #204 (UI parity audit fleet — `/policies` page)

## Narrative

Surfaced by the slice 204 audit of `/policies` against
`Plans/mockups/policies.html` (see
`docs/audit-log/204-page-audit-policies.md`).

The mockup at `Plans/mockups/policies.html` (lines 107–114) renders
a header bar with an inline status-count summary next to the page
title:

```html
<h1>Policy library</h1>
<span class="text-sm text-slate-500">14 published · 2 draft · 1 retired</span>
```

The production page at `web/app/(authed)/policies/page.tsx` (lines
337–339, 420–423) renders only the title + a single subtitle
("Versioned policies · acknowledgment tracked against the current
version") — the inline status-count summary is missing.

The counts are derivable client-side from the rows already on the
wire (`policiesQ.data?.policies`), grouped by `status`. No new
endpoint or wire change is needed.

## Threat model

**Verdict.** **no-mitigations-needed.** Purely presentational
derivation from rows already authorized + returned by the list
endpoint.

## Acceptance criteria

- **AC-1.** The `<ListPage>` title row renders an inline count
  summary next to the title (or in a clearly-paired position), with
  the shape `<N> published · <M> draft · <K> retired` matching the
  mockup. The summary uses `rows.length`-derived counts (not the
  filtered `visible.length`), so the summary is a stable
  aggregate, not a filter-sensitive figure.
- **AC-2.** Status bins shown are: `published`, `draft`,
  `retired`. Other statuses (`under_review`, `approved`) are
  **not** dropped; they roll into a fourth `· <X> other` segment
  if any rows carry those statuses, OR they're enumerated
  explicitly if `>= 1` row exists. (Decisions log entry: chose
  the explicit-when-present variant to avoid hiding signal.)
- **AC-3.** The summary is omitted when `rows.length === 0` (the
  empty-state CTA does the talking instead).
- **AC-4.** Empty rows return `0 published · 0 draft · 0 retired`
  is NOT rendered (covered by AC-3 hide).
- **AC-5.** Unit test under `web/app/(authed)/policies/` (filename
  `header-counts.test.ts` or extended into an existing test file)
  covers: (a) all-published rows, (b) mixed statuses, (c) zero
  rows, (d) `under_review` row promotes the fourth segment.
- **AC-6.** Pre-commit clean, DCO sign-off, Co-Authored-By trailer.

## Constitutional invariants honored

- **Invariant 1 (one control, N framework satisfactions).** Not
  affected; policy primitive is orthogonal.
- **AI-assist boundary.** No AI-generated content touched.
- **Anti-pattern rejected.** "Vanity trust centers" — the inline
  counts ARE useful operator signal at-a-glance, and the mockup
  carries them; restoring them aligns the live surface to the
  design.

## Canvas references

- `Plans/canvas/04-evidence-engine.md` §4.5 — policy lifecycle states
- `Plans/canvas/07-metrics.md` — operator-at-a-glance signal density
- `Plans/mockups/policies.html` lines 107–114 — header shape

## Dependencies

- **#204** (audit parent) — `in-progress`.
- **#022** (policy primitive + list endpoint) — merged.

## Anti-criteria (P0 — block merge)

- **P0-239-1.** Does NOT introduce a separate `/v1/policies/counts`
  endpoint. The counts derive client-side from the list response.
- **P0-239-2.** Does NOT make the count summary filter-sensitive
  (it's the unfiltered aggregate per AC-1 — the "Showing X of Y"
  meta covers the filtered figure already).
- **P0-239-3.** Does NOT bundle multiple findings. Status-count
  header only. Filter pills are slice 238; pagination footer is
  slice 240.
- **P0-239-4.** Does NOT use vendor-prefixed test fixture tokens.

## Skill mix

1. Next.js App Router — purely presentational addition to the
   `ListPage` subtitle/title region.
2. Vitest unit testing — covering the count-deriver.
3. TypeScript reduce over `Policy[]` by status.
