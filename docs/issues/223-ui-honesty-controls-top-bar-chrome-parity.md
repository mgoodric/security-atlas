# 223 — UI honesty: controls top bar omits breadcrumb, search, audit banner, avatar

**Cluster:** Quality / UI hygiene (frontend)
**Estimate:** 1d (search input is the substantive piece; the others are presentational)
**Type:** JUDGMENT
**Status:** `merged` (status reconciled 2026-06-03 — backlog drained per \_STATUS.md SoR; loop terminated batch 184)

## Narrative

Surfaced during slice 204 audit fleet (controls page), captured as
follow-up per continuous-batch policy. The mockup at
`Plans/mockups/controls.html` (lines 23-54, `TOP BAR` block) shows the
top bar carrying:

- Tenant breadcrumb chip: `Sentinel Labs > Controls`.
- Global search input (placeholder `Search controls, evidence, risks…`,
  `⌘K` kbd hint, right-aligned).
- Audit-in-progress banner pill (amber, pulsing dot, text `SOC 2 Type
II · Q2 2026 in progress`).
- User avatar circle + display name.

The live `/controls` page (`web/app/(authed)/controls/page.tsx`
consumes `<ListPage>`; the chrome is rendered by the authed-layout
header at `web/app/(authed)/layout.tsx`) shows:

- Logo + `security-atlas` wordmark + `v0 · self-host` chip.
- TenantSwitcher component (slice 192) — a switcher, not the
  breadcrumb chip the mockup promises.
- `Sign out` button.

No global search input, no audit-in-progress banner, no avatar.

The largest material gap is the search input. The mockup signals
global search on every page; the live header has none. The other
three differences are presentational and could be deferred or
walked back in the mockup.

The slice's value: either ship the global-search affordance + the
audit-in-progress banner (the two affordances with real product
value), or update the mockup to reflect the deferred-indefinitely
scope. Default to ship-the-search; the AC shape below assumes that
path.

## Threat model

| STRIDE                | Threat                                                                                                                               | Mitigation                                                                                                                                                                                                    |
| --------------------- | ------------------------------------------------------------------------------------------------------------------------------------ | ------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- |
| **S** Spoofing        | Global search could leak cross-tenant results.                                                                                       | The search BFF MUST run under the caller's tenant context (cookie → bearer → RLS). No tenant override. Per CLAUDE.md invariant #6.                                                                            |
| **T** Tampering       | None — search is a read path.                                                                                                        | n/a                                                                                                                                                                                                           |
| **R** Repudiation     | None — read path.                                                                                                                    | n/a                                                                                                                                                                                                           |
| **I** Info disclosure | Search results page could surface evidence captions the user cannot read in a detail view (e.g. due to FrameworkScope intersection). | AC-5: search result rows respect the SAME applicability + framework-scope intersection that the detail-view authz checks. Reuse the existing detail-view query path; do not write a new "search RLS" surface. |
| **D** DoS             | Unbounded query.                                                                                                                     | AC-6: search input debounced (≥250ms); upstream search endpoint enforces a hard `LIMIT 50` cap.                                                                                                               |
| **E** EoP             | None — search is gated on the same JWT the page already validates.                                                                   | n/a                                                                                                                                                                                                           |

**Verdict.** `mitigations-required`. The Spoofing + Info-disclosure
items are real but the existing RLS plumbing handles them; the search
backend just needs to use it.

## Acceptance criteria

- **AC-1.** Global search input lands in the authed-layout header
  (`web/app/(authed)/layout.tsx`), visible on every authed page —
  not just `/controls`. Placeholder text: `Search controls, evidence,
risks…`. `⌘K` keyboard shortcut focuses the input.
- **AC-2.** Search BFF endpoint at `web/app/api/search/route.ts`
  forwards the bearer cookie to a new upstream `GET /v1/search?q=...`
  endpoint. The upstream endpoint queries SCF anchors + tenant
  controls + evidence + risks via existing sqlc query paths; RLS
  enforces tenant isolation at the database layer.
- **AC-3.** Search results render in a popover below the input
  (shadcn/ui `<Command>` pattern), grouped by entity type (Controls
  / Evidence / Risks). Each result links to the entity's detail page.
