# #1756 — /login keeps email/password sign-in after first install

detection_tier_actual: manual_review
detection_tier_target: playwright

The server half (`POST /auth/local/login` without `tenant_id` resolves the only
tenant, 400 on several) landed with #1907. This change is the `/login` half.

## Decisions

- **D1 — install-state reports `single_tenant`, not a tenant id.** The boolean
  comes from `platform.Status.TenantCount` (all-zeros row excluded), at every
  point in the install's life. `tenant_id` is still returned only during first
  install, as before. A failed count logs and reports `false`, which makes
  `/login` ask for the tenant; sign-in still works.
- **D2 — the email/password card always renders.** `loginTenantMode`
  (`web/lib/auth/login-tenant.ts`) picks how it names the tenant: a hidden
  field during first install, nothing on a single-tenant install, and a
  visible Tenant ID field otherwise. When install-state cannot be read, the
  field is shown but optional, since the server may still resolve one tenant.
- **D3 — the multi-tenant field takes a tenant ID, not a slug.** The login
  endpoint accepts a UUID only, and adding slug lookup is outside this ticket.
  A slug picker belongs to the single-tenant spec (#1758) if it is wanted.
- **D4 — the login page fetches install-state with `no-store`.** The previous
  5s revalidation could serve a first-install answer after the marker flipped,
  or hide the tenant field after a second tenant appeared.
- **D5 — the bearer-paste card is always titled "Developer sign-in"**, and
  errors from either form render in the email/password card, now that card is
  always present.
- **D6 — the e2e user is made at run time.** A random password is hashed by
  `atlas-cli bootstrap hash-password` and inserted with psql; no credential is
  committed. The shared e2e database may hold one or several tenants when the
  spec runs, so the spec fills the Tenant ID field only when the page shows it.
  The single-tenant 200 / multi-tenant 400 split is pinned by the API
  integration tests from #1907.
