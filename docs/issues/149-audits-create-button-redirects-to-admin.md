# 149 — Audits page "Create audit period" button redirects to /admin instead of slice 042 workspace

**Cluster:** Frontend
**Estimate:** 0.5d
**Type:** AFK
**Status:** `ready`

## Narrative

Surfaced 2026-05-18 from operator report on v1.10.0:

> "On the audits page, if I click 'Create audit period' I get redirected to /admin"

Slice 042 (Audit workspace view — `merged` 2026-05-13, PR gh#80) shipped the audit workspace UI, but the audits LIST page (`web/app/(authed)/audits/page.tsx`) wires its "Create audit period" button to `/admin` (placeholder) instead of to slice 042's workspace flow.

This is a frontend-only wiring fix. Slice 042's create flow exists; the audits list page needs to route to it.

## Acceptance criteria

- [ ] AC-1: Audit `web/app/(authed)/audits/page.tsx`; identify the create-button onclick handler.
- [ ] AC-2: Confirm slice 042 audit workspace exposes a create route (likely `/audits/new` or modal).
- [ ] AC-3: Re-wire create button to slice 042's actual create flow.
- [ ] AC-4: Playwright e2e: click create → land on the audit workspace create page (NOT `/admin`).
- [ ] AC-5: Decisions log notes whether the audit workspace create flow is a route or a modal + why.
- [ ] AC-6: CHANGELOG entry: "Audits page 'Create audit period' wires to slice 042 workspace (#149)".

## Dependencies

- **#042** Audit workspace view (merged).
- Audits list page (likely slice 060-ish or earlier).

## Anti-criteria (P0 — block merge)

- **P0-AUD-1** Button MUST route to working create flow; NOT to `/admin`.
- **P0-AUD-2** NO scope creep into redesigning the audit workspace.

## Notes for the implementing agent

Smallest slice in this batch. Fix is likely one line in the audits page onclick handler.

Provenance: filed 2026-05-18 from operator v1.10.0 report.
