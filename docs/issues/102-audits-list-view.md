# 102 — /audits list view (per slice 093 mockup)

**Cluster:** Frontend
**Estimate:** 1d
**Type:** AFK
**Status:** `merged` (status reconciled 2026-06-03 — backlog drained per \_STATUS.md SoR; loop terminated batch 184)

## Narrative

Implementation slice for `Plans/mockups/audits.html`. Today `/audits` (plural) 404s in the sidebar (audit F-4).

Important disambiguation per design doc §6: `/audits` (plural) is the new period index — list of `audit_periods`. `/audit/[controlId]` (singular, shipped in slice 042) is the per-control auditor walk-through and STAYS untouched. Different routes, different files, different goals.

## Acceptance criteria

- [ ] AC-1: `web/app/(authed)/audits/page.tsx` server component renders `GET /v1/audit-periods` as a table.
- [ ] AC-2: Columns per design doc §7: `name`, `framework_version_id` (joined to a human label), `period_start..period_end`, `status` (planned/in-progress/frozen/closed), `frozen_at` + `frozen_by` (when present), `created_by`.
- [ ] AC-3: Horizontal pill filter row: framework + status + year.
- [ ] AC-4: Empty state per §2: "No audit periods yet" + `Create audit period` primary CTA.
- [ ] AC-5: Loading skeleton per §3.
- [ ] AC-6: Frozen periods render with a small lock icon next to `status` + tooltip showing `frozen_at` / `frozen_by`. Visual urgency for in-progress periods within 30 days of `period_end`.
- [ ] AC-7: Row click navigates to a per-period detail page (placeholder OR drawer).
- [ ] AC-8: Vitest unit tests for status-pill color, frozen-icon visibility, days-until-end formatting.
- [ ] AC-9: Playwright spec `web/e2e/audits-list.spec.ts`.

## Constitutional invariants honored

- **Invariant 6:** tenant isolation via BFF.
- **Audit-period freezing (canvas §8.4):** the slice respects the freezing primitive — frozen periods are read-only on the list AND visually distinct.
- **AI-assist boundary:** pure render.

## Canvas references

- `Plans/mockups/audits.html`
- `Plans/canvas/12-ui-fill-in-design-decisions.md` §2, §3, §6, §7, §8
- `Plans/canvas/08-audit-workflow.md` §8.4 (freezing semantics)
- `internal/api/auditperiods/handlers.go` (`periodWire`)
- Slice 042 audit workspace (`/audit/[controlId]` — the disjoint sibling)
- Slice 028 audit-period freezing
- Slice 098 (shared list-shell)

## Dependencies

- **093** — merged
- **098** — RECOMMENDED to land first (shared list-shell)
- **020** + **021** (audit periods + audit notes) — merged
- **028** (audit-period freezing) — merged

## Anti-criteria (P0)

- **P0-A1:** Does NOT collide with `/audit/[controlId]` (singular — slice 042). Different file, different route segment.
- **P0-A2:** Does NOT allow editing frozen periods from the list — that requires going through the period-detail page's "unfreeze" workflow (out of scope for this slice).
- **P0-A3:** Does NOT bundle period-create UI — `Create audit period` CTA links to existing admin flow OR placeholder.
- **P0-A4:** Does NOT invent columns; `periodWire` is authoritative.
- **P0-A5:** Does NOT use vendor-prefixed tokens.

## Skill mix

- Next.js + TanStack Query list-view (shell from slice 098)
- Audit-period lifecycle awareness (status pills, frozen indicators)
- Cross-route disambiguation (`/audits` plural vs `/audit/[id]` singular)

## Notes

- The "in-progress periods within 30 days of period_end" cue is a UX touch — gives the security leader an early signal to start fieldwork. Render as a soft amber dot, not a red alarm.
- Verify `framework_version_id` has a human label endpoint (`/v1/framework-versions/[id]`) — if not, derive from the existing framework metadata.
