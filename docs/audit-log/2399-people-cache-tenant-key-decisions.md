# #2399 — people directory cache keyed by tenant: decisions

detection_tier_actual: manual_review
detection_tier_target: unit

Source: the re-review of #2383 (#2085). The browser `QueryClient` is a
tab singleton. #2383 dropped the people directory on a tenant switch and
on sign-out, but a session expiry, then a sign-in in the same tab
(server-action redirects, not full loads), drops nothing. Signing in to
another tenant could briefly show the previous tenant's names until the
re-fetch.

## D1 — the tenant comes from the session cookie, read on the server

`<AppShell>` (the chrome every authenticated tree renders through, #2080)
reads `atlas:current_tenant_id` off the `atlas_jwt` cookie and hands it
down through `<SessionTenantProvider>`. No new fetch: it is a cookie
read per server render. A tenant switch (`router.refresh()`) and a new
sign-in (a fresh server render of the layout) both hand down the new
tenant.

Rejected: `GET /api/me` from the browser. It is a new request on every
mount of a tree that renders `<Person>`, and the key is unknown until it
lands, so every name renders the loading state for one round trip.
Rejected: `/api/me/tenancy`, which carries the tenant count, not the
tenant ID.

The payload is decoded, not verified. The value only partitions the
browser cache; every fetch that fills an entry is a bearer call the
platform verifies and RLS scopes. A forged claim can only file the
caller's own data under a wrong key.

## D2 — unknown tenant fails closed

`peopleDirectoryQuery(undefined)` keys the entry `["people", ""]` and sets
`enabled: false`. Nothing is fetched, nothing cached for a real tenant is
read, and `<Person>` stays on its loading state (`…`). A cookie without a
UUID tenant claim, or a page outside `<AppShell>`, is unknown. Every
`atlas_jwt` the platform accepts carries the claim (jwtmw shape-checks a
JWT; `/v1/test/issue-jwt` sets it), so no current path stays on loading.

## D3 — the login page clears the whole client, not only the directory

The ticket asks for `forgetPeopleDirectory()` on login mount.
`<ForgetPreviousSession>` calls `getQueryClient().clear()` instead, a
superset: it drops the directory and every other query the previous
session cached, whether or not its key carries a tenant. The login page
holds no client queries of its own, so nothing on it is orphaned.

## D4 — cache sweep: other tenant data under tenant-less keys

Every other browser query key omits the tenant: about 100 keys, for
example `["risks"]`, `["policies"]`, `["controls-list", "tenant"]`,
`["vendors"]`, `["audits", "list"]`, `["frameworks"]`,
`["saved-views", …]`, `["admin", "users"]`, `["admin-creds"]`,
`["settings-*"]` and `["dashboard", …]`. Many hold names or titles.

| Path                                     | State after this PR                                                                                                                                                                                              |
| ---------------------------------------- | ---------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- |
| Expiry, then sign-in in the same tab     | Covered for every key: the login page clears the client (D3).                                                                                                                                                    |
| Sign-out, then sign-in                   | Covered for every key: sign-out lands on `/login`, which clears.                                                                                                                                                 |
| In-tab tenant switch / sibling broadcast | Only the people directory is dropped. The other keys keep the old tenant's data for up to `staleTime` (60 s default) and `gcTime` (5 min), and a page revisited in that window can render it before re-fetching. |

Re-keying those queries one by one is not cheap: roughly 100 call sites
across `app/` and `components/`, plus their `invalidateQueries`
partners. The proportionate fix is one call on the switch path
(`qc.resetQueries()` beside `forgetPeopleDirectory()` in
`tenant-switcher.tsx`, both branches), which resets every query and
re-fetches the mounted ones. It changes what the page shows during a
switch (every panel back to loading), so it belongs in its own ticket
with its own e2e check, not folded in here. To be filed.

## D5 — tests

- vitest, query-key level (`lib/people-cache.test.ts`): tenant A's
  directory cached, then a `QueryObserver` for tenant B. No emission from
  mount to settle carries A's data, and B is fetched once. With the tenant
  unknown, nothing cached is read and nothing is fetched. Both fail
  against the old `["people"]` key (checked by reverting the key).
- vitest (`lib/auth/session-tenant.test.ts`): the claim parser, and
  every malformed input as unknown.
- Playwright: not added. Reproducing the leak needs two tenants with
  local-password users and a sign-in through the form in one tab, with no
  full page load in between. The e2e seed provides one demo tenant, and
  `page.goto` is a full load, which resets the singleton and hides the
  bug. The query-key test reproduces the mechanism directly.

## Out of scope

`signOut` only deletes the cookie, with no server-side session
revocation. The dispatcher files it separately.