- **AC-4.** Search supports keyboard navigation: arrows move
  selection, Enter follows the link, Esc closes the popover.
- **AC-5.** Result rows respect applicability + framework-scope
  intersection — i.e. the SAME predicate the detail page enforces.
  No "search bypasses authz" surface.
- **AC-6.** Search input is debounced (250ms) and the upstream query
  is capped at `LIMIT 50` rows.
- **AC-7.** Tenant breadcrumb chip ("Sentinel Labs > Controls")
  rendered in the authed-layout header — value comes from the
  current tenant context + current pathname. Distinct from the
  TenantSwitcher (the switcher is a control; the breadcrumb is
  read-only wayfinding).
- **AC-8.** Audit-in-progress banner pill rendered in the
  authed-layout header when an active AuditPeriod exists for the
  current tenant. Hidden otherwise. Sources `audit_periods` table
  (slice 015) filtered by `status='in_progress'`.
- **AC-9.** Banner pill text follows the mockup's shape:
  `<framework_id> <type> · <quarter> in progress` (e.g. `SOC 2 Type
II · Q2 2026 in progress`).
- **AC-10.** Vitest unit coverage for the search BFF route handler
  (cookie → upstream forwarding, error paths). Playwright e2e spec
  asserts the search input focuses on ⌘K, types a query, and
  navigates to a control detail page.
- **AC-11.** Per-slice docs: `docs/audit-log/223-controls-top-bar-decisions.md`
  capturing (D1) why the search backend is a separate endpoint
  rather than re-using `/v1/anchors`; (D2) the popover-vs-page
  choice for search results; (D3) banner-pill hide-when-no-audit
  policy; (D4) CI-delta scan results.
- **AC-12.** Pre-commit clean, DCO sign-off, Co-Authored-By trailer
  per CLAUDE.md.

## Constitutional invariants honored

- **Invariant 6 (tenant isolation via RLS).** Search BFF forwards
  bearer cookie; upstream query runs under tenant context; RLS
  enforces isolation. No "search-bypasses-RLS" anti-pattern.
- **Invariant 5 (FrameworkScope intersection).** Search results
  respect the same `effective_scope(control, framework) =
applicability_expr ∩ framework_scope.predicate` predicate the detail
  view enforces.
- **Anti-pattern rejected.** "Vanity trust centers" — chrome that
  promises features (search) the platform does not back is the bug
  this slice fixes.

## Canvas references

- `Plans/canvas/05-scopes.md` §5.5 — FrameworkScope intersection
- `Plans/canvas/08-audit-workflow.md` — AuditPeriod lifecycle
  (banner-pill source)
- `Plans/mockups/controls.html` lines 23-54 — top bar promise
- `docs/audit-log/204-page-audit-controls.md` — parent audit

## Dependencies

- **#204** (UI parity audit fleet) — parent. This slice is one of
  five spillovers from the controls-page audit.
- **#015** (AuditPeriod table + lifecycle) — merged. Source of
  banner pill state.
- **#192** (TenantSwitcher) — merged. The breadcrumb chip
  coexists with the switcher.

## Anti-criteria (P0 — block merge)

- **P0-223-1.** Does NOT bypass RLS for search results. Every result
  row's visibility is checked through the existing detail-view
  authz path — no new "search authz" surface.
- **P0-223-2.** Does NOT ship a search UI without backing query
  capability — no placeholder input that no-ops. If the upstream
  endpoint is deferred, the input is hidden.
- **P0-223-3.** Does NOT touch the slice 204 audit harness or
  manifest.
- **P0-223-4.** Does NOT introduce a separate "search service" — the
  upstream query runs in the same atlas process, same `internal/api/`
  handler shape as other read endpoints.
- **P0-223-5.** Does NOT commit any vendor-prefixed test fixture
  tokens; neutral `test-*` only.

## Skill mix (3-5)

1. Next.js App Router + shadcn/ui — header layout + popover.
2. sqlc + RLS-aware Go handler — upstream search endpoint.
3. Playwright (slice 069 functional flow) — ⌘K focus + navigation.
4. Vitest — BFF route handler unit coverage.
