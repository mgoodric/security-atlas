# 243 — UI honesty: risks top bar omits breadcrumb, search, audit banner, avatar

**Cluster:** Quality / UI hygiene (frontend)
**Estimate:** 0.5d (presentational; the global-search piece is tracked separately as #223 and is the substantive one)
**Type:** JUDGMENT
**Status:** `ready`

## Narrative

Surfaced during slice 204 audit fleet (`/risks` page), captured as a
follow-up per continuous-batch policy. The mockup at
`Plans/mockups/risks.html` (lines 23-53, `TOP BAR` block) shows the
top bar carrying:

- Tenant breadcrumb chip: `Sentinel Labs > Risks`.
- Global search input (placeholder `Search controls, evidence,
risks…`, `⌘K` kbd hint, right-aligned).
- Audit-in-progress banner pill (amber, pulsing dot, text `SOC 2 Type
II · Q2 2026 in progress`).
- User avatar circle + display name.

The live `/risks` page consumes `<ListPage>`; the chrome is rendered
by the authed-layout header at `web/app/(authed)/layout.tsx`. It
shows logo + `security-atlas` wordmark + `v0 · self-host` chip +
TenantSwitcher (slice 192) + `Sign out` button. No global search
input, no audit-in-progress banner, no avatar.

This is the SAME horizontal gap previously flagged on `/controls`
(#223), `/audits` (#213), and `/dashboard` (#228). The remediation
necessarily lands in the authed-layout header — fixing it once
satisfies all four spillovers. This slice is filed separately to
keep the per-page audit traceable; the maintainer can collapse the
four into a single layout-shell slice at execution time.

## Threat model

Identical to slice #223. Global-search is the only sensitive
affordance — tenant isolation must flow through the existing
RLS-on-bearer-cookie path. The breadcrumb, audit banner, and avatar
are presentational.

**Verdict.** `mitigations-required` only if shipping the search
piece; presentational pieces alone are `no-mitigations-needed`.

## Acceptance criteria (Option A — chrome-only, defer search to #223)

- **AC-1.** Tenant breadcrumb chip "<tenant> > Risks" lands in the
  authed-layout header, visible on every authed page (not just
  `/risks`). The chip uses `TenantSwitcher`'s selected-tenant label
  as the first segment; the page slug renders as the second segment.
- **AC-2.** SOC 2 audit-in-progress banner pill renders in the top
  bar when the tenant has at least one `AuditPeriod` row with
  `status='in_progress'`. The pill text reads
  `<framework> <audit_type> · <period_label> in progress`. When no
  audit is in progress, the pill is absent (not "no audits" stub).
- **AC-3.** User avatar circle (initials) + display name render to
  the right of the audit banner pill, to the left of the
  TenantSwitcher. Initials derive from the JWT `name` claim
  (slice 187's claim set).
- **AC-4.** Slice 178 audit-honesty heuristic: this slice does NOT
  ship the global-search affordance — that is `#223`'s scope. The
  spillover here is presentational chrome only.
- **AC-5.** Existing Playwright spec `risks-list.spec.ts` is updated
  to assert the breadcrumb chip is visible on `/risks`. The
  audit-banner assertion is conditional on the e2e seed including an
  in-progress audit period (slice 088 provides the fixture).
- **AC-6.** CHANGELOG entry: "Authed layout: tenant breadcrumb,
  audit-in-progress pill, and user avatar in the top bar (#243;
  slice 204 audit follow-on)".

## Constitutional invariants honored

- **Tenant isolation at DB layer.** The audit-banner query runs
  under the caller's tenant context — RLS enforces isolation. No
  tenant override in the BFF or page handler.
- **Truth-telling chrome.** The audit-banner pill renders ONLY when
  an audit period is actually in progress. No "SOC 2 Type II ·
  Q2 2026 in progress" stub when the tenant has no such period.

## Canvas references

- `Plans/canvas/08-audit-workflow.md` — audit periods + freezing
- `Plans/canvas/09-tech-stack.md` — auth + tenancy plumbing

## Dependencies

- **#187** (OAuth AS scaffolding) — `merged`. JWT claim set ships
  `name` claim.
- **#192** (multi-tenant switch) — `merged`. TenantSwitcher in the
  header carries the tenant label.
- **#088** (audit-period fixtures) — `merged`. e2e seed provides the
  audit-banner test condition.
- **#223** (controls top-bar parity) — `ready`. Same remediation
  surface; maintainer may collapse the two.

## Anti-criteria (P0 — block merge)

- **P0-243-1.** Does NOT ship the global-search input. That is
  #223's scope; combining the two muddies the audit-traceability.
- **P0-243-2.** Does NOT hardcode the audit-banner text. The pill
  renders FROM live `AuditPeriod` data, not from a stub.
- **P0-243-3.** Does NOT remove the existing TenantSwitcher.
  Breadcrumb + switcher coexist (breadcrumb is a label; switcher is
  the action).
- **P0-243-4.** Does NOT add a fourth top-bar entry that the mockup
  does not show. Scope is exactly: breadcrumb + audit pill + avatar.

## Skill mix (3-5)

1. Next.js App Router — authed-layout header refactor
2. Playwright spec update — slice-069 functional flow stays green
3. shadcn/ui Avatar + Badge primitives
4. RLS context plumbing — audit-period query path
