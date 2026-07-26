# 230 — UI honesty: dashboard "Export" and "New board report" header actions missing

**Cluster:** Quality / UI parity (frontend)
**Estimate:** 0.5d
**Type:** AFK
**Status:** `implemented` (reclassified against archived-mockup guidance; both
shipped actions are backed by real endpoints)

## Narrative

Surfaced during the slice 204 per-page UI parity audit fleet (page slug:
`dashboard`; archived mockup file: `Plans/_archive/mockups/dashboard.html`).
Category (i) layout / chrome parity.

The dashboard mockup's header right-side action cluster
(`Plans/_archive/mockups/dashboard.html` lines 125–131) renders two buttons:

1. **"Export"** — secondary button. Mockup implies a one-click dashboard-state export (CSV / PDF / OSCAL bundle of current posture + risks + freshness + drift snapshots).
2. **"New board report"** — primary CTA button with leading arrow icon. Mockup implies a click navigates to the board-pack composer (`/board-packs/new`) with the dashboard's current snapshot pre-attached.

The live `/dashboard` header (`web/app/(authed)/dashboard/page.tsx` lines 94–103) renders no action buttons. The H1 region is text-only. The two CTAs in the mockup are unrepresented.

**Why this is a finding.** The two CTAs are the dashboard's primary "act on what you see" affordances. Their absence pushes operators to sidebar-navigate to Board Packs and click through to create a new report from scratch — losing the dashboard's snapshot context. For the solo-security-leader persona on a board-report deadline, that's daily friction.

**Historical `not-ready` reason.** At filing time, "New board report" was
believed to map to a `/board-packs/new` composer and "Export" was believed to
lack a dashboard-snapshot export endpoint. Both assumptions were rechecked in
the implementation fire: `/board-packs/new` is stale and absent, while the
dashboard export endpoint now exists from slice 269.

## Slice 230 implementation classification

Archive check first: `Plans/_archive/mockups/README.md` says per-page
divergence between archived iteration-1 mockups and shipped `web/` is expected
and is **not** automatically fileable drift. This slice was kept only because
the two header actions name first-class product operations, so each action was
reclassified against the live app/backend instead of treating the old mockup as
binding design.

- **Export:** genuine ship-gap, not merely expected mockup divergence. The
  archived mockup's exact CSV / PDF / OSCAL menu is stale, but slice 269 later
  shipped the real platform endpoint: `GET /v1/dashboard/export?format=json|csv|xlsx`.
  Slice 230 adds the missing BFF at `GET /api/dashboard/export` and the
  dashboard header menu for the endpoint's actual supported formats. No PDF or
  OSCAL option ships because the backing endpoint does not support those formats.
- **New board report:** genuine ship-gap, not merely expected mockup divergence.
  The old `/board-packs/new?from=dashboard-snapshot...` composer route is stale
  and does not exist in `web/app`, but the live board-pack draft generation
  endpoint exists: `POST /api/board-packs` -> `POST /v1/board-packs`. Slice 230
  wires the dashboard CTA to generate a draft from the current date snapshot and
  opens the created `/board-packs/{id}` review page.

No follow-up OE was filed for a missing endpoint: the original Export blocker was
closed by slice 269 before this implementation fire. The UI intentionally omits
any unbacked PDF/OSCAL export option and any `/board-packs/new` composer link.

## Threat model

**S — Spoofing.** Both actions reuse the existing session bearer.

**T — Tampering.** "New board report" mutates state (creates a board-pack draft). It MUST respect the existing board-pack write authz (slice 053 owns the gate). The Playwright e2e-audit harness's read-only guardrail correctly classifies it as a mutating action — the implementing slice's e2e spec uses the functional `web/e2e/` harness, not the audit harness.

**I — Info disclosure.** The dashboard snapshot may include freshness numbers, top risks, drift events — all RLS-scoped to the active tenant. Export must NOT cross-tenant-leak. Implementing slice's authz test owns this.

**Verdict.** **needs-mitigations.** Standard RLS + authz tests on the backing endpoints. None block this audit-spillover.

## Acceptance criteria

- **AC-1.** Header right-side action cluster renders only actions backed by live endpoints.
- **AC-2.** "New board report" posts to `POST /api/board-packs`, then opens the returned `/board-packs/{id}` draft review page.
- **AC-3.** "Export" opens a small menu for the real dashboard export formats (JSON / CSV ZIP / XLSX). PDF and OSCAL are omitted because the endpoint does not support them.
- **AC-4.** Both actions are role-gated in the UI: "New board report" renders only for admin callers; "Export" renders only for admin / `grc_engineer` callers, matching the backend handler's effective access gate.
- **AC-5.** Disabled-state honesty: if the dashboard is in a degraded state (e.g. all panel queries errored), the buttons render disabled with a tooltip explaining why ("Snapshot incomplete — refresh panels first").

## Constitutional invariants honored

- **Invariant 6 (RLS at the DB layer).** Export reuses the same RLS-scoped reads the dashboard panels already use; no new data path.
- **Slice 178 chrome-honesty discipline.** Disabled-state with tooltip beats invisible-or-broken (AC-5).

## Canvas references

- `Plans/canvas/07-metrics.md` — board reporting is first-class; the dashboard is the snapshot source.
- `Plans/canvas/08-audit-workflow.md` — OSCAL export discipline; if the Export action ships OSCAL, it MUST use the existing OSCAL bridge (slice 026/027), not a new format.

## Dependencies

- **Board-pack generation endpoint** — present in `POST /v1/board-packs`, exposed
  through `POST /api/board-packs`. Backs "New board report".
- **Slice 269 dashboard snapshot export endpoint** — merged. Backs "Export" via
  `GET /v1/dashboard/export?format=json|csv|xlsx`, exposed through the slice 230
  BFF `GET /api/dashboard/export`.

## Anti-criteria (P0 — block merge)

- **P0-A1.** DOES NOT ship "Export" as a no-op button that posts a `console.log`. If the endpoint isn't ready, the action is omitted, not faked.
- **P0-A2.** DOES NOT ship "New board report" without the role gate. The mockup is silent on authz; the gate is non-negotiable per CLAUDE.md.
- **P0-A3.** DOES NOT bypass the board-pack composer's existing snapshot-attach flow (slice 053). Reuse, don't duplicate.

## Surfaced by

Slice 204 dashboard audit (parent). See `docs/audit-log/204-page-audit-dashboard.md` finding F-204D-3.
