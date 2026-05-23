# 257 — UI honesty: control-detail top bar chrome parity (tenant breadcrumb, audit pill, user avatar)

**Cluster:** Quality / UI hygiene
**Estimate:** 0.25d (folds into the shared-chrome fix surfaced by #223)
**Type:** AFK

## Narrative

Surfaced during slice 204's per-page audit of `/controls/{id}` (see
`docs/audit-log/204-page-audit-control.md`, Finding 5). The mockup's
top bar (`Plans/mockups/control.html` lines 17-44) carries three
chrome affordances absent from the live page:

1. **Tenant breadcrumb chain** — `Sentinel Labs > Controls > MFA Enforcement`
   (mockup line 28-32). The breadcrumb names the current tenant + the
   list view ancestor + the control's title leaf. Live page has only
   `← All controls` above the page header (one link, no tenant
   context, no leaf title).
2. **SOC 2 Type II audit-in-progress amber pill** with a pulsing dot,
   reading "SOC 2 Type II · Q2 2026 in progress" (mockup lines 35-38).
   Live: absent.
3. **User avatar circle** with initials (mockup line 40). Live: only
   a `Sign out` button. The TenantSwitcher component (`$L19`) renders
   in the live header but is a switcher, not an avatar.

**This is the same shared-chrome shortfall** flagged by spillover #223
(controls list audit). Filing as a separate finding per slice 204's
per-page audit rule (one slice per finding per page), but the
implementation fix is almost certainly a single shared-header slice
that closes both #223 and #257 (and likely the equivalent on the
risks / audits / policies detail pages — those audit logs may surface
the same finding).

**The implementing engineer should:**

1. Check #223's current status. If #223 is `in-progress` or `in-review`,
   this slice merges into it (or closes as dup → #223). If #223 is
   `ready`, mark this slice `dup-of-223` and treat the shared chrome
   fix as the controls-list audit's responsibility.
2. If neither slice has shipped chrome work, ship the chrome fix once
   and close both this slice and #223 (and any sibling detail-page
   chrome spillovers) on the merge.

## Threat model

**Verdict.** **no-mitigations-needed.** Header chrome additions. The
tenant breadcrumb reads from already-fetched tenant context (the
TenantSwitcher already has this data — slice 192 shipped the
multi-tenant switch flow). The audit-in-progress pill reads from
`GET /v1/audits?status=in_progress&limit=1` (already on main). The
user avatar reads from the JWT subject + display name (already on
main). No new data path, no new auth surface.

## Acceptance criteria

- **AC-1.** A breadcrumb chip renders in the top bar showing the
  current tenant name (from TenantSwitcher's `currentTenant` state)
  followed by the page-stack — for `/controls/{id}`, the stack is
  `{tenant} > Controls > {control.title}`. The chevrons match the
  mockup's SVG chevron pattern.
- **AC-2.** When the page renders an empty-state for an unresolved
  control id (slice 152 / ADR-0004), the breadcrumb leaf reads
  `{control id, truncated}` — not the friendly title (which doesn't
  exist), and not a hardcoded "Unknown".
- **AC-3.** If any audit is in `status=in_progress` for the current
  tenant, the amber audit-in-progress pill renders (right side of
  the top bar) with `{audit.framework_name} {audit.type} · {period_label}` —
  e.g., "SOC 2 Type II · Q2 2026 in progress". Clicking the pill
  navigates to `/audits/{audit.id}/workspace`. The pill is hidden
  when no audit is in progress (no empty pill, no "—" placeholder).
- **AC-4.** A user avatar circle (initials) renders in the top
  bar, replacing or composing with the existing Sign out button.
  Hover/click opens a small menu (sign out + theme toggle + link
  to /settings). The Tenant switcher remains its own surface
  beside the avatar (the mockup composes them; the live page
  may compose them as the engineer judges fits the existing
  shadcn pattern).
- **AC-5.** This slice's chrome work is shared across detail
  pages (risks/[id], audits/[id], policies/[id]) — the fix lives
  in the layout component (`web/app/(authed)/layout.tsx` or
  whichever component renders the top bar today), NOT
  per-page.
- **AC-6.** Vitest covers the breadcrumb composition (tenant ·
  list ancestor · leaf) for the four detail-page routes.
- **AC-7.** Playwright covers: avatar menu opens on click,
  audit-in-progress pill click navigates to the audit workspace,
  breadcrumb tenant chip click navigates to /dashboard.

## Constitutional invariants honored

- **Tenant-context visibility.** The breadcrumb makes the current
  tenant visible in chrome — critical for the multi-tenant switch
  workflow (slice 192). Without it, an operator who switches
  tenants mid-session has no chrome-level confirmation of which
  tenant they're acting in.
- **UI-honesty.** The audit-in-progress pill renders only when
  a real in-progress audit exists. No placeholder copy.
- **Anti-pattern rejected:** "vanity trust centers" — this is the
  inverse: the live page strips back chrome the user needs to
  navigate confidently. Fixing it is anti-vanity in the honest
  direction.

## Canvas references

- `Plans/canvas/08-audit-workflow.md` — audit period semantics
  (informs the pill copy)
- `docs/audit-log/204-page-audit-control.md` Finding 5
- `docs/issues/223-ui-honesty-controls-top-bar-chrome-parity.md` —
  sibling finding from the controls-list audit; this slice
  likely subsumes / is subsumed by it

## Dependencies

- **#204** (UI parity audit) — parent.
- **#223** (controls list top bar chrome parity) — sibling /
  likely-dup. The implementing engineer triages first.
- **#192** (tenant switch) — already on main; provides the
  current-tenant data the breadcrumb reads.

## Anti-criteria (P0 — block merge)

- **P0-257-1.** Does NOT duplicate the implementation in #223.
  Triage first; merge into #223 OR consume #223 here, whichever
  ships first.
- **P0-257-2.** Does NOT render an empty audit-in-progress pill
  (the slice 178 dead-affordance anti-pattern). When no audit is
  in progress, the pill is fully absent.
- **P0-257-3.** Does NOT remove the existing TenantSwitcher
  component. It composes with the avatar; it is not replaced.
- **P0-257-4.** Does NOT add `Sentinel Labs`-shaped hardcoded
  copy anywhere. The tenant name reads from real tenant state.

## Skill mix (3-5)

1. Next.js App Router layout composition — the shared chrome
   lives in `web/app/(authed)/layout.tsx`.
2. shadcn/ui Breadcrumb + Avatar + DropdownMenu primitives.
3. TanStack Query — wiring the audit-in-progress query at
   layout level (cached across detail-page transitions).
4. UI-honesty discipline — empty pill = absent pill.
