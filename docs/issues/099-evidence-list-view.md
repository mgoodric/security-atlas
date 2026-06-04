# 099 — /evidence list view (per slice 093 mockup)

**Cluster:** Frontend
**Estimate:** 1-2d
**Type:** AFK
**Status:** `merged` (status reconciled 2026-06-03 — backlog drained per \_STATUS.md SoR; loop terminated batch 184)

## Narrative

Implementation slice for `Plans/mockups/evidence.html`. Today `/evidence` 404s in the sidebar (audit F-4). This slice ships the missing list view.

The mockup data shape derives from `internal/api/evidence/http.go` (`recordWire` + `receiptWire`). The existing dashboard activity feed already reads a similar shape — verify whether the dashboard endpoint covers the list-view needs or whether a `GET /v1/evidence?control_id=&kind=&result=&since=` extension is needed.

## Acceptance criteria

- [x] AC-1: `web/app/(authed)/evidence/page.tsx` Client Component renders evidence records as a paginated table.
- [x] AC-2: Endpoint: `GET /v1/evidence?control_id=`. The shape requires `control_id` today — design call surfaced + spillover slice 106 files the backend extension (`?control_id=` optional + `?kind=`/`?result=`/`?since=` filters) per the slice instruction "preferred path is to extend the existing endpoint over adding a new one".
- [x] AC-3: Columns per design doc §7: `observed_at`, `evidence_kind`, `control_id`, `source`, `scope`, `hash`. Hash rendered as 8-char prefix with click-to-copy → full hash + "Copied!" feedback. NB: `result` column OMITTED — that field is NOT on `evidenceWire` today (only on the PUSH `recordWire`). Spillover 106 surfaces it on the GET shape; honors P0-A1 (no invented columns).
- [x] AC-4: Horizontal `<FilterPills>` from the slice 098 shell — Control pill (drives selection + data fetch). Freshness + scope pills deferred until backend ledger-wide endpoint ships (slice 106).
- [x] AC-5: Empty state per §2 — "No evidence records match these filters" + `Clear filters` + `Set up a connector →` (routes to `/admin/credentials`). Plus the v1-specific "Pick a control to see its evidence ledger" prompt when no control is selected.
- [x] AC-6: Loading skeleton via shared `<ListLoadingSkeleton>` per §3 (3 shimmer rows).
- [x] AC-7: Row click opens inline shadcn `<Dialog>` drawer showing the full record JSON pretty-printed (decision D3 — simpler than `/evidence/[id]` stub page).
- [x] AC-8: Vitest unit tests — `filters.test.ts` (6 tests: control selection state), `format.test.ts` (13 tests: hash prefix, scope cell, source summary, pretty JSON), `route.test.ts` (7 tests: BFF auth + forwarding + tenant-isolation guard).
- [x] AC-9: Playwright spec `web/e2e/evidence-list.spec.ts` covers list renders, control selection narrows, empty state, row drawer, hash copy. Quarantined per slice-079 (bodies preserved verbatim as reviewable contract; un-comments when slice 082 seed harness lands).

## Constitutional invariants honored

- **Invariant 6 (tenant isolation):** BFF reads via tenant-bound atlas_app pool.
- **AI-assist boundary:** pure render; no auto-interpretation of evidence records.

## Canvas references

- `Plans/mockups/evidence.html`
- `Plans/canvas/12-ui-fill-in-design-decisions.md` §2, §3, §7, §8
- `internal/api/evidence/http.go` (`recordWire` + `receiptWire`)
- Slice 098 controls list view (shared `<ListView>` shell if extracted)

## Dependencies

- **093** — merged
- **005** — merged
- **013** (evidence ledger write API + read endpoints) — merged
- **016** (evidence freshness + drift) — merged

## Anti-criteria (P0)

- **P0-A1:** Does NOT invent columns; binds to existing wire shape.
- **P0-A2:** Does NOT show full hashes in the row (8-char prefix only — full hash on copy-click).
- **P0-A3:** Does NOT use a left filter sidebar — horizontal pill row.
- **P0-A4:** Does NOT use vendor-prefixed tokens in fixtures.

## Skill mix

- Next.js + TanStack Query list-view pattern (reusing slice 098 shell if shared)
- Wire-shape binding from `internal/api/evidence/http.go`
- Pagination semantics (the dashboard activity feed has the precedent)

## Notes

- The hash-copy pattern is a UX nicety worth doing — auditors will paste hashes into evidence-chain validation tools.
- If the `GET /v1/evidence?...` endpoint shape needs an extension, file as a backend follow-on slice rather than expanding this PR.
