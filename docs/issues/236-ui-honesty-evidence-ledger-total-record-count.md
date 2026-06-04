# 236 — UI honesty: /evidence record-count meta lacks ledger-total context

**Cluster:** Quality / UI hygiene
**Estimate:** 0.5d (small wire addition + UI label)
**Type:** AFK
**Status:** `merged` (status reconciled 2026-06-03 — backlog drained per \_STATUS.md SoR; loop terminated batch 184)
**Parent:** #204 (UI parity audit fleet)

## Narrative

Surfaced during the slice 204 per-page audit of `/evidence`
(audit log: `docs/audit-log/204-page-audit-evidence.md`).

The mockup at `Plans/mockups/evidence.html` shows two count
surfaces that the live page does not match:

1. **Page-title subtitle.** Mockup line 111: the H1 is followed by
   `append-only · 14,712 records · 7 connectors` — a constant-
   context summary independent of filters. Live page shows only
   the abstract subtitle `Append-only · ingestion separated from
evaluation · point-in-time replay always possible`.

2. **Filter-row meta count.** Mockup line 181-183: `Showing 12 of
14,712 records` — a window-vs-total ratio. Live shows
   `Showing N records` (no total). On a fresh tenant the live
   meta reads `Showing 0 records`, which is technically true but
   gives the operator no signal of whether the ledger is empty
   tenant-wide OR whether their filters are narrowing it to zero.

The operator confusion mode: a v1 user lands on `/evidence`, sees
`Showing 0 records`, and cannot distinguish "the platform has not
yet received any evidence" (true on first-install) from "my
filters are too narrow" (false — no filters are set yet). The
empty-state UI already addresses this when ZERO is the upstream
truth, but the meta line is the secondary signal that should also
be honest.

This is the slice 178 HONESTY-DATA-BOUND class: a count surface
that gives a misleading impression by omission.

The fix is small: `/v1/evidence` returns a `count` field today
(per `web/lib/api.ts` `EvidenceListResponse` type) but the
upstream response does NOT include a tenant-wide ledger total
(only the result-set count). Two paths:

- **Path A (0.5d, recommended).** Add a `total` field to the
  evidence list response (tenant-scoped). The handler issues a
  parallel `SELECT COUNT(*)` against the RLS-bound view (one
  cheap query; the table is indexed on `tenant_id`). The frontend
  surfaces `Showing N of {total} records` and the page subtitle
  surfaces `append-only · {total} records`. Connectors count
  (`7 connectors` in the mockup) is **deferred** — that's a
  separate read against a connectors table that does not yet
  exist on the API.
- **Path B (0.0d, cheap-cheap).** Drop the mockup's count claim
  entirely from the live page; do nothing. **Rejected** —
  the count is load-bearing for the operator-confusion mode
  described above.

Defaulting AC shape to Path A.

## Threat model

**Verdict.** **no-mitigations-needed.** The `COUNT(*)` query is
issued through the same RLS-bound connection pool; tenant
isolation is preserved. The total is a non-sensitive aggregate
(no per-record data crosses the boundary).

## Acceptance criteria (Path A — chosen)

- **AC-1.** `/v1/evidence` handler at
  `internal/api/controldetail/handler.go` adds a `total` field
  to the response struct (tenant-wide ledger row count, ignores
  filter predicates).
- **AC-2.** The total is computed via a separate `COUNT(*)` query
  against the same RLS-bound pool. The query uses the existing
  `evidence` table (or whatever read model the list-handler reads)
  and does NOT apply any filter predicates.
- **AC-3.** sqlc query is added (named `CountEvidenceForTenant` or
  similar) and wired through. Coverage: a new unit test in
  `internal/api/controldetail/handler_test.go` confirms the count
  is RLS-bound (a tenant-B count from a tenant-A session returns
  zero or differs).
- **AC-4.** `web/lib/api.ts` `EvidenceListResponse` type gains
  `total: number`.
- **AC-5.** `web/app/(authed)/evidence/page.tsx` updates the
  `meta` block (lines 219-226 currently) to render
  `Showing {records.length} of {total} records` when `total > 0`,
  and `No records in ledger yet` when `total === 0`. The page
  subtitle gains a separate constant line (independent of filter
  state): `Append-only · {total} records` when `total > 0`.
- **AC-6.** Slice 204 audit's HONESTY-DATA-BOUND finding F-204-E-4
  is resolved on the next audit run.

## Constitutional invariants honored

- **Invariant 2 (ingestion / evaluation are separated stages).**
  The total reads the evidence ledger directly, not an evaluation
  read model. Honoring the append-only invariant means the count
  is monotonic-non-decreasing tenant-wide.
- **Invariant 6 (tenant isolation at DB layer).** The
  `COUNT(*)` query runs through the same RLS-bound pool.
- **Anti-pattern rejected:** Count surfaces that imply state the
  product cannot verify.

## Canvas references

- `Plans/canvas/04-evidence-engine.md` — append-only ledger
- `Plans/canvas/02-primitives.md` — Evidence primitive
- `Plans/mockups/evidence.html` line 111 + 181-183 — the mockup
  count claims

## Dependencies

- **#204** (UI parity audit fleet) — `in-progress`. Surfacing
  parent.
- **#016** (evidence freshness + drift) — `merged`. Confirms the
  evidence-table read pattern this query reuses.
- **#106** (evidence list query) — `merged`. The handler this
  slice extends.

## Anti-criteria (P0 — block merge)

- **P0-236-1.** Does NOT ship the `7 connectors` count in this
  slice. That's a separate read against a connector inventory
  endpoint that does not yet exist.
- **P0-236-2.** Does NOT apply filter predicates to the `total`
  count — the total is the ledger-wide tenant total, not a
  filtered total. Confusion between the two is the bug class
  this slice exists to close.
- **P0-236-3.** Does NOT cache the count outside the request —
  the ledger is append-only and the count is cheap; staleness
  is the wrong trade.

## Skill mix (3-5)

1. Go HTTP handler + sqlc — adding the count query
2. Next.js — meta-line + subtitle update
3. RLS unit test — confirming tenant isolation on the count
4. Playwright assertion — `Showing N of M` shape
