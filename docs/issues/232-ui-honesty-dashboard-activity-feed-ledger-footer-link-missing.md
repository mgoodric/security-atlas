# 232 — UI honesty: dashboard activity-feed "View full activity ledger" footer link missing

**Cluster:** Quality / UI parity (frontend)
**Estimate:** 0.25d
**Type:** AFK
**Status:** `implemented` (slice OE-416; public `/activity` ledger route exists)

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

**Status: ship-gap, not mockup-stale.** OE-416 rechecked
`Plans/_archive/mockups/README.md`: archived iteration-1 mockup divergence is
expected and is not automatically fileable drift. This finding remains a
ship-gap because the full activity ledger is a shipped product surface, not
mockup-only chrome: slice 270 added a non-admin `/activity` ledger route backed
by `/api/activity` -> `/v1/activity/unified`, and it is reachable to every
signed-in tenant member. The dashboard simply lacked the navigation affordance
to that real route.

**Route resolution.** OE-416 uses `/activity`, not `/admin/audit-log`. The
admin audit log remains role-gated; `/activity` is the dashboard-appropriate
destination because it carries the non-admin row-visibility/OPA contract.

## Threat model

**S — Spoofing.** No new auth surface. The ledger view reuses the existing session bearer.

**I — Info disclosure.** The full ledger MUST RLS-scope identically to the dashboard panel (slice 066 D1 — `admin_audit_log_v` filtered by evidence_audit_log RLS). The implementing slice's RLS test asserts this.

**Verdict.** **needs-mitigations.** Standard RLS + role-gate test on the backing route. None block this audit-spillover.

## Acceptance criteria

- **AC-1.** The `ActivityFeedPanel` renders a centered footer link "View full activity ledger →" below the event list. **Implemented in OE-416.**
- **AC-2.** The link target is a real route — either `/activity` (non-admin scope) or conditionally `/admin/audit-log` (admin scope) per the slice 186 pattern. **Implemented in OE-416 with `/activity`; `web/e2e/dashboard.spec.ts` asserts navigation reaches the page shell.**
- **AC-3.** If the user lacks access to the target route, the footer link is omitted (not rendered disabled — for a small affordance, omission is cleaner than a disabled link). **Satisfied by choosing `/activity`, which is reachable to all signed-in tenant members.**
- **AC-4.** The target route (whichever the implementing slice chooses) RLS-scopes to the active tenant, returns the same shape as the dashboard panel's events but unpaginated, and renders newest-first.
- **AC-5.** Empty-state honesty: if the ledger is empty, the link is omitted (no point linking to a known-empty page from the empty-panel state). **Implemented in OE-416 by rendering the footer only in the non-empty activity-list branch.**

## Constitutional invariants honored

- **Invariant 6 (RLS at the DB layer).** The ledger view inherits the dashboard panel's RLS path.
- **Slice 186 affordance-honesty.** Don't advertise what the user can't use. AC-3 is the enforcement.

## Canvas references

- `Plans/canvas/04-evidence-engine.md` §4.3 — the append-only evidence ledger is a first-class surface; "click through to the full ledger" is the canonical follow-through from a dashboard summary.

## Dependencies

- **A non-admin `/activity` view OR the slice 186 role-conditional rendering pattern.** Resolved by slice 270's `/activity` route; OE-416 links there.
- **Slice 067** (admin audit-log page) — merged. Reusable as the admin-scoped destination.
- **Slice 186** (role-conditional sidebar entry) — merged. Reusable as the role-gating pattern.
- **Slice 685 relationship.** Slice 685 remains adjacent but separate. It needs
  the dashboard `/v1/activity` endpoint widened beyond the evidence branch and
  given a kind/source filter for dashboard chips. OE-416 does not need that
  widening because the footer navigates to the already-shipped `/activity`
  ledger, whose BFF uses `/v1/activity/unified`.

## Anti-criteria (P0 — block merge)

- **P0-A1.** DOES NOT link to a 404 destination. AC-2's target must be a real shipped route.
- **P0-A2.** DOES NOT link to an admin-only route from a non-admin context. AC-3 is the enforcement.
- **P0-A3.** DOES NOT render the link in the empty-state. AC-5 is the enforcement.

## Surfaced by

Slice 204 dashboard audit (parent). See `docs/audit-log/204-page-audit-dashboard.md` finding F-204D-5.
