# 271 — Shared-shell breadcrumb (`<tenant> › <page>`)

**Cluster:** frontend
**Estimate:** 1.0d
**Type:** JUDGMENT
**Status:** `ready`

## Narrative

Spillover from slice 213. The audits mockup at
`Plans/mockups/audits.html` (lines 32-36) shows a breadcrumb chip on
the topbar of the shape `Sentinel Labs › Audits` — the operator's
tenant name on the left, a chevron divider, the current page name on
the right. Slice 213 deferred this element because the breadcrumb is
a global cross-page surface (every authed page has a different page
name to fill in), not an audits-only widget, and inventing the cross-
page pattern in a JUDGMENT slice for one page would be premature.

This spillover ships the breadcrumb across the shared authed-shell
topbar (`web/components/shell/topbar.tsx`). The same chip mockup
appears on every other product page (controls, evidence, risks,
audits, policies, dashboard, board-pack, settings, questionnaire —
verified during slice 213's design review).

## Threat model

**Verdict.** **no-mitigations-needed.** The tenant name comes from
the existing `/v1/me/tenants` endpoint (slice 192) that the
TenantSwitcher already consumes. The page name comes from Next.js
App Router segment metadata (route-derived, no user input). No new
endpoint, no new auth surface.

## Acceptance criteria

- **AC-1.** A `<Breadcrumb />` component renders in the shared
  topbar between the brand mark and the in-progress pill. The
  component reads the current tenant's name (existing source) and
  the current page name (derived from the URL segment) and renders
  `<tenant> › <page>` with a chevron divider matching the mockup
  copy + amber-free chrome.
- **AC-2.** Visible on every authed page (the chrome is shared via
  `(authed)/layout.tsx`).
- **AC-3.** Page-name derivation is centralized in a single
  helper (e.g. `web/lib/page-names.ts`) keyed by URL prefix.
  Unrecognized routes fall back to humanizing the first segment.
- **AC-4.** Vitest covers the page-name derivation helper:
  exact-match table-driven (each known route → known label), plus
  the unknown-route fallback case.
- **AC-5.** Playwright e2e: on `/audits`, breadcrumb reads
  `<tenant-name> › Audits`. On `/dashboard`, breadcrumb reads
  `<tenant-name> › Dashboard`.

## Constitutional invariants honored

- **Invariant 6 (tenant isolation).** Tenant name comes from the
  bearer-derived `/v1/me/tenants` read; no client-supplied tenant
  context.

## Canvas references

- `Plans/mockups/audits.html` lines 32-36 (the breadcrumb chip)
- Same chip on every other page mockup — `controls.html`,
  `evidence.html`, `risks.html`, `policies.html`, `dashboard.html`,
  etc.

## Dependencies

- **#213** (header chrome parity gap — spawner)
- **#192** (`/v1/me/tenants` — tenant name source) — `merged`

## Anti-criteria (P0 — block merge)

- **P0-271-1.** Does NOT add a new platform endpoint. Tenant name
  reuses `/v1/me/tenants`.
- **P0-271-2.** Does NOT make the breadcrumb segments clickable —
  v1 is a non-interactive label. Click-to-tenant-switcher is the
  TenantSwitcher's job (separate affordance, separate slice).
- **P0-271-3.** Does NOT hard-code page names in `topbar.tsx`. The
  route-to-name map lives in its own helper for test isolation.

## Skill mix (3-5)

1. Next.js App Router segment introspection
2. Tailwind / shadcn chrome styling
3. Vitest table-driven derivation tests
4. Playwright e2e assertion across two pages
