# 672 — Policy detail link 404s — `/policies/{id}` route does not exist

**Cluster:** Policies
**Estimate:** S-M (0.5-1.5d)
**Type:** JUDGMENT (build the detail route vs remove the link for v1)
**Status:** `ready` — surfaced by the 2026-06-10 demo-tenant UI audit (ATLAS-024).

## Narrative

Policy titles in the library (`/policies`, 5 seeded policies) are links to `/policies/{id}`,
but that route returns a **hard Next.js 404** ("This page could not be found"). Re-verified
on `main` build `2a3805b`. Orchestrator-confirmed: only `web/app/(authed)/policies/page.tsx`
exists — there is **no `[id]/page.tsx`** — so every policy title is a clickable-but-broken
link. Secondary: the 404 renders with **no app shell/nav** (only browser-back recovers),
stranding the user.

## Threat model

No new data surface. If a detail route is built, it must be RLS-tenant-scoped (read the
tenant's policy) and read-only unless a deliberate edit surface is in scope.

## Acceptance criteria

- [ ] **AC-1.** JUDGMENT (decisions log): either (a) build a read-only `/policies/{id}` detail
      route (title, body/summary, version, owner, acknowledgment status), or (b) remove the
      link (render the title as non-link text) until the detail surface ships. Default lean:
      (a) a minimal read-only detail page — the seeded policies have content to show.
- [ ] **AC-2.** No clickable-but-broken link remains: every policy-title affordance either
      navigates to a working page or is not a link.
- [ ] **AC-3.** The app 404 page renders **within the app shell/nav** (a stranded
      shell-less 404 is itself a bug) — fix the not-found boundary so navigation is recoverable.
- [ ] **AC-4.** Playwright: clicking a seeded policy title reaches a 200 page (or asserts the
      title is non-interactive if (b)); a genuinely-missing id renders the in-shell 404.

## Anti-criteria

- Does NOT add policy editing/authoring (read-only detail only, unless explicitly scoped).
- Does NOT leave the shell-less full-page 404 in place.

## Dependencies

- `web/app/(authed)/policies` + the policies read API (`internal/api/policies`).
- The app-level `not-found` boundary (`web/app/.../not-found.tsx`).

## Notes

Source: 2026-06-10 demo-tenant audit, item **ATLAS-024** (high/major). Re-tested open on
`2a3805b`. Note: ATLAS-017 (slice 670) flagged "future slice" copy on policies — this slice
resolves whether policy detail genuinely ships or the link goes away.
