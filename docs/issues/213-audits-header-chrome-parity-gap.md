# 213 — Audits page header chrome parity gap (breadcrumb + in-progress badge + global search + user avatar)

**Cluster:** frontend
**Estimate:** 1.0d
**Type:** JUDGMENT
**Status:** `ready`

## Narrative

Surfaced during slice 204 audit fleet (audits page), captured as
follow-up per continuous-batch policy.

The mockup at `Plans/mockups/audits.html` shows a header chrome that
the live `/audits` page does NOT render:

| Mockup element                                                                      | Live page                       |
| ----------------------------------------------------------------------------------- | ------------------------------- |
| Breadcrumb: `Sentinel Labs › Audits` (tenant name + page)                           | absent — no breadcrumb at all   |
| In-progress audit pill: amber `SOC 2 Type II · Q2 2026 in progress` badge top-right | absent                          |
| Global search box (`Search controls, evidence, risks…` + `⌘K` kbd hint)             | absent                          |
| User avatar + display name (`MG · Sam`)                                             | absent — only `Sign out` button |

The live page's `header` element today carries only: logo + product
name + `v0 · self-host` tag + `Sign out` form. This is the shared
authed-shell header (`web/app/(authed)/layout.tsx` or equivalent), so
the gap is global, not audits-specific — but the audits mockup is
where the parity divergence becomes visible because the in-progress
pill specifically references the SOC 2 period.

The shared-shell breadcrumb and global search are the larger lifts.
The in-progress-audit pill and user avatar are narrower wins that
already have backing data: the pill can read from
`GET /v1/audit-periods?status=in_progress` (existing endpoint); the
avatar can read from the existing user-info endpoint (`/api/me` or
similar — slice 191 ships JWT claims that include `sub`/`name`).

## Threat model

**Verdict.** **no-mitigations-needed.** Pure chrome additions reading
existing endpoints. The in-progress pill reads tenant-scoped data
already gated by RLS; same security envelope as the audits list.

## Acceptance criteria

- **AC-1.** A maintainer JUDGMENT decision records which subset of
  the four mockup elements ships in this slice vs. defers to follow-
  ons. (Recommended split: in-progress audit pill + user avatar in
  this slice; breadcrumb + global search as separate slices given
  cross-page surface.)
- **AC-2.** For each element that ships: visible on `/audits` and
  every authed page (the chrome is shared).
- **AC-3.** In-progress audit pill: queries `GET /v1/audit-periods`
  client-side (TanStack Query, 60s stale time), filters to
  `status === "in_progress"`, renders the most-recently-started
  period as an amber pill. If zero in-progress periods, the pill is
  hidden (not "no audit in progress" copy — silence is honest).
- **AC-4.** User avatar: reads display name + initials from the
  existing user-context source (no new endpoint). Falls back to the
  email's local-part if `name` is unset.
- **AC-5.** Playwright e2e spec asserts the in-progress pill appears
  when a fixture period exists with `status='in_progress'` and is
  absent otherwise.

## Constitutional invariants honored

- **Invariant 6 (tenant isolation).** The new pill reads
  `/v1/audit-periods` which is already RLS-tenant-scoped; no new
  tenant plumbing.
- **Invariant 10 (audit-period freezing).** The in-progress pill
  surfaces only `status='in_progress'`. Frozen periods do not appear
  in the pill (they have their own row affordance on the table).

## Canvas references

- `Plans/canvas/08-audit-workflow.md` — audit-period status model
- `Plans/mockups/audits.html` — lines 23-53 (the header chrome block)

## Dependencies

- **#204** — UI parity audit (surfacing parent)
- **#102** — audits page (the live surface to extend)
- **#191** — JWT claims carrying user name (needed for avatar copy)

## Anti-criteria (P0 — block merge)

- **P0-213-1.** Does NOT add a new platform endpoint. The pill
  reuses `GET /v1/audit-periods`; the avatar reuses the existing
  user-context source.
- **P0-213-2.** Does NOT render the in-progress pill if zero periods
  match. Silent absence is honest; "no audit in progress" copy
  would be UI clutter.
- **P0-213-3.** Does NOT scope-creep into the breadcrumb or global
  search if the maintainer JUDGMENT call defers them.
- **P0-213-4.** Does NOT mock the user-context source in production
  — the avatar reads real claims.

## Skill mix (3-5)

1. Next.js App Router shared layout
2. TanStack Query (the existing pattern used elsewhere on `/audits`)
3. Tailwind / shadcn-ui pill styling matching the mockup's amber
   palette
4. Playwright e2e assertion
