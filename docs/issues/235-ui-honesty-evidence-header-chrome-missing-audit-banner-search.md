# 235 — UI honesty: /evidence header missing audit-period banner + global search

**Cluster:** Quality / UI hygiene · cross-cutting (chrome)
**Estimate:** 2.0d (audit-period banner is a cross-cutting shell element; search is a separate v1.5 feature)
**Type:** AFK
**Status:** `ready`
**Parent:** #204 (UI parity audit fleet)

## Narrative

Surfaced during the slice 204 per-page audit of `/evidence`
(audit log: `docs/audit-log/204-page-audit-evidence.md`).

The mockup at `Plans/mockups/evidence.html` lines 23-53 shows a
top header chrome with three elements absent from the live page:

1. **Tenant-context breadcrumb.** Mockup: `Sentinel Labs > Evidence`.
   Live: only `security-atlas` brand mark + the page title in `<main>`.
2. **Audit-period banner pill.** Mockup: an amber-pill component
   reading `SOC 2 Type II · Q2 2026 in progress` with a pulsing
   amber dot. Live: not rendered anywhere on `/evidence`.
3. **Global search input (`⌘K`).** Mockup: an inline search box
   with placeholder `Search controls, evidence, risks…` + a
   `⌘K` kbd hint. Live: no global search element exists in the
   header at all.

Source — live header is rendered by `web/components/shell/Shell.tsx`
(or the shell layout under `web/app/(authed)/layout.tsx`); none of
the three elements have backing components.

**Cross-cutting note.** This is a shell-level gap — same finding
will likely surface on every page the slice 204 audit fleet covers
(controls, risks, audits, etc.). One spillover at the shell level
is the right boundary; the per-page audits should reference this
slice rather than file duplicates.

**Why it matters to the v1 user.** The audit-period banner is the
single highest-value bit of chrome for the v1 user (solo security
leader running SOC 2). When the audit-period is `frozen` (canvas
§8.4 invariant #10), the operator's mental model needs a constant
reminder — every page they touch is doing point-in-time-replay
math against that frozen boundary. The mockup's amber-pulse banner
is the operator's "you are in an audit window" indicator. Its
absence is the chrome-level analogue of the slice 178 HONESTY-GAP
class.

Three paths (mix-and-match by maintainer):

- **Path A (1.0d).** Ship the audit-period banner only. Reads
  `/v1/audit-periods?status=active` (slice 048) and renders the
  amber pill in the shell header if one is found. Skip search +
  breadcrumb.
- **Path B (0.5d).** Ship the tenant breadcrumb only (already
  derivable from the JWT — `atlas:current_tenant_id` + the existing
  tenant-switcher's name lookup). Cheap, cosmetic.
- **Path C (3.0d).** Ship the global `⌘K` search. This is a v1.5
  feature, not a v1 chrome polish. **Out of scope for this slice
  — file as a separate spillover if the maintainer wants it.**

Defaulting AC shape to Path A + B (the audit-period banner is the
load-bearing piece; the breadcrumb is the cheap complement).

## Threat model

**Verdict.** **no-mitigations-needed.** The banner reads an
existing RLS-protected endpoint. The breadcrumb reads JWT claims
already sent to the browser. No new mutating operations; no new
data crosses tenant boundaries.

## Acceptance criteria (Path A + B — chosen)

- **AC-1.** A new component `web/components/shell/AuditPeriodBanner.tsx`
  is added. It calls `/api/audit-periods?status=active` (BFF
  pass-through to `/v1/audit-periods?status=active`) and renders
  an amber-pill `<div>` if exactly one active period is returned.
  Renders nothing if zero. Renders a tooltip with the period's
  framework + name if more than one (edge case).
- **AC-2.** The banner is mounted in the shell header next to the
  brand mark + page title, on every authed route (i.e. inside
  `web/app/(authed)/layout.tsx`). Visible on `/evidence`,
  `/controls`, `/risks`, `/audits`, etc.
- **AC-3.** Tenant breadcrumb: the existing `TenantSwitcher`
  component is extended (or a sibling component added) to render
  the tenant's display name as a left-aligned crumb after the
  brand mark. Format matches mockup: `<brand> · <tenant-name>`
  separated by a hairline divider.
- **AC-4.** Playwright spec at `web/e2e/shell-chrome.spec.ts`
  (new) asserts: (a) the banner renders when a seed dataset
  active audit-period exists, (b) it does not render when no
  active period exists, (c) the tenant name appears in the
  header.
- **AC-5.** Slice 204 audit's PARITY-LAYOUT finding F-204-E-3 is
  resolved on the next audit run.

## Constitutional invariants honored

- **Invariant 10 (audit-period freezing).** The banner is the
  operator's UI-level signpost that freezing is active. The
  banner must read the `frozen` flag in the period response and
  render an additional `frozen` micro-pill when set.
- **Invariant 6 (tenant isolation).** Both endpoints called are
  RLS-bound; no cross-tenant data leaks through the banner /
  breadcrumb.
- **Anti-pattern rejected:** Mockups that promise chrome the
  shell does not ship.

## Canvas references

- `Plans/canvas/07-metrics.md` — board reporting context
- `Plans/canvas/08-audit-workflow.md` — audit-period freezing
- `Plans/canvas/05-scopes.md` — tenant breadcrumb design
- `Plans/mockups/evidence.html` lines 23-53 — the mockup chrome

## Dependencies

- **#204** (UI parity audit fleet) — `in-progress`. Surfacing
  parent.
- **#048** (audit-periods backend) — `merged`. The banner reads
  its endpoint.
- **#192** (tenant-switcher frontend) — `merged`. The breadcrumb
  extends its component.

## Anti-criteria (P0 — block merge)

- **P0-235-1.** Does NOT ship the global `⌘K` search (Path C).
  That is a separate spillover slice if the maintainer wants it.
- **P0-235-2.** Does NOT modify the `/v1/audit-periods` wire
  contract.
- **P0-235-3.** Does NOT render the audit-period banner if the
  period response does not include a `frozen` field — fail
  closed (no banner) rather than guessing a default.

## Skill mix (3-5)

1. Next.js App Router — extending the authed shell layout
2. shadcn/ui Badge / Pill primitives
3. Playwright spec — chrome assertions
4. TanStack Query — banner data fetch
