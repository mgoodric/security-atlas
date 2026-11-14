# ADR 0023 — Single-Tenant Experience by Default, Multi-Tenant as a One-Way Opt-In

**Status:** Accepted

**Date:** 2026-11-13

**Decides:** how an install presents tenancy to its operators and users, and how an install becomes multi-tenant.

**Spec:** [#1758 — single-tenant install as the default experience](https://github.com/mgoodric/security-atlas/issues/1758). Tracking issue for the adopter first-run findings: [#1715](https://github.com/mgoodric/security-atlas/issues/1715).

**Keeps and completes:** [OQ #13](../../Plans/canvas/11-open-questions.md) (solo-operator vs multi-tenant tension, resolved 2026-05-11).

**Shipped in this change (docs only):** this ADR, the OQ #13 pointer, the README "Who it's for" and feature-list wording, and the `GLOSSARY.md` entries for Tenant, Single-tenant install, Multi-tenant install and Bootstrap tenant. The behaviour lands in the follow-on tickets under #1758.

---

## Context

OQ #13 resolved to build multi-tenant from day one: the solo operator runs a single-tenant deployment of the multi-tenant system, and "the UI MAY hide tenant chrome when `tenant_count == 1`, but the data model and authz never branch on tenant count." The foundation held: every organisation-data table has row-level security, and isolation is checked in CI and by ISA probes.

The second half, hiding the tenant chrome, was never finished. Almost every adopter runs security-atlas for one organisation and still meets tenancy at every edge:

- local and OIDC sign-in need a `tenant_id`, and `/login` loses its email/password form after the first install (#1756, #1727);
- OAuth client-credentials clients write into the all-zeros tenant, so uploaded controls are invisible (#1739);
- six CLI commands ask for a tenant UUID;
- the admin UI shows Tenants and Super-admins pages and asks for a tenant UUID when assigning users;
- the first OIDC login and the Docker seed can each create a different "default" tenant.

Separately, some admin endpoints that change global catalog data are gated only by tenant admin (#1757). That is harmless with one tenant and a cross-tenant write with several.

## Decision

1. **The single-tenant install is the default experience.** With one tenant, every edge (sign-in, tokens, CLI, UI, demo data, docs) resolves to the **bootstrap tenant** implicitly. Nobody types or sees a tenant ID. User-facing copy calls the tenant "your organisation".
2. **Tenant count, not a mode flag, drives the experience.** There is no tenancy-mode setting. The UI shows tenant chrome (switcher, Tenants page, Super-admins, tenant pickers) only when two or more tenants exist, and the CLI and sign-in require an explicit tenant only then.
3. **The data model, row-level security and authorisation never branch on mode or tenant count.** `tenant_id` columns and RLS are unchanged. Database-enforced isolation stays on for a single-tenant install, as defence in depth.
4. **Multi-tenant is opt-in.** Creating a second tenant requires a super-admin **and** the deployment setting `ATLAS_ALLOW_MULTI_TENANT=true`. Installs that already hold several tenants are unaffected.
5. **The opt-in is one-way.** There is no path from multi-tenant back to single-tenant; tenants are not merged or collapsed.
6. **Global catalog writes require super-admin.** Rows with no tenant (crosswalk edge tiers, global framework versions) are not organisation data and are not editable as such, even on a single-tenant install. On a single-tenant install the bootstrap admin holds super-admin, so this does not lock the operator out.
7. **Writes from a credential carrying no tenant (the all-zeros tenant) are refused.** OAuth clients are bound to a tenant when they are created, defaulting to the bootstrap tenant.

## Alternatives considered

| Alternative                                          | Why rejected                                                                                                                                                                                              |
| ---------------------------------------------------- | --------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- |
| A tenancy-mode configuration flag (`solo` / `multi`) | A second source of truth that can disagree with the data. OQ #13 already chose tenant count; a flag would let authorisation branch on mode, which OQ #13 forbids.                                         |
| Map the all-zeros tenant to the bootstrap tenant     | It would silently widen what machine tokens can do: every unbound client would gain write access to the organisation. Binding clients explicitly, and refusing all-zeros writes, keeps the grant visible. |
| Drop or relax RLS and `tenant_id` for one tenant     | Breaks invariant 6 and removes defence in depth for the deployment shape most adopters run.                                                                                                               |
| Allow converting multi-tenant back to single-tenant  | Merging or dropping tenants risks destroying or co-mingling organisation data; no adopter has asked for it.                                                                                               |

## Consequences

- Single-tenant operators never handle a tenant UUID; the follow-on tickets under #1758 own the sign-in, token, CLI, UI, demo-seed and docs changes.
- Test fixtures that need a second tenant (RLS integration tests, tenant-switch e2e specs, the ISA tenant-isolation probe) create it with `ATLAS_ALLOW_MULTI_TENANT=true`.
- Existing multi-tenant installs carry on unchanged, except that unbound OAuth clients there keep read access and have writes refused until bound to a tenant.
- MCP tenant resolution is unchanged; it already uses the bearer credential's tenant.
