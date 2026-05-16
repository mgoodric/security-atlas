# 100 — /risks list view (per slice 093 mockup)

**Cluster:** Frontend
**Estimate:** 1d
**Type:** AFK
**Status:** `ready`

## Narrative

Implementation slice for `Plans/mockups/risks.html`. Today `/risks` 404s (audit F-4). This slice ships the flat list view AND addresses audit F-3 by removing `/risks/hierarchy` from the top-nav (replaced by a page-header `Hierarchy view →` link on `/risks` per design doc §5).

The flat list is the canonical default; `/risks/hierarchy` remains as the specialized org-tree view (slice 056), reached via the page-header link instead of the sidebar.

## Acceptance criteria

- [ ] AC-1: `web/app/(authed)/risks/page.tsx` server component renders `GET /v1/risks` as a table.
- [ ] AC-2: Columns per design doc §7: `id`, `title`, `category`, `treatment`, `treatment_owner`, `residual_score`, `severity`, `review_due_at`.
- [ ] AC-3: Horizontal pill filter row (design doc §8): treatment + severity band + owner.
- [ ] AC-4: Empty state per §2: "No risks logged yet" + `Add first risk` primary CTA (true zero-state — most installs start with zero risks).
- [ ] AC-5: Loading skeleton per §3 (3 shimmer rows).
- [ ] AC-6: Page-header link `Hierarchy view →` navigates to `/risks/hierarchy`.
- [ ] AC-7: Row click navigates to a per-risk detail page (placeholder OR drawer per slice 099 pattern).
- [ ] AC-8: Update `web/components/shell/sidebar.tsx` — REMOVE the `/risks/hierarchy` top-level entry. Add a corresponding `List view →` link to `/risks/hierarchy`'s page header so the hierarchy view is still reachable (closes audit F-3).
- [ ] AC-9: Vitest unit tests for filter computation + residual-score formatting.
- [ ] AC-10: Playwright spec `web/e2e/risks-list.spec.ts`: list renders, filter narrows, hierarchy link navigates.

## Constitutional invariants honored

- **Invariant 6:** tenant isolation via BFF.
- **AI-assist boundary:** pure render.

## Canvas references

- `Plans/mockups/risks.html`
- `Plans/canvas/12-ui-fill-in-design-decisions.md` §2, §3, §5, §7, §8
- `Plans/canvas/13-ui-mockup-audit-2026-05-16.md` F-3 (this slice closes the deferred fix)
- `internal/api/risks/handlers.go` (`riskWire`)
- Slice 056 risk hierarchy implementation
- Slice 098 controls list (shared list-shell)

## Dependencies

- **093** — merged
- **098** — RECOMMENDED to land first (shared list-shell extraction). NOT a hard blocker; this slice can ship without it but the row-skeleton duplication will be visible.
- **019** (risk register CRUD) — merged
- **056** (risk hierarchy) — merged

## Anti-criteria (P0)

- **P0-A1:** Does NOT modify the `/risks/hierarchy` page content beyond adding the `List view →` page-header link.
- **P0-A2:** Does NOT add risk-create / risk-edit endpoints — read-only list (the `Add first risk` CTA links to the existing CRUD flow under `/admin/...` or whichever route owns risk-creation today).
- **P0-A3:** Does NOT invent columns; `riskWire` is authoritative.
- **P0-A4:** Does NOT use vendor-prefixed tokens.

## Skill mix

- Next.js + TanStack Query list-view (shell from slice 098)
- Wire binding from `internal/api/risks/handlers.go`
- Cross-page nav: `/risks` ↔ `/risks/hierarchy` reciprocal page-header links

## Notes

- The `Add first risk` CTA needs to know WHERE risk creation lives in v1 — check `/admin/risks/new` or whichever route exists. If no creation UI exists, link to a placeholder + file a spillover for a risk-create flow.
- AC-8 (sidebar update) is the F-3 audit closure. Without it, this slice is incomplete from the audit perspective.
