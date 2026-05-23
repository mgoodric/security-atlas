# 237 — UI honesty: /evidence table missing pagination footer (cursor unwired)

**Cluster:** Quality / UI hygiene
**Estimate:** 1.0d (cursor wiring + footer UI + Playwright assertion)
**Type:** AFK
**Status:** `ready`
**Parent:** #204 (UI parity audit fleet)

## Narrative

Surfaced during the slice 204 per-page audit of `/evidence`
(audit log: `docs/audit-log/204-page-audit-evidence.md`).

The mockup at `Plans/mockups/evidence.html` lines 266-272 shows a
pagination footer on the ledger table:

```
Showing 1–7 of 12 (last 7d) · 14,712 total in ledger
[Previous] [Next]
```

The live page at `https://atlas-edge.home.gmoney.sh/evidence` has
no pagination footer of any kind. Source:
`web/app/(authed)/evidence/page.tsx` lines 476-483 — the
`<ListTable>` is rendered without a `pagination` prop or sibling
footer component.

Backend state: `/v1/evidence` returns
`{control_id, count, evidence, next_cursor}` (visible from the
audit's API probe). The `next_cursor` field is therefore wired
end-to-end through the wire shape, the BFF, and into
`EvidenceListResponse` (per `web/lib/api.ts`) — but the UI does
**not** consume it. Operators ingesting >50 evidence records
(`limit` default) see only the first page and have no UI path
to the rest. The data is reachable via the API; the UI silently
truncates.

This is the slice 178 HONESTY-BROKEN-INTERACTION class:
infrastructure (cursors) is wired but the UI surface that should
expose it is missing.

The fix:

- **Path A (1.0d, recommended).** Ship a minimal cursor-paginated
  footer that respects the existing `next_cursor` contract.
  Previous-page support comes via a client-side cursor stack
  (record each cursor as the user pages forward; Previous pops).
  This pattern is already in use on `/controls` per slice 098 —
  reuse the component.
- **Path B (3.0d, full offset-based).** Add `total`-aware page
  N-of-M math + jump-to-page. Out of scope — slice 236 ships the
  `total` first.

Defaulting AC shape to Path A.

## Threat model

**Verdict.** **no-mitigations-needed.** Cursor pagination uses the
existing RLS-bound query path. No new endpoints. No client-side
session state beyond a short cursor stack in component memory
(not persisted; cleared on navigation away).

## Acceptance criteria (Path A — chosen)

- **AC-1.** A `<ListPagination>` (or equivalent reused from
  `/controls` slice 098) footer is mounted below the `<ListTable>`
  in `web/app/(authed)/evidence/page.tsx`. Visible when
  `records.length > 0`.
- **AC-2.** The footer renders `Showing N records` (or
  `Showing N of M records` once slice 236 lands — gated on
  `total` existence).
- **AC-3.** The footer renders `Previous` and `Next` buttons. The
  Next button is `disabled` when `next_cursor` is empty. The
  Previous button is `disabled` when the client-side cursor stack
  is empty.
- **AC-4.** Clicking Next: pushes the current cursor onto the
  stack, re-issues the query with `cursor={next_cursor}`. The
  React Query key includes the cursor so the cache treats each
  page as a distinct entry.
- **AC-5.** Clicking Previous: pops the most-recent cursor off
  the stack, re-issues the query with that cursor. Reaching the
  empty stack returns to the unparameterized first page.
- **AC-6.** Navigating to `/evidence` from a different page
  resets the cursor stack to empty.
- **AC-7.** Playwright spec at `web/e2e/evidence-pagination.spec.ts`
  asserts Next → Previous → Next round-trips correctly on the
  dev-seed dataset (which must have ≥3 pages of evidence — if it
  does not, this slice also adds the necessary seed records to
  `deploy/docker/seed.sh` per slice 205's seed pattern).
- **AC-8.** Slice 204 audit's HONESTY-BROKEN-INTERACTION finding
  F-204-E-5 is resolved on the next audit run.

## Constitutional invariants honored

- **Invariant 2 (ingestion / evaluation separated; point-in-time
  replay always possible).** Cursor pagination must be stable
  across reads of the same `observed_at` window — the cursor
  encodes the last-seen `(observed_at, evidence_id)` tuple,
  which is monotonic in the append-only ledger.
- **Invariant 6 (tenant isolation).** Cursor queries are RLS-
  bound; cross-tenant cursors must fail closed (return zero
  records).
- **Anti-pattern rejected:** Wired backend cursors with no UI
  surface (silently truncated pagination).

## Canvas references

- `Plans/canvas/04-evidence-engine.md` — append-only ledger
- `Plans/canvas/02-primitives.md` — Evidence primitive
- `Plans/mockups/evidence.html` lines 266-272 — the mockup
  footer
- Slice 098's `<ListPagination>` component pattern

## Dependencies

- **#204** (UI parity audit fleet) — `in-progress`. Surfacing
  parent.
- **#106** (evidence list query) — `merged`. Already returns
  `next_cursor`.
- **#098** (controls list pagination) — `merged`. Source of the
  reusable footer component.
- **#205** (demo seed data) — `merged` (codecov-only). The seed
  may need top-up to ensure ≥3 pages of evidence; defer that
  decision to the implementing engineer.
- **#236** (ledger-total record count) — `ready`. Not a strict
  prerequisite; if 236 ships first, the footer renders
  `Showing N of M` instead of `Showing N`.

## Anti-criteria (P0 — block merge)

- **P0-237-1.** Does NOT introduce offset-based pagination. The
  existing wire is cursor-based; respecting it is part of the
  invariant-2 commitment to replay stability.
- **P0-237-2.** Does NOT persist the cursor stack across
  navigation (no localStorage, no URL state for the stack —
  cursors are session-scoped). The CURRENT cursor MAY appear in
  the URL for shareable deep-links; the stack is not shared.
- **P0-237-3.** Does NOT modify the `/v1/evidence` handler's
  cursor semantics.

## Skill mix (3-5)

1. Next.js App Router — wiring the footer + URL state
2. TanStack Query — keying queries by cursor
3. Component reuse — `<ListPagination>` from `/controls`
4. Playwright spec — multi-page navigation assertions
