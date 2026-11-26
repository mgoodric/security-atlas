# #1764 — UI chrome follows tenant count: decisions log

detection_tier_actual: none
detection_tier_target: none

## D1 — The signal is read client-side, through a BFF

`GET /v1/me/tenancy` (#1763) reaches the browser through a new
`/api/me/tenancy` BFF route, and `lib/tenancy.ts` reads it with TanStack
Query. The admin section nav moved out of the `/admin` server layout into a
client component (`components/admin/admin-section-nav.tsx`) so that the
Tenants and Super admins links can follow it. A server-side probe would
avoid a brief render of the links, but the shared e2e database holds
however many tenants earlier specs seeded, and only a browser-side fetch
can be pinned with `page.route`. The single- and two-tenant e2e cases
depend on that.

## D2 — Unknown shows the chrome

While the signal is loading, or when the probe fails, the UI renders as it
did before #1764. Hiding the chrome on a multi-tenant install is the worse
mistake, and the failure then looks like the old UI rather than a
different one. On a single-tenant install the Tenants and Super admins
links can therefore flash for a moment on first load.

## D3 — What "tenant chrome" covers on the users page

With one tenant, `/admin/users` drops the Tenant column, the
cross-tenant / tenant-scoped banner, the "Add me to a tenant" button and
the tenant field in the assign dialog. The button and dialog read "Add
user". `tenantFieldMode` gains a `sole` mode that posts the session tenant,
falling back to the one listed tenant. The session tenant is the sole
tenant on a single-tenant install. The server is still the authority on
the assignment.

## D4 — Settings card

The Settings Tenant card is titled "Organisation" and hides the tenant ID
row with one tenant. The bundle gate-mode copy says "organisation". The
testids are unchanged so existing specs keep working. The `/settings` page
header ("Tenant administration") is server-prefetched and was left as it
is. The `/admin/tenants` and `/admin/super-admins` pages stay reachable by
URL, since hiding a link is presentation, not authorization.
