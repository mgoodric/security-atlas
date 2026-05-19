# 143 — Create-tenant flow (super_admin-gated)

**Cluster:** Backend / Frontend / Multi-tenancy
**Estimate:** 1d
**Type:** AFK
**Status:** `not-ready`

## Narrative

Surfaced 2026-05-18 via `/idea-to-slice` as a sibling spillover of slice 141 (multi-tenant login) + slice 142 (super_admin role).

vCISO with multiple clients needs to provision a new tenant for each new client. Today the only tenant-create path is the slice-141 bootstrap (first-install OIDC sign-in) — there's no post-bootstrap way to add another tenant short of running SQL directly.

**What this slice ships:**

- NEW endpoint `POST /v1/admin/tenants` — super_admin-gated; body `{name, slug?, creator_joins_as?: 'admin'|'none'}`; creates new tenant + writes 1 row to `tenants` + 0 or 1 row to `user_tenants` (if creator opts to join as admin) + 0 or 1 row to `user_roles` (matching admin role).
- NEW management page `web/app/admin/tenants/page.tsx` — list current tenants (super_admin only); "Create New Tenant" form (name + optional slug + checkbox "Join as admin").
- BFF route `web/app/api/admin/tenants/route.ts`.
- Seed minimal scope cells / default framework subscriptions in new tenants per slice 002 + canvas §5 conventions (e.g. one default scope cell "All").
- Audit-log integration via slice 124 unified aggregator: new `kind='tenant_create'`.

**Scope discipline (what is OUT):**

- **Tenant deletion** — out of scope; future slice with retention policy + data-purge design.
- **Bulk tenant import** (CSV → N tenants) — out of scope.
- **Tenant cloning / templating** — out of scope; new tenants start empty.
- **Tenant-level resource quotas** (max users, max controls, etc.) — out of scope.
- **Billing / per-tenant pricing tier metadata** — out of scope.

## Threat model

Inherits slice 141 + 142. Create-specific additions:

| STRIDE                       | Threat                                                                                                                                                                  | Mitigation                                                                                                                                                                                                                                                 |
| ---------------------------- | ----------------------------------------------------------------------------------------------------------------------------------------------------------------------- | ---------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- |
| **T** Tampering              | Slug injection — caller passes `slug` containing special chars that confuse downstream URL building / DNS lookup (if per-tenant URL routing ever ships).                | Strict slug regex: `^[a-z0-9][a-z0-9-]{0,62}$`. Server rejects 400 otherwise. Documented in OpenAPI spec.                                                                                                                                                  |
| **D** DoS                    | Super_admin (or compromised super_admin) creates 10,000 phantom tenants → DB bloat + `user_tenants` enumeration slowdowns + admin UI freezes.                           | Soft rate-limit: max 100 tenants per super_admin per day (enforced via `super_admin_audit_log` count over rolling window). Configurable via env. NOT a hard ceiling on platform tenant count.                                                              |
| **E** Elevation of privilege | Super_admin opts to "Join as admin" → self-grants admin in the new tenant + writes `user_tenants` + `user_roles` rows. Could be misused to land in N tenants invisibly. | Per-tenant audit-log entry written for the user_roles INSERT (slice 035 already enforces); `super_admin_audit_log` records the tenant_create. Both surface in slice 124 unified aggregator. The audit trail is the deterrent; no programmatic block at v1. |

## Acceptance criteria (stub — expand at pickup)

- [ ] AC-1: `POST /v1/admin/tenants` handler; super_admin-gated; body validation (slug regex; name nonempty; creator_joins_as enum).
- [ ] AC-2: Atomic transaction: INSERT tenant + (conditional) INSERT user_tenants + INSERT user_roles + INSERT super_admin_audit_log row (`kind=tenant_create`).
- [ ] AC-3: Seed default scope cell "All" + default framework subscriptions per slice 002 conventions.
- [ ] AC-4: Soft rate-limit (max 100 tenants / super_admin / day; 429 with Retry-After).
- [ ] AC-5: BFF route + management page (list + create form + result modal).
- [ ] AC-6: Slice 124 unified audit-log aggregator extension: new `kind='tenant_create'`.
- [ ] AC-7: Slug uniqueness enforced at schema level (`UNIQUE (slug)` on `tenants`); 409 on conflict.
- [ ] AC-8: Cross-tenant test (creating Tenant B as super_admin doesn't grant access to Tenant A's data unless creator_joins_as also asserts in A).
- [ ] AC-9: Playwright e2e on `/admin/tenants` page.
- [ ] AC-10: CHANGELOG entry.

## Constitutional invariants honored

Inherits slice 141 + 142. Adds: **#5 FrameworkScope intersection** — new tenant gets default framework subscriptions per canvas §5 conventions.

## Canvas references

- `Plans/canvas/05-scopes.md` — scope-cell model; new tenants start with default scope.
- `Plans/canvas/02-primitives.md` — tenants primitive shape.

## Dependencies

- **#141** Multi-tenant login (merged) — extends `tenants` write surface.
- **#142** super_admin role (merged) — gates create.
- **#124** Unified audit-log aggregator (merged) — new `kind='tenant_create'`.
- **#002** Schema + migrations (merged) — `tenants` table.

## Anti-criteria (P0 — block merge)

- Inherits slice 141 + 142 anti-criteria.
- **P0-CT-1** Slug regex strict (`^[a-z0-9][a-z0-9-]{0,62}$`); 400 otherwise.
- **P0-CT-2** Soft rate-limit ≤100 tenants / super_admin / day; 429 with Retry-After.
- **P0-CT-3** Atomic transaction for all 4-5 writes; partial state on failure forbidden.
- **P0-CT-4** NO tenant deletion endpoint in this slice (out of scope).
- **P0-CT-5** NO bulk import.
- **P0-CT-6** NO vendor-prefixed test fixture tokens.

## Skill mix

- slice 141 + 142 packages (consume).
- Go integration tests + Playwright e2e.

## Notes for the implementing agent

Slice 142 must be merged before this picks up (super_admin role gate). Pickup time ~1d.

Provenance: filed 2026-05-18 via `/idea-to-slice` as a sibling spillover of slice 141.
