# 232 — UI honesty: dashboard activity-feed "View full activity ledger" footer link missing

**Cluster:** Quality / UI parity (frontend)
**Estimate:** 0.25d
**Type:** AFK
**Status:** `not-ready` (depends on a public ledger route — see Dependencies)

## Narrative

Surfaced during the slice 204 per-page UI parity audit fleet (page slug: `dashboard`; mockup file: `Plans/mockups/dashboard.html`). Category (i) layout / chrome parity with a (iv) mockup-stale overlap (resolved as ship-gap, not mockup-stale, on inspection).

The dashboard mockup's activity-feed panel ends with a centered footer link (`Plans/mockups/dashboard.html` lines 608–610):

```html
<div class="mt-4 text-center">
  <a href="#" class="text-xs text-slate-500 hover:text-slate-700"
    >View full activity ledger →</a
  >
</div>
```

The mockup's intent is "the dashboard shows the 6 most recent events; click to see the full ledger". The live `ActivityFeedPanel` (`web/components/dashboard/activity-feed-panel.tsx`) renders zero footer link — once the operator has paged through the displayed events, there is no path to the full ledger from the dashboard.

**Status: ship-gap, not mockup-stale.** The full activity ledger surface IS a real product concept — slice 062's `admin_audit_log_v` view is the backing data, and slice 067 (admin audit-log page at `/admin/audit-log`) ships an authz-gated full view. The dashboard simply lacks a navigation affordance to it.

**Why `not-ready`.** The natural destination (`/admin/audit-log`) is admin-only — surfaceing a "View full activity ledger →" link from a dashboard panel that non-admin users see is a UX honesty problem (slice 186 precedent: don't advertise affordances the user can't use). The implementing slice must either:

1. Ship a non-admin "activity ledger" view at `/activity` that mirrors the same data with the same RLS scope as the dashboard panel (recommended), OR
2. Conditionally render the footer link based on the user's role (slice 186 pattern), OR
3. Wait for the v2 admin/non-admin role split to mature before deciding.

The implementing slice picks the path; this audit-spillover only records the gap.

## Threat model

**S — Spoofing.** No new auth surface. The ledger view reuses the existing session bearer.

**I — Info disclosure.** The full ledger MUST RLS-scope identically to the dashboard panel (slice 066 D1 — `admin_audit_log_v` filtered by evidence_audit_log RLS). The implementing slice's RLS test asserts this.

**Verdict.** **needs-mitigations.** Standard RLS + role-gate test on the backing route. None block this audit-spillover.

## Acceptance criteria

- **AC-1.** The `ActivityFeedPanel` renders a centered footer link "View full activity ledger →" below the event list.
- **AC-2.** The link target is a real route — either `/activity` (non-admin scope) or conditionally `/admin/audit-log` (admin scope) per the slice 186 pattern.
- **AC-3.** If the user lacks access to the target route, the footer link is omitted (not rendered disabled — for a small affordance, omission is cleaner than a disabled link).
- **AC-4.** The target route (whichever the implementing slice chooses) RLS-scopes to the active tenant, returns the same shape as the dashboard panel's events but unpaginated, and renders newest-first.
- **AC-5.** Empty-state honesty: if the ledger is empty, the link is omitted (no point linking to a known-empty page from the empty-panel state).

## Constitutional invariants honored

- **Invariant 6 (RLS at the DB layer).** The ledger view inherits the dashboard panel's RLS path.
- **Slice 186 affordance-honesty.** Don't advertise what the user can't use. AC-3 is the enforcement.

## Canvas references

- `Plans/canvas/04-evidence-engine.md` §4.3 — the append-only evidence ledger is a first-class surface; "click through to the full ledger" is the canonical follow-through from a dashboard summary.

## Dependencies

- **A non-admin `/activity` view OR the slice 186 role-conditional rendering pattern.** Neither is shipped as a dashboard-linked surface today. The implementing slice picks one.
- **Slice 067** (admin audit-log page) — merged. Reusable as the admin-scoped destination.
- **Slice 186** (role-conditional sidebar entry) — merged. Reusable as the role-gating pattern.

## Anti-criteria (P0 — block merge)

- **P0-A1.** DOES NOT link to a 404 destination. AC-2's target must be a real shipped route.
- **P0-A2.** DOES NOT link to an admin-only route from a non-admin context. AC-3 is the enforcement.
- **P0-A3.** DOES NOT render the link in the empty-state. AC-5 is the enforcement.

## Surfaced by

Slice 204 dashboard audit (parent). See `docs/audit-log/204-page-audit-dashboard.md` finding F-204D-5.
