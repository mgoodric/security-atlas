# 272 — Global search box (`⌘K` modal) in shared shell

**Cluster:** frontend + backend
**Estimate:** 3.0d
**Type:** STANDARD

**Status:** `merged` — shipped by slice 223 (UI) on top of slice 268 (endpoint),
with slice 361 (combobox ARIA) and slice 661 (SCF-anchor index) layered on.
Reconciled 2026-07-25 (OE-398); the `not-ready` gate below is discharged —
slice 268 merged 2026-05-23 (#593, d9d8e69b).

## Reconciliation (2026-07-25, OE-398)

This slice was filed as a spillover placeholder and its `not-ready` marker
outlived the dependency it was waiting on. The audit below records what is
actually on `main`, so the doc stops describing work that already shipped.

### Is this the same work as slice 228?

**Yes — one piece of work, two descriptions from opposite ends.** Slice 228
described the gap from the UI end ("the topbar has no search input"); this
slice described it from the shape end ("the ⌘K surface is a multi-day
feature, not chrome"). Both name the same missing capability (cross-entity
search over controls / evidence / risks reachable by ⌘K from the shared
shell) and both name the same blocker (no unified search endpoint). Neither
should be built again. Slice 228 is closed as superseded; its one distinct
remainder is stated in its own doc.

### What search the API supports today

| Surface               | Status            | Notes                                                                          |
| --------------------- | ----------------- | ------------------------------------------------------------------------------ |
| `GET /v1/search`      | ships (slice 268) | `internal/api/search/search.go`. The cross-entity endpoint. RLS-scoped.        |
| — `controls` type     | ships             | ILIKE over `controls.title` + `.description`, active versions only.            |
| — `risks` type        | ships             | ILIKE over `risks.title` + `.description`. Slice 268 added it; none existed.   |
| — `evidence` type     | ships             | ILIKE over `evidence_records.evidence_kind` + `.control_ref`.                  |
| — `anchors` type      | ships (slice 661) | Bundled SCF catalog; tenant-agnostic by construction (no `tenant_id`, no RLS). |
| `GET /v1/controls?q=` | ships (slice 064) | Per-primitive. NOT used by the ⌘K surface (P0-272-2).                          |
| `GET /v1/evidence?q=` | ships (slice 050) | Per-primitive. NOT used by the ⌘K surface (P0-272-2).                          |
| BFF `GET /api/search` | ships (slice 223) | `web/app/api/search/route.ts`. Thin proxy; forwards the bearer, not a tenant.  |

So step 3 of the OE-398 brief ("if no cross-entity search exists, design the
endpoint contract first") is moot: the endpoint exists, its contract is
documented in the `internal/api/search` package doc, and the UI consumes it.

### Shape divergence: popover, not modal (resolved — keep the shipped shape)

AC-2 below specified a **centered modal**. Slice 223 shipped an
**always-visible topbar input with a dropdown popover** instead. The shipped
shape is kept, deliberately:

1. It is what slice 228 AC-1 asked for (right-pinned 256px input + `⌘K`
   badge), and 228 is the surface-parity slice this one reconciles with.
   Converting to a modal would regress 228's stated criterion.
2. Slice 361's WCAG 4.1.2 combobox ARIA wiring was designed and audited
   against the input+listbox shape. A modal rewrite would invalidate that
   audit and the Path-1 `<Link>`-keep decision behind it.
3. The substance AC-2 was protecting — ⌘K reaches a real search that
   searches real data and navigates to a real record — is met. The modal was
   a means, not the requirement.

The mockups are archived and per-page divergence from `web/` is expected, not
drift (slice 437), so the mockup's shape is not itself an argument for the
modal.

### Anti-criteria check

- **P0-272-1** (no stub search): honored. The surface queries `/v1/search`;
  no hardcoded or "coming soon" results exist anywhere in the path.
- **P0-272-2** (no bypass of `/v1/search`): honored, and pinned by the
  `P0-223-1` Playwright assertion plus the BFF vitest URL assertion.
- **P0-272-3** (no free-text in a URL path segment): honored. The BFF reads
  `req.url`'s search params and forwards them as a query string to
  `/v1/search?…`; the query never becomes a path segment.

### Where each acceptance criterion landed

| AC              | Landed in                                                                          |
| --------------- | ---------------------------------------------------------------------------------- |
| AC-1            | `web/components/shell/topbar.tsx` renders `<GlobalSearch />` (slice 223)           |
| AC-2 (⌘K)       | `isShortcutTrigger` + `document` keydown listener; grouped result list             |
| AC-2 (modal)    | **diverged** — popover, not modal. See above.                                      |
| AC-3            | 250ms debounce (`DEBOUNCE_MS`) against BFF `/api/search`                           |
| AC-4            | Arrow keys + Enter (`router.push`) + Esc in `onKeyDown`                            |
| AC-5            | "No matches" zero-state; popover only opens at ≥2 chars                            |
| AC-X (security) | `web/app/api/search/route.test.ts` — query string, never a path segment            |
| AC-6            | `web/e2e/controls-top-bar.spec.ts` — open, type, Enter-navigate AND click-navigate |

The click-navigate leg of AC-6 was the one genuine gap and was added under
OE-398; the keyboard leg alone left the `<Link>` path unpinned.

## Narrative

Spillover from slice 213. The audits mockup at
`Plans/mockups/audits.html` (lines 42-46) shows a global search input
in the topbar — placeholder copy `"Search controls, evidence,
risks…"`, a `⌘K` kbd hint, focus-state styling matching the
brand-color picker. Slice 213 deferred this element because:

1. Global search is not a chrome decoration — it's a substantive
   product feature that requires platform-side work (a unified
   search endpoint or a per-primitive fan-out + ranking layer).
2. The cmd-K modal pattern (Linear / Stripe / Vercel) is a load-
   bearing UX surface; shipping a stub would be worse than no
   surface, and a real implementation is multi-day.
3. The slice 268 backend endpoint (`POST /v1/search`) is filed but
   not yet ready — this slice DEPENDS on 268 landing first. Marked
   `not-ready` until 268 ships.

## Threat model

**S — Spoofing.** Search must run inside the bearer-derived tenant
context — operator A must never see operator B's results across
tenants. The slice 268 endpoint is tenant-scoped via RLS (same
envelope as every other read); this UI is a thin client on top.

**T — Tampering.** Cmd-K modal accepts free-text input; the
endpoint must treat it as untrusted (parameterize the LIKE / FTS
query). v1 of slice 268 will document the query-shape constraints;
this UI inherits them.

**Verdict.** **mitigations-required.** Spell out in AC-X that the
query string is treated as untrusted and the BFF does NOT
interpolate it into an URL path segment (only into a JSON body /
query string).

## Acceptance criteria

- **AC-1.** A `<GlobalSearch />` component renders in the shared
  topbar between the in-progress pill and the user avatar.
- **AC-2.** A `⌘K` keyboard shortcut (or `Ctrl+K` on non-mac) opens
  a modal centered on the viewport showing the search input + a
  result list grouped by primitive (Controls, Evidence, Risks,
  Policies, Audits, Vendors).
- **AC-3.** The modal queries the slice 268 `POST /v1/search`
  endpoint via the BFF (`/api/search`) on every keystroke after a
  150 ms debounce. Results are TanStack Query-cached by query
  string.
- **AC-4.** Up/down arrow keys navigate results; Enter routes to
  the selected row's detail page; Esc closes the modal.
- **AC-5.** Empty-state copy: "Type to search — controls, evidence,
  risks, policies, audits, vendors". Zero-result copy: "No matches".
- **AC-X (security).** The free-text query is treated as untrusted.
  The BFF forwards it as a JSON body field, never an URL segment.
  Vitest pins this contract.
- **AC-6.** Playwright e2e: open the modal via the kbd shortcut,
  type a known seeded substring, assert the matching row is
  rendered + clickable.

## Constitutional invariants honored

- **Invariant 6 (tenant isolation).** Search results are tenant-
  scoped by the upstream platform via RLS. The BFF forwards the
  bearer; no client-supplied tenant context.

## Canvas references

- `Plans/mockups/audits.html` lines 42-46 (the search input + kbd)
- Same input on every other page mockup — global affordance.

## Dependencies

- **#213** (header chrome parity gap — spawner)
- **#268** (`POST /v1/search` unified-search endpoint) — `not-ready`.
  Blocks this slice until merged.

## Anti-criteria (P0 — block merge)

- **P0-272-1.** Does NOT ship a stub search (a modal that opens but
  returns "search coming soon"). Either ship the real flow or wait.
- **P0-272-2.** Does NOT bypass the `POST /v1/search` endpoint —
  the UI does NOT directly call per-primitive list endpoints with
  ad-hoc query parameters.
- **P0-272-3.** Does NOT interpolate the free-text query into a URL
  path segment. JSON body or `?q=` query string only.

## Skill mix (3-5)

1. Next.js App Router + shadcn modal (`Dialog`)
2. TanStack Query debounced fetch
3. Keyboard event handling (cross-platform kbd shortcut)
4. Playwright e2e with keyboard input simulation
5. Threat-model-aware string handling at the BFF boundary
