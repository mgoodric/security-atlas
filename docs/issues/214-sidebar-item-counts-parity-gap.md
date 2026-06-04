# 214 — Sidebar item counts parity gap (Controls "82", Risks "3" badges)

**Cluster:** frontend
**Estimate:** 0.5d
**Type:** AFK
**Status:** `merged` (status reconciled 2026-06-03 — backlog drained per \_STATUS.md SoR; loop terminated batch 184)

## Narrative

Surfaced during slice 204 audit fleet (audits page), captured as
follow-up per continuous-batch policy.

The mockup at `Plans/mockups/audits.html` shows sidebar entries with
inline count badges:

- `Controls` row carries a right-aligned mono `82` badge
  (`Plans/mockups/audits.html` line 66)
- `Risks` row carries a right-aligned mono `3` badge in rose
  (`Plans/mockups/audits.html` line 75) — the rose color signals the
  open critical-severity risk count

The live `/audits` page sidebar (shared across every authed page)
renders neither. The sidebar links are bare text:
`Controls / Evidence / Risks / Audits / ...` with no count metadata.

The counts are not vanity — they are the affordance the operator
uses to spot a sudden risk-register spike or a deletion of in-scope
controls without opening each page. The rose color on Risks is
specifically the "you have N open critical risks" signal — a board-
report-relevant metric.

Data sources both exist:

- `GET /v1/controls?count_only=true` (or equivalent count from the
  existing controls list endpoint)
- `GET /v1/risks?status=open&severity=critical` count from the risks
  endpoint that backs the risk-register page

## Threat model

**Verdict.** **no-mitigations-needed.** Counts read tenant-scoped
data already gated by RLS; the data shape (a single integer) cannot
leak record-level information.

## Acceptance criteria

- **AC-1.** Sidebar's `Controls` link renders an aggregate count
  badge (right-aligned, mono, muted) matching the in-scope control
  count for the current tenant.
- **AC-2.** Sidebar's `Risks` link renders the OPEN CRITICAL risk
  count in rose. If zero, the badge is hidden (silence over a `0`
  badge).
- **AC-3.** Counts refresh every 60s via TanStack Query
  (`staleTime: 60_000`, `refetchInterval: 60_000`). The badge
  shows a subtle pulse during refetch.
- **AC-4.** Vitest module test for the sidebar component asserts:
  - Controls badge renders when count > 0
  - Risks badge hidden when critical-open count is 0
  - Risks badge in rose when critical-open count > 0
- **AC-5.** Playwright e2e spec confirms the badges appear on
  `/audits` (proxy for "shared shell shows them").

## Constitutional invariants honored

- **Invariant 6 (tenant isolation).** Counts read RLS-tenant-scoped
  endpoints; cross-tenant leakage impossible by construction.
- **Invariant 9 (manual evidence is first-class).** Controls count
  includes manual controls; same lifecycle.

## Canvas references

- `Plans/mockups/audits.html` lines 63-76 — the sidebar badge pattern
- `Plans/canvas/07-metrics.md` — the operator-attention surface
  rationale (critical-risk visibility)

## Dependencies

- **#204** — UI parity audit (surfacing parent)
- **#102** — audits page (one of the consumer surfaces)
- Existing controls + risks list endpoints (already merged)

## Anti-criteria (P0 — block merge)

- **P0-214-1.** Does NOT add a new endpoint. Counts derive from
  existing list endpoints' `count` envelope field.
- **P0-214-2.** Does NOT render a `0` badge. Zero state = badge
  hidden.
- **P0-214-3.** Does NOT refetch more often than 60s. The badge is
  a low-priority surface; tighter refresh is unnecessary
  cardinality on the backend.
- **P0-214-4.** Does NOT block the sidebar render on the count
  fetch. Sidebar renders immediately; badges fade in on resolve.

## Skill mix (3-5)

1. Next.js App Router shared layout
2. TanStack Query
3. Tailwind badge styling (mono + rose variant)
4. Vitest module test
5. Playwright e2e assertion
