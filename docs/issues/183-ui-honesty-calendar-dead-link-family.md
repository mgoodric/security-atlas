# 183 — UI honesty: calendar dead-link family + dashboard mockup refresh

**Cluster:** Quality / UI hygiene
**Estimate:** 0.5d
**Type:** AFK
**Status:** `merged` (status reconciled 2026-06-03 — backlog drained per \_STATUS.md SoR; loop terminated batch 184)

## Narrative

Surfaced during slice 178 first-pass audit, captured per AC-17. The
calendar view's `linkFor(ev)` helper in
`web/components/calendar/agenda-view.tsx` and
`web/components/calendar/month-grid-view.tsx` produces three classes
of forward-looking-UI signals on `/calendar`:

1. **default → `"#"` dead anchor.** The switch statement's `default`
   case for any unrecognized event type returns `"#"`. While the BFF
   currently only emits the four allowed types (audit / exception /
   policy / control), the rendered HTML carries an `<a href="#">` for
   any divergent payload. The slice-178 dead-anchor heuristic flags
   these as a HONESTY-GAP.

2. **`/admin/exceptions/<id>` 404.** The `exception` branch returns
   `/admin/exceptions/<id>`. The route does not exist (`web/app/admin/`
   has `features`, `api-keys`, `sso`, `audit`, `users` — no
   `exceptions/`). Every exception event renders as a 404 link.

3. **`/policies/<id>` 404.** The `policy` branch returns
   `/policies/<id>`. There is no `web/app/(authed)/policies/[id]/`
   detail route. Every policy event renders as a 404 link.

Additionally, the dashboard mockup at `Plans/mockups/dashboard.html`
carries two `<a href="#">` sidebar entries (`Vendors` and a generic
profile/help link) that pre-date the production sidebar's real
`/vendors` route and current chrome — flagged by slice 178 as
MOCKUP-STALE. Bundling the mockup refresh into this slice keeps the
"dead-link family" coherent: the user-facing fix and the design-doc
fix touch related surfaces.

The fix shape is small and uncontroversial; the urgency is moderate
(the broken links are visible to any user landing on `/calendar`
today). This slice ships the AUDIT'S CORRECTIVE FIX — the
detection lives in slice 178; the remediation is here.

## Threat model

**Verdict.** **no-mitigations-needed.** This is a UI-only correction.
No new auth surface, no new data path, no new external IO. The
admin-only `/admin/exceptions` link presented to all users does NOT
escalate privilege (clicking it 404s; the user lands on the same
not-found page they would land on by typing any non-existent URL).
The fix removes a misleading affordance, it does not gate any
existing capability.

## Acceptance criteria

- **AC-1.** `linkFor(ev)` in both `agenda-view.tsx` and
  `month-grid-view.tsx` no longer returns `"#"`. The `default` branch
  either throws (TypeScript-exhaustive `assertNever` pattern) or
  returns a non-link rendering. The downstream consumer renders a
  `<span>` (not an `<a>`) for non-linkable events.
- **AC-2.** Exception events render with an explanatory tooltip + no
  link (the `/admin/exceptions/<id>` page does not exist; pretending
  it does is the bug). Future slice that ships the page can re-enable
  the link.
- **AC-3.** Policy events render with an explanatory tooltip + no
  link, same shape as exception events, for the same reason.
- **AC-4.** `Plans/mockups/dashboard.html` is updated: the `Vendors`
  sidebar entry's `href` becomes `vendors.html` IF a vendors mockup
  is added in this slice; otherwise the entry is removed (the
  mockup is not the source of truth for shipped surfaces). The
  generic profile/help `#` entries are removed.
- **AC-5.** The slice-178 first-pass report's F-178-1, F-178-2,
  F-178-3, F-178-7, F-178-8 are addressed; the slice 178 audit
  harness's next run (on this PR's CI) reports zero findings for
  these subjects.
- **AC-6.** Unit test added covering the new `linkFor` shape — at
  minimum, asserting the `exception` and `policy` branches return
  the no-link sentinel value and that the `default` branch never
  returns `"#"`.

## Constitutional invariants honored

- **Invariant 9 (manual evidence is first-class).** The fix does not
  touch evidence flows.
- **Slice 178's read-only constraint.** This slice fixes findings
  surfaced by slice 178's audit; it does NOT modify the audit
  harness itself (the harness is a generic detector).
- **Anti-pattern rejected:** "vanity trust centers" — the calendar
  view should not promise UI affordances that 404 on click.

## Canvas references

- `Plans/canvas/01-vision.md` §1.6 — UI-honesty anti-pattern
- `Plans/canvas/12-ui-fill-in-design-decisions.md` — sidebar nav
  ordering (the dashboard mockup's sidebar should match)
- `docs/audit-log/178-ui-honesty-first-pass.md` — F-178-1, F-178-2,
  F-178-3, F-178-7, F-178-8

## Dependencies

- **#178** (UI honesty audit harness) — `in-progress`. This slice is
  surfaced BY that slice's first-pass; it stays in `ready` because
  the audit-harness's existence is not a prerequisite for the fix.

## Anti-criteria (P0 — block merge)

- **P0-183-1.** Does NOT add a backing `/admin/exceptions/:id` or
  `/policies/:id` detail page in this slice. Those are independent
  features; this slice REMOVES the misleading affordance. Future
  slices can ship the detail pages.
- **P0-183-2.** Does NOT silently rewrite the BFF's
  `CalendarResponse` shape. The contract stays four event types.
- **P0-183-3.** Does NOT modify the slice-178 audit harness or
  manifest — this slice is downstream of the audit.
- **P0-183-4.** Trivial doc-only mockup edits are bundled into this
  PR (Amendment 2 allows batching trivial findings); the calendar
  code change is the substantive part.

## Skill mix (3-5)

1. Next.js App Router + shadcn/ui — fixing presentational
   components without changing the data contract.
2. Playwright (slice 069 + slice 178) — ensuring the audit harness
   sees the fix on its next run.
3. TypeScript exhaustiveness — the `assertNever` pattern for the
   `linkFor` default branch.
4. Mockup hygiene — Plans/mockups/ as a design-doc artifact, not a
   shipped surface.
