# 249 — Settings admin variants flicker between non-admin → admin on first paint

**Cluster:** Frontend
**Estimate:** 0.5d
**Type:** JUDGMENT
**Status:** `ready`
**Parent:** #204 (per-page UI parity audit fleet) — settings audit. Slice 154 audited section content; the admin-variant SSR/CSR mismatch surfaces when fetching `/settings` with an admin JWT and comparing SSR HTML to the post-hydration DOM. 154's findings did not include this.

## Narrative

`web/app/(authed)/settings/page.tsx:172-208` resolves `isAdmin` from
`useQuery(["settings-session-me"], getSessionMe)`. At SSR time the
query has not yet resolved, so `meQuery.data?.is_admin` is `undefined`
and the page ships the **non-admin** variant in the initial HTML:

1. Subhead renders `<span class="text-muted-foreground">Tenant
administration (admin role required)</span>` — a non-link muted
   text (line 195-197 of `page.tsx`).
2. API tokens card renders `data-testid="settings-section-tokens-non-admin"`
   with the alert "Admin role required. Issuing personal API tokens
   currently requires the admin role…" (line ~1003 of `page.tsx`).
3. `TenantSection` (admin-only) is not rendered (line 204:
   `{isAdmin ? <TenantSection /> : null}`).

When the page hydrates with the admin JWT, `/v1/me` resolves
`{ is_admin: true, tenant_role: "admin" }` and the page **swaps in
the admin variants**: subhead becomes a real link to `/admin`,
tokens-section becomes the full table affordance, `TenantSection`
appears.

**Verified live behavior** with `curl --cookie atlas_jwt=$JWT
https://atlas-edge.home.gmoney.sh/settings`:

- SSR HTML contains `data-testid="settings-section-tokens-non-admin"`
- `curl -sk https://atlas-edge.home.gmoney.sh/v1/me` returns
  `{ "is_admin": true, "tenant_role": "admin", ... }`

This is a layout-flicker / SSR-hydration mismatch:

- **Honest about role at SSR** — yes (it doesn't have role data, so
  it ships the safe default).
- **Honest about role post-hydration** — yes (it swaps to the right
  variant).
- **Honest about the user experience between those two moments** —
  **no** (the admin user sees "Admin role required" for ~50-200ms
  before the page corrects itself). For a primary-user-persona
  (solo security leader who IS admin) this is the modal experience.

**Three design options to consider:**

1. **Cookie-decode + server-component path:** decode the `atlas_jwt`
   cookie in the Server Component, embed `is_admin` into the initial
   tree directly. Eliminates the flicker entirely. ~30 LOC change in
   the page + cookie/JWT helper. Best UX. Requires the page to become
   a hybrid Server+Client component.
2. **Skeleton-until-hydrated:** show a skeleton placeholder for the
   subhead + tokens-section + tenant-section UNTIL `meQuery.data`
   resolves; then render the role-aware variant. Eliminates the
   incorrect-variant flicker; introduces a "loading state" flicker
   instead. ~15 LOC.
3. **Initial-data hydration via Next.js cache:** prefetch `/v1/me` in
   a server component above the page; pass the resolved data as
   `initialData` to `useQuery`. Eliminates the flicker without
   changing the page's client-component nature. ~25 LOC. Composes
   cleanly with slice 209 (local-credential-as-JWT) which already
   plumbs the credential context.

The engineer chooses + records in the decisions log. Default
recommendation: **(3) initialData prefetch** — composes with the
existing TanStack Query state, no Server Component refactor needed.

## Threat model

| STRIDE                | Threat                                                                                                                                                                                                                                                                                                                                               | Mitigation                                                                                                                                                                       |
| --------------------- | ---------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- | -------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- |
| **S** Spoofing        | If we decode the JWT in a Server Component (option 1), a tampered cookie could mis-set initial state.                                                                                                                                                                                                                                                | Server-side JWT verification (slice 187+) is the trust boundary; the client-side `/v1/me` re-fetch on hydration corrects any mismatch (the server `/v1/me` re-verifies the JWT). |
| **T** Tampering       | None new — the existing client-side flow already trusts `/v1/me` post-hydration.                                                                                                                                                                                                                                                                     | n/a                                                                                                                                                                              |
| **I** Info disclosure | A user who is admin briefly sees "Admin role required" — purely a UX gap, no info leaks.                                                                                                                                                                                                                                                             | n/a (this slice CLOSES the info-display gap).                                                                                                                                    |
| **E** EoP             | Could a non-admin user manipulate the initial-data to render the admin variant before `/v1/me` corrects? Yes — but the server-side authz gate on every `/v1/admin/*` API call remains the trust boundary; rendering admin UI chrome to a non-admin yields a 403 the moment the user clicks anything mutating. The chrome-flicker is purely cosmetic. | AC-5 below: the server-side `/v1/admin/credentials` gate (slice 062) is the security boundary; this slice does NOT change it.                                                    |

