# 099 — /evidence list view (per slice 093 mockup)

**Cluster:** Frontend
**Estimate:** 1-2d
**Type:** AFK
**Status:** `ready`

## Narrative

Implementation slice for `Plans/mockups/evidence.html`. Today `/evidence` 404s in the sidebar (audit F-4). This slice ships the missing list view.

The mockup data shape derives from `internal/api/evidence/http.go` (`recordWire` + `receiptWire`). The existing dashboard activity feed already reads a similar shape — verify whether the dashboard endpoint covers the list-view needs or whether a `GET /v1/evidence?control_id=&kind=&result=&since=` extension is needed.

## Acceptance criteria

- [ ] AC-1: `web/app/(authed)/evidence/page.tsx` server component renders the tenant's evidence records as a paginated table.
- [ ] AC-2: Endpoint: `GET /v1/evidence?control_id=&kind=&result=&since=`. If this exact shape doesn't exist (dashboard activity feed may be the closest equivalent), surface as a design question; preferred path is to extend the existing endpoint over adding a new one.
- [ ] AC-3: Columns per design doc §7: `observed_at`, `evidence_kind`, `control_id`, `result`, `source_attribution`, `scope`, `hash`. Hash shown as 8-char prefix with copy-on-click.
- [ ] AC-4: Horizontal pill filter row (design doc §8): control + freshness class + scope.
- [ ] AC-5: Empty state per §2: "No records match" + `Clear filters` button + `Set up a connector →` link (true zero-state path on first install).
- [ ] AC-6: Loading skeleton per §3 (3 shimmer rows).
- [ ] AC-7: Row click navigates to a per-record detail page (placeholder — out of scope for this slice; link to `/evidence/[id]` with a thin "page coming soon" stub OR open an inline drawer with the full record JSON pretty-printed).
- [ ] AC-8: Vitest unit tests for filter-state computation + row formatting (hash prefix, scope rendering).
- [ ] AC-9: Playwright spec `web/e2e/evidence-list.spec.ts` covering: list renders, filter narrows, empty state appears.

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
