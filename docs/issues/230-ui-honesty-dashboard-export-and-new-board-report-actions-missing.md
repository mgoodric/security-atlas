# 230 — UI honesty: dashboard "Export" and "New board report" header actions missing

**Cluster:** Quality / UI parity (frontend)
**Estimate:** 0.5d
**Type:** AFK
**Status:** `not-ready` (one of the two actions has no backing endpoint yet — see Dependencies)

## Narrative

Surfaced during the slice 204 per-page UI parity audit fleet (page slug: `dashboard`; mockup file: `Plans/mockups/dashboard.html`). Category (i) layout / chrome parity.

The dashboard mockup's header right-side action cluster (`Plans/mockups/dashboard.html` lines 125–131) renders two buttons:

1. **"Export"** — secondary button. Mockup implies a one-click dashboard-state export (CSV / PDF / OSCAL bundle of current posture + risks + freshness + drift snapshots).
2. **"New board report"** — primary CTA button with leading arrow icon. Mockup implies a click navigates to the board-pack composer (`/board-packs/new`) with the dashboard's current snapshot pre-attached.

The live `/dashboard` header (`web/app/(authed)/dashboard/page.tsx` lines 94–103) renders no action buttons. The H1 region is text-only. The two CTAs in the mockup are unrepresented.

**Why this is a finding.** The two CTAs are the dashboard's primary "act on what you see" affordances. Their absence pushes operators to sidebar-navigate to Board Packs and click through to create a new report from scratch — losing the dashboard's snapshot context. For the solo-security-leader persona on a board-report deadline, that's daily friction.

**Why `not-ready`.** "New board report" can ship today: slice 053 (board-pack composer) + slice 052 (board-pack list) are merged; `/board-packs/new` is a real route. "Export" has no backing endpoint — there's no dashboard-snapshot-export API. The implementing slice MAY split this into two slices (board-report CTA = ship-able today; export = depends on a new endpoint) — that decision lives in the implementing slice's decisions log.

## Threat model

**S — Spoofing.** Both actions reuse the existing session bearer.

**T — Tampering.** "New board report" mutates state (creates a board-pack draft). It MUST respect the existing board-pack write authz (slice 053 owns the gate). The Playwright e2e-audit harness's read-only guardrail correctly classifies it as a mutating action — the implementing slice's e2e spec uses the functional `web/e2e/` harness, not the audit harness.

**I — Info disclosure.** The dashboard snapshot may include freshness numbers, top risks, drift events — all RLS-scoped to the active tenant. Export must NOT cross-tenant-leak. Implementing slice's authz test owns this.

**Verdict.** **needs-mitigations.** Standard RLS + authz tests on the backing endpoints. None block this audit-spillover.

## Acceptance criteria

- **AC-1.** Header right-side action cluster renders two buttons matching the mockup's order (Export · secondary, New board report · primary).
- **AC-2.** "New board report" navigates to `/board-packs/new` with a `?from=dashboard-snapshot&snapshot_at={iso}` query parameter that the board-pack composer reads to pre-fill the snapshot section.
- **AC-3.** "Export" opens a small menu (CSV / PDF / OSCAL bundle) — OR the implementing slice splits the export action into its own follow-on slice and renders only the board-report CTA at v1.
- **AC-4.** Both actions are role-gated: a user without `board:write` does not see "New board report"; a user without `evidence:export` does not see "Export".
- **AC-5.** Disabled-state honesty: if the dashboard is in a degraded state (e.g. all panel queries errored), the buttons render disabled with a tooltip explaining why ("Snapshot incomplete — refresh panels first").

## Constitutional invariants honored

- **Invariant 6 (RLS at the DB layer).** Export reuses the same RLS-scoped reads the dashboard panels already use; no new data path.
- **Slice 178 chrome-honesty discipline.** Disabled-state with tooltip beats invisible-or-broken (AC-5).

## Canvas references

- `Plans/canvas/07-metrics.md` — board reporting is first-class; the dashboard is the snapshot source.
- `Plans/canvas/08-audit-workflow.md` — OSCAL export discipline; if the Export action ships OSCAL, it MUST use the existing OSCAL bridge (slice 026/027), not a new format.

## Dependencies

- **Slice 053** (board-pack composer) — merged. Backs "New board report".
- **No dashboard-snapshot export endpoint exists.** The implementing slice files a separate endpoint slice OR scopes this slice to the board-report CTA only.

## Anti-criteria (P0 — block merge)

- **P0-A1.** DOES NOT ship "Export" as a no-op button that posts a `console.log`. If the endpoint isn't ready, the action is omitted, not faked.
- **P0-A2.** DOES NOT ship "New board report" without the role gate. The mockup is silent on authz; the gate is non-negotiable per CLAUDE.md.
- **P0-A3.** DOES NOT bypass the board-pack composer's existing snapshot-attach flow (slice 053). Reuse, don't duplicate.

## Surfaced by

Slice 204 dashboard audit (parent). See `docs/audit-log/204-page-audit-dashboard.md` finding F-204D-3.