**Verdict.** Hide-admin-UI-fail-closed posture is preserved (the cookie/JWT-decode path verifies before rendering); chrome render is not the trust boundary.

## Acceptance criteria

- **AC-1.** Engineer picks option 1, 2, or 3 above and records the
  rationale + chosen path in
  `docs/audit-log/249-settings-admin-variants-flicker-decisions.md`.
- **AC-2.** With an admin JWT cookie, the SSR HTML of `/settings`
  contains the admin variant of the subhead (a link to `/admin`)
  OR a skeleton placeholder — but NEVER the literal text "Admin role
  required" / "admin role required".
- **AC-3.** With an admin JWT cookie, the SSR HTML of `/settings`
  contains either `data-testid="settings-section-tokens"` (admin
  variant) OR a skeleton placeholder — but NEVER
  `data-testid="settings-section-tokens-non-admin"`.
- **AC-4.** With a non-admin JWT cookie, the existing non-admin
  variants render correctly (no regression).
- **AC-5.** The server-side authz gate on `/v1/admin/credentials`
  (slice 062) is NOT touched. Verified by `git diff` not showing
  any change in `internal/api/admincreds/`.
- **AC-6.** Vitest regression: a render test exercises the admin
  - non-admin paths with a mocked `useQuery` initial-data
    (or whichever pattern the engineer picks); both paths assert the
    correct variant is in the first paint.
- **AC-7.** Playwright e2e expansion (settings.spec.ts): a new
  AC asserts that on first paint of `/settings` as admin, the
  string "Admin role required" is NOT present in the DOM.
- **AC-8.** No regression in 11/11 settings.spec.ts ACs from slice
  171's close-out.

## Constitutional invariants honored

- **Invariant 6 (RLS at the DB layer).** The server-side trust
  boundary is unchanged. UI chrome is not the access decision.
- **Slice 103 P0-A3 + P0-A4 (admin RBAC + no migration of /admin/\*
  pages).** Unchanged.
- **Slice 209 (local-credential-as-JWT)** — composes cleanly; the
  initial-data path consumes the same JWT this slice ships.

## Canvas references

- `Plans/canvas/12-ui-fill-in-design-decisions.md` §4 — settings
  is user-only; admin cross-link is conditional.
- `Plans/mockups/settings.html` lines 105-110 — subhead shows the
  admin link as a brand-colored anchor; never shows the non-admin
  fallback (the mockup represents the admin-user experience).

## Dependencies

- **#204** (this slice's parent — per-page UI parity audit fleet).
- **#103** (settings page initial slice — merged).
- **#062** (admin credentials API + authz gate — merged).
- **#187/#192** (auth-substrate-v2 / JWT path — merged).
- **#209** (local-credential-as-JWT) — composes; engineer picks
  whether to consume the JWT helper this slice ships or stays
  client-only.

## Anti-criteria (P0 — block merge)

- **P0-249-1.** Does NOT remove the client-side `/v1/me` re-fetch
  — the server-side response is still the source of truth post-
  hydration. The initial-data path is a hydration-priming
  optimization, not a replacement.
- **P0-249-2.** Does NOT change the server-side authz gate.
- **P0-249-3.** Does NOT fabricate a role-claim in the initial
  data if the JWT is absent / unverified — falls back to the
  non-admin variant (failing closed).
- **P0-249-4.** Does NOT cache `/v1/me` responses across users /
  sessions (privacy invariant).
- **P0-249-5.** Does NOT regress the 11/11 settings.spec.ts ACs
  (slice 171 close-out).

## Skill mix (3-5)

1. Next.js App Router — Server Component + initialData hydration
   pattern
2. TanStack Query — `initialData` + `staleTime` patterns for
   hydration-time fetch elision
3. JWT cookie decode helper (composes with slice 209)
4. Vitest mocked-query render test
5. Playwright SSR-content assertion (string-not-present pattern)
