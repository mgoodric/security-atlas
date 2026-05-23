# 240 — Policies list: missing pagination footer + "365-day acknowledgment window" disclosure

**Cluster:** policies (UI parity)
**Estimate:** 0.5d
**Type:** AFK
**Status:** `ready`
**Parent:** #204 (UI parity audit fleet — `/policies` page)

## Narrative

Surfaced by the slice 204 audit of `/policies` against
`Plans/mockups/policies.html` (see
`docs/audit-log/204-page-audit-policies.md`).

The mockup at `Plans/mockups/policies.html` (lines 278–284) renders
a footer bar BELOW the table containing two distinct affordances:

1. **A pagination control** — `[Previous] [Next]` with a textual
   `Showing 1–7 of 17` window.
2. **A regulatory disclosure** — `· 365-day acknowledgment window`
   in the same footer row.

The production `/policies` page at
`web/app/(authed)/policies/page.tsx` renders the `<ListTable>` shell
without a footer — neither the pagination control nor the disclosure
appears on the live page.

The two affordances should be evaluated together because they
co-occupy a single footer row in the mockup; splitting them into
two slices would force two PRs to touch the same `<ListPage>`
footer slot.

**Pagination.** The `/v1/policies` endpoint returns the full row
set (no `limit` / `offset` / `cursor`) — see the wire shape
(`PoliciesListResponse` in `web/lib/api.ts`). For v1 (low policy
counts; the mockup itself shows "17" total), this is acceptable —
**but the mockup-claim of pagination is honesty-class drift**:
either the live UI should show pagination (and the wire should
support it), or the mockup should be updated to drop the
affordance.

**365-day acknowledgment window.** This is the SOC 2 CC1.4
acknowledgment-cadence convention: a policy acknowledgment older
than 365 days is treated as stale. The mockup discloses this
publicly. The live UI does not — the operator has no in-product
indication of what counts as a fresh attestation. This is an
operator-friction finding, not a security finding.

## Threat model

**Verdict.** **no-mitigations-needed.** Both affordances are
purely presentational over data already on the wire. Pagination,
if added, operates on the existing row response (client-side
paging) until a server-side pagination wire follow-on lands.

## Acceptance criteria

- **AC-1.** The `<ListPage>` table footer renders a single bar
  containing: `<left-text>` + `<right-pagination-buttons>`.
- **AC-2.** Left text reads `Showing <start>–<end> of <total> ·
365-day acknowledgment window` when the table has at least one
  row. When the table is empty, the footer is omitted (the empty-
  state CTA does the talking instead).
- **AC-3.** Pagination is **client-side** for v1: 25 rows per page
  default (decision: rationalized to match other list-view pages
  — see `web/components/list/list-table.tsx` for any existing
  convention; if none, 25 is the decisions-log entry). `[Previous]`
  disabled on page 1; `[Next]` disabled on the last page.
- **AC-4.** Page state participates in the URL-driven query-string
  pattern (`?page=2`), so the URL is bookmarkable.
- **AC-5.** The `365-day acknowledgment window` substring is
  exposed as a constant, NOT a literal in JSX — a future slice
  can swap the window length if the audit-policy of record
  changes.
- **AC-6.** Unit test in `web/app/(authed)/policies/` covers:
  (a) page-1-of-1 case (Previous + Next both disabled),
  (b) middle-page case,
  (c) last-page case,
  (d) empty-rows case (footer omitted).
- **AC-7.** Decisions log entry at
  `docs/audit-log/240-policies-pagination-footer-decisions.md`:
  (D1) page size choice (25 vs other), (D2) client-vs-server
  pagination boundary + when to graduate, (D3) acknowledgment
  window constant location + change-management path.
- **AC-8.** Pre-commit clean, DCO sign-off, Co-Authored-By trailer.

## Constitutional invariants honored

- **Invariant 1 (one control, N framework satisfactions).** Not
  affected.
- **AI-assist boundary.** No AI-generated content touched.
- **Anti-pattern rejected.** "Continuous monitoring that's actually
  24-hour polling" — the 365-day acknowledgment window is the
  honest signal of acknowledgment freshness; disclosing it in the
  UI is honesty-positive.

## Canvas references

- `Plans/canvas/04-evidence-engine.md` §4.5 — policy acknowledgment
  cadence
- `Plans/canvas/07-metrics.md` — KPI surfaces
- `Plans/mockups/policies.html` lines 278–284 — footer shape

## Dependencies

- **#204** (audit parent) — `in-progress`.
- **#107** (policy ack-rate join) — merged.

## Anti-criteria (P0 — block merge)

- **P0-240-1.** Does NOT add a server-side pagination wire
  (`?limit=` / `?cursor=`) — that's a follow-on slice. Client-side
  paging over the existing full-row response is the v1 fit.
- **P0-240-2.** Does NOT hard-code `365` as a literal in JSX — use
  a constant (per AC-5) so the window-length change can ship as a
  one-line PR if the policy of record updates.
- **P0-240-3.** Does NOT bundle other findings. Pagination + window
  disclosure only (they share a footer slot, hence one slice).
- **P0-240-4.** Does NOT use vendor-prefixed test fixture tokens.

## Skill mix

1. Next.js App Router + `useSearchParams` — URL-driven page state.
2. shadcn/ui pagination component (or the list-shell convention
   if one exists) — consistent with sibling list views.
3. Vitest unit testing — page-window math.
4. TypeScript — page slicer with off-by-one safety.
