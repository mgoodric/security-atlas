# 272 — Global search box (`⌘K` modal) in shared shell

**Cluster:** frontend + backend
**Estimate:** 3.0d
**Type:** STANDARD

**Status:** `not-ready`

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
