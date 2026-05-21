# Slice 178 — UI honesty audit, first-pass report

**Date:** 2026-05-20
**Harness version:** slice-178 `web/e2e-audit/`
**Target:** static code analysis of `main @ 84c2b41` + manifest at `web/e2e-audit/mockup-spec.json`
**Route coverage:** the 10 v1 routes locked in AC-8.

## Run method

The harness's first-pass run was executed as a **structured static
analysis** of the production frontend source tree rather than a
runtime Playwright run against a live `docker compose up` stack. The
agent's worktree did not have a docker daemon available, and standing
up the slice-037 seeded stack inside the worktree was out of budget.
The substitution preserves the spec's intent (AC-16 — produce real
findings to file as spillover slices) by reading the source files the
harness's runtime would have encountered and applying the same three
heuristic patterns the harness uses at runtime (AC-5a / 5b / 5c) plus
the SHIP-GAP / HONESTY-GAP / MOCKUP-STALE diff (AC-11) against the
manifest.

The CI job `Frontend · UI honesty (advisory)` will execute the
**runtime** harness against the seeded stack on every PR. This report
is the **point-in-time** first pass; the runtime job is the durable
gate. Any divergence between the static finding set and the first CI
run will be reconciled as either a manifest fix or a new spillover
slice.

## Summary

| total | HONESTY-GAP | SHIP-GAP | MOCKUP-STALE | spillover slices filed |
| ----- | ----------- | -------- | ------------ | ---------------------- |
| 8     | 6           | 0        | 2            | 183, 184, 185, 186     |

(The 8-finding total is **substantive findings only**. Trivial /
expected forward-looking placeholders that the manifest's
`allowedExtraTestIds` set already covers — e.g.
`evidence-stream-placeholder` on `/controls/:id`, the
`framework-posture-empty` empty-state on `/dashboard` — are NOT
counted.)

## Findings

### HONESTY-GAP findings

These are the load-bearing audit signal — live UI shipped that is not
backed by a feature.

#### F-178-1 · `/calendar` · dead-anchor fallback for unrouted event types

- **Subject:** `web/components/calendar/agenda-view.tsx` and
  `web/components/calendar/month-grid-view.tsx` both implement a
  `linkFor(ev)` function whose default branch returns `"#"` for any
  event type not in `{ audit, exception, policy, control }`.
- **Heuristic:** AC-5a — dead anchor.
- **Why it's a HONESTY-GAP:** the `default` branch will never fire
  given the current four-event-type spec, but `<a href="#">` is
  rendered for any unrecognized event type the BFF returns. The
  harness's dead-anchor detector flags every literal `href="#"` on
  the page — this is a brittle defensive default that should either
  be removed (route the event to its known target) or replaced with
  a non-anchor element (a disabled span with explanatory tooltip).
- **Suggested action:** filed as spillover slice **#183**.

#### F-178-2 · `/calendar` · event link to `/admin/exceptions/<id>` 404s

- **Subject:** `linkFor` returns `/admin/exceptions/<id>` for
  `exception` events. The `/admin/exceptions` route DOES NOT EXIST
  in the live UI tree (`web/app/admin/` has `features`, `api-keys`,
  `sso`, `audit`, `users`, but no `exceptions` page). The link will
  hit Next.js's default 404 page.
- **Heuristic:** AC-5a — dead anchor (resolves to 404 on probe).
- **Suggested action:** filed as part of **#183** (calendar dead-link
  family).

#### F-178-3 · `/calendar` · event link to `/policies/<id>` 404s

- **Subject:** `linkFor` returns `/policies/<id>` for `policy`
  events. There is no `/policies/[id]/page.tsx` — only the list page.
  The link 404s.
- **Heuristic:** AC-5a — dead anchor (resolves to 404 on probe).
- **Suggested action:** filed as part of **#183**.

#### F-178-4 · `/audits` · row-click routes to a non-existent detail page

- **Subject:** `web/app/(authed)/audits/page.tsx:504` — the row-click
  handler is `onRowClick={(p) => router.push(/audits/${encodeURIComponent(p.id)})}`.
  The code's own comment (lines 498-503) acknowledges:
  > "the route is a placeholder — the per-period detail page is a
  > future slice. Today this routes to /audits/{id} which 404s with
  > the standard Next.js not-found UI"
- **Heuristic:** AC-5a — every row in the audits table is a
  click-target whose destination 404s.
- **Why it's a HONESTY-GAP:** the table promises a per-period detail
  page; the user clicks; the page 404s. The slice author was
  intentional about this (the comment is honest internal docs), but
  the LIVE UI does not say "detail page coming soon" — it just
  presents clickable rows that 404. Either the row should be
  non-clickable until the detail page ships, or a tooltip/banner
  should explain.
- **Suggested action:** filed as spillover slice **#184**.

#### F-178-5 · `/risks` · row-click routes the user to `/risks/hierarchy?focus=<id>` instead of a per-risk detail

- **Subject:** `web/app/(authed)/risks/page.tsx:398-409` — the
  row-click handler routes to `/risks/hierarchy?focus=<id>` because
  "a dedicated per-risk detail route lives in a future slice."
- **Heuristic:** structural — the row-click destination is a workable
  substitute, not the documented behavior the table implies.
  Different from F-178-4 (which 404s) because this destination
  exists; the HONESTY-GAP is that the table presents itself as "click
  to view risk detail" while in fact taking the user to the
  hierarchy.
- **Suggested action:** filed as spillover slice **#185** (one finding,
  one slice — distinct from the audits-row-click 404 case because the
  fix shape is different).

#### F-178-6 · sidebar dead-anchor risk — `/admin` entry visible to non-admin users

