# 177 — Exceptions list-page UI surface

**Cluster:** Frontend
**Estimate:** 0.5d
**Type:** AFK
**Status:** `not-ready`

## Narrative

Surfaced 2026-05-19 as a spillover from slice 138. The slice 138
exceptions data-export backend handler + BFF ship, but no dedicated
list-page UI surface exists at v1 (`web/app/(authed)/` has no
`/exceptions` route). This slice adds the missing `/exceptions`
list page following the slice 098 (`/controls`) and slice 099
(`/evidence`) patterns: tenant-wide list with filter pills, the slice
138 Export buttons (CSV / JSON / XLSX) wired in the toolbar, and
data fetched via the existing `/v1/exceptions` API surface.

**What this slice ships:**

- `web/app/(authed)/exceptions/page.tsx` — tenant-wide list view of
  exception rows
- Filter pills: `status` (requested / approved / denied / active /
  expired), `control_id` (when arriving from a control page)
- Columns: id, control_id (link), status, requested_by, requested_at,
  expires_at, duration_days, justification (truncated)
- Toolbar Export buttons (CSV / JSON / XLSX) calling the existing
  slice 138 BFF at `/api/admin/exceptions/export`
- Playwright e2e spec covering the Export-button click → file-download
  shape

## Threat model

Inherits slice 138 P0-A-Ledger-3 (justification is sensitive but
in-scope via RLS). No new threat surface — the read endpoint
(`/v1/exceptions`) already exists.

## Acceptance criteria

- [ ] AC-1: `/exceptions` route renders the tenant-wide register
- [ ] AC-2: filter pills work URL-shareably (mirror slice 098 pattern)
- [ ] AC-3: Export CSV / JSON / XLSX buttons appear in the toolbar with
      stable `data-testid` tokens
- [ ] AC-4: clicking Export triggers the browser file-save dialog
      (Playwright e2e verifies)
- [ ] AC-5: empty-state matches slice 099's pattern (filter-clear CTA
  - connector-config CTA)
- [ ] AC-6: CHANGELOG entry

## Constitutional invariants honored

Inherits slice 138. No new architectural invariants.

## Dependencies

- **#138** Ledger entities export (backend handler + BFF). **Gate: 138
  merged.**

## Anti-criteria (P0 — block merge)

- Inherits slice 138 P0-A-Ledger-1 through P0-A-Ledger-3.
- **P0-A-176-1:** No editing of exceptions from the list view (the
  exception lifecycle stays on the slice 022 request/approve/deny
  workflow at the control detail page).
- **P0-A-176-2:** No invented columns — every column derives from the
  `/v1/exceptions` wire shape.

## Skill mix

- Next.js App Router + shadcn/ui (mirrors slice 098 / 099 / 102).
- Playwright e2e (mirrors slice 102 patterns).

## Notes for the implementing agent

The slice 138 backend Export buttons render fine even if you call them
directly via URL today. This slice surfaces them in the canonical UI
location. The slice 022 exception lifecycle workflow (request / approve
/ deny / activate / expire) lives at the control detail page; nothing
about THIS slice touches that workflow.

Provenance: filed 2026-05-19 as a spillover from slice 138 D6.
