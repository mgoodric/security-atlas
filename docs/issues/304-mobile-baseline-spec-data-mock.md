# 304 — Mobile-baseline e2e: page.route mock for /controls /risks /evidence rows

**Cluster:** Quality
**Estimate:** 0.5d
**Type:** AFK
**Status:** `ready`

## Narrative

Surfaced during slice 281, captured as follow-up per continuous-batch
policy.

Slice 281 added Playwright assertions to `web/e2e/mobile-baseline.spec.ts`
verifying that at 375px viewport the cards-branch is visible and the
table-branch is hidden on `/controls`, `/risks`, `/evidence`. The
load-bearing assertion `getByTestId('list-cards-wrap')` failed in CI
because the e2e fixture's SCF anchor catalog is empty — the page
renders its empty-state fallback ("No controls in your tenant yet"),
which short-circuits both `<ListTable>` branches via the
`if (rows.length === 0 && emptyFallback) return <>{emptyFallback}</>`
path.

The slice 281 fix-forward relaxed the assertions to verify only that
the table-branch is hidden at mobile (the load-bearing regression
check) — but the cards-branch + per-row assertions were dropped. This
slice restores them by mocking the BFF endpoints so each page renders
at least one row.

## Acceptance criteria

- **AC-1**: At 375px on /controls /risks /evidence with mocked data,
  `list-cards-wrap` is visible and `list-card-row` count ≥ 1.
- **AC-2**: At 1280px on /controls /risks /evidence with mocked data,
  `list-table-wrap` is visible and `list-cards-wrap` is hidden.
- **AC-3**: Mocks live in `web/e2e/mobile-baseline.spec.ts` only — no
  shared seed file changes.

## Dependencies

- **#281** (mobile list-table card-stack collapse) — `merged`.

## Anti-criteria (P0)

- **P0-304-1**: Does NOT modify `<ListTable>` or its `mobileMode`
  semantics.
- **P0-304-2**: Does NOT change page-component code in
  /controls /risks /evidence.
- **P0-304-3**: Does NOT use real-data fixtures — the mocks live
  entirely in the spec file via `page.route()`.

## Skill mix

- Playwright `page.route()` mocking
- TanStack Query queryKey alignment with mocked endpoints
- Wire-shape fidelity (anchorWire / riskWire / evidenceWire)

## Notes for the implementing agent

The three pages fetch via TanStack `useQuery` from BFF routes:

- `/controls` → `fetchControlsList()` → `/api/controls`
- `/risks` → fetch list endpoint (check page source)
- `/evidence` → fetch list endpoint (check page source)

Mock the minimum wire shape needed to satisfy `<ListTable>` rendering
(rows array with the columns the page extracts). Use neutral
deterministic UUIDs (`11111111-1111-1111-1111-111111111111` pattern).