- **Subject:** `web/components/shell/sidebar.tsx` — the sidebar NAV
  array includes `{ href: "/admin", label: "Admin" }` unconditionally.
  Non-admin users will see + click the entry; the admin layout's
  authz gate handles the 403, but the chrome is misleading.
- **Heuristic:** AC-5a — for non-admin TEST_BEARER the destination
  resolves to a 403 page (a Next.js error page is not literally a
  404, but the harness's dead-anchor probe captures the non-2xx).
- **Why it's a HONESTY-GAP:** the sidebar should conditionally hide
  the Admin entry for non-admin users, OR render it as a styled
  affordance only when the bearer has `roles[admin]` in its
  credential.
- **Suggested action:** filed as spillover slice **#186**.

### MOCKUP-STALE findings

These are mockup elements the slice owner has decided to defer
indefinitely (or that have already been removed from the live UI
without the mockup being updated).

#### F-178-7 · `Plans/mockups/dashboard.html` · "Vendors" sidebar entry is rendered with `href="#"`

- **Subject:** the dashboard mockup's sidebar shows a `Vendors` link
  with `href="#"` (line ~88 in `dashboard.html`). The live UI now
  has a real `/vendors` route (slice 092). The mockup is one
  iteration behind.
- **Suggested action:** filed as part of mockup-refresh spillover
  **#183** (group with the dead-link family — they touch the same
  mockup file).

#### F-178-8 · `Plans/mockups/dashboard.html` · sidebar's "Profile" / generic-help entries use `href="#"`

- **Subject:** the dashboard mockup has two `<a href="#">` entries
  that don't correspond to any production route. The mockup
  pre-dates the production sidebar — same root cause as F-178-7.
- **Suggested action:** filed as part of **#183**.

## Coverage gaps (deferred to spillover, per AC-9)

The following routes are NOT audited in v1:

| route               | reason                                                               | follow-on |
| ------------------- | -------------------------------------------------------------------- | --------- |
| `/admin/*`          | Needs separate admin TEST_BEARER (threat-model E mitigation)         | future    |
| `/framework-scopes` | No mockup counterpart; post-dates iteration-1                        | future    |
| `/vendors`          | No mockup counterpart                                                | future    |
| `/exceptions`       | No mockup counterpart                                                | future    |
| `/catalog/:anchor`  | No mockup counterpart                                                | future    |
| `/dashboards/*`     | No mockup counterpart (the plural; `/dashboard` singular IS audited) | future    |

A single spillover slice can extend the manifest to cover any of
these once the maintainer agrees on (a) mockup parity and (b) admin
bearer plumbing.

## Spillover slices filed

| Slice | Title                                                            | Type | Status |
| ----- | ---------------------------------------------------------------- | ---- | ------ |
| 183   | UI honesty: calendar dead-link family + dashboard mockup refresh | AFK  | ready  |
| 184   | UI honesty: audits row-click 404 (per-period detail placeholder) | AFK  | ready  |
| 185   | UI honesty: risks row-click routes to hierarchy, not detail      | AFK  | ready  |
| 186   | UI honesty: sidebar "Admin" entry shown to non-admin users       | AFK  | ready  |

## Decisions made (JUDGMENT slice notes)

The slice is `Type: AFK` per the spec front matter, but two
build-time judgment calls were resolved by Claude during
construction:

1. **`/calendar` has no mockup counterpart.** The spec's AC-8 locks
   the `/calendar` route as one of the ten v1 audited routes, but
   `Plans/mockups/` contains no `calendar.html`. P0-178-11 forbids
   referencing non-existent mockups. The resolution: extend the
   manifest's `mockupPath` field to allow `null`, treating
   no-mockup routes as "HONESTY-GAP heuristics only; SHIP-GAP and
   MOCKUP-STALE skipped". The mockupPath regex still rejects bogus
   string values; `null` is the explicit absence marker.
   Justification: this preserves the spec's intent (audit the route
   for forward-looking-UI signals) without falsely asserting a
   mockup exists. Confidence: high. Revisit if maintainer wants
   a `calendar.html` mockup backfill (file as a spillover then).

2. **First-pass run was static-only.** AC-16 says "run the harness
   once locally against the seeded docker-compose stack and commit
   the report." The agent did not have a docker daemon in the
   worktree. The resolution: ship the harness fully built + CI-wired
   (so the runtime job will produce the first true live report on
   PR open), and ship this static-analysis substitute as the
   first-pass record. The substitute applies the same AC-5
   heuristics the runtime harness uses, just against grep'd source
   instead of rendered DOM. Confidence: medium — the static pass
   may miss findings that only render at runtime (conditional
   panels, client-side feature-flag toggles), and may flag patterns
   that the runtime page tree-shakes out. The CI run on PR open is
   the durable correction. Revisit: when the CI job's first live
   run lands a sticky comment, diff the static set against the
   live set; file a follow-on if any divergence is substantive.

## Revisit once in use

- After 5+ PRs of advisory-mode runs, evaluate promotion to a
  required check (the spec's deferred follow-on). The non-zero-
  findings threshold for required-promotion is a separate decision
  the maintainer makes once flake rate is observable.
- If a new mockup is added under `Plans/mockups/` (e.g. a backfill
  `calendar.html`), update `web/e2e-audit/mockup-spec.json` to
  reference it and flip the entry's `mockupPath` from `null` to
  the new filename.
- The `forward-looking-UI` heuristics in `lib/heuristics.ts` may
  need to grow as the app ships AI-assist surfaces (slice 173+):
  surface a heuristic for unapproved AI-draft text rendering,
  matching the schema-level enforcement boundary.
