# 674 — Dashboard/breadcrumb shows "Default Tenant" while the active tenant is "Demo Demo"

**Cluster:** Dashboard / multi-tenant
**Estimate:** S (0.5-1d)
**Type:** AFK
**Status:** `ready` — surfaced by the 2026-06-10 demo-tenant UI audit (ATLAS-027).

## Narrative

With the tenant-switcher and Settings both showing **"Demo Demo"** (tenant id
`ad0e6b3c-…`), the dashboard Program header (`web/app/(authed)/dashboard/page.tsx:100`,
"the H1 row carries the active tenant name", slice 229) and the breadcrumb still render
**"Default Tenant"**. Re-verified on `main` build `2a3805b`. The dashboard resolves the
tenant name from the wrong source — it does not reflect the **currently-switched**
`current_tenant_id`; the switcher + `/v1/me/tenants` name enrichment have the right name, so
the H1/breadcrumb are reading a stale/origin tenant name instead.

This surfaced now because tenant-switching only became reachable after slice #1245 (the
self-assign actor fix) — so the post-switch name-resolution path was effectively untested.

## Threat model

Display-only, but it's a **multi-tenant correctness** issue — the wrong tenant label on the
primary screen risks operating in the wrong tenant. No data/scope change; the fix routes the
name from the active tenant consistently.

## Acceptance criteria

- [ ] **AC-1.** After switching tenants, the dashboard Program H1 AND the breadcrumb render
      the **active** tenant's name (matching the switcher + Settings), not "Default Tenant"
      or the origin tenant.
- [ ] **AC-2.** Identify the source of truth (the switched `current_tenant_id` → tenant name
      via the same `/v1/me/tenants` enrichment the switcher uses) and route the H1/breadcrumb
      through it.
- [ ] **AC-3.** Audit other surfaces that render a tenant name (settings header, any "Program ·
      X") for the same stale-source bug; fix consistently.
- [ ] **AC-4.** Playwright (multi-tenant): switch tenant → assert the dashboard H1 +
      breadcrumb update to the new tenant name.

## Anti-criteria

- Does NOT change tenant-switch semantics or RLS scoping — name display only.

## Dependencies

- `web/app/(authed)/dashboard` (slice 229 H1) + the breadcrumb (`web/components/shell/breadcrumb.tsx`) + `/v1/me/tenants` name enrichment (slice 192).
- Surfaced post-#1245 (tenant-switch now reachable).

## Notes

Source: 2026-06-10 demo-tenant audit, item **ATLAS-027** (medium/major). Re-tested open on `2a3805b`.
