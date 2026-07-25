# 733 — Live group-role derivation wiring + SCIM /Groups REST resource

**Cluster:** Auth
**Estimate:** M (1-2d)
**Type:** JUDGMENT (session-mint integration point + SCIM Group resource shape)
**Status:** `ready`

## Narrative

Slice 509 (`closes 509`, merged `2a6297a7`) shipped the **complete IdP group-to-role
derivation engine**: the `oidc_idp_group_mappings` table, the pure-Go reconciliation
planner (precedence / conflict / last-admin guard), the DB-backed `grouprole.Resolver`
(`Derive`), the admin-only mapping CRUD surface, the validated OIDC `groups`-claim
capture, append-only audit, and four-policy RLS — every AC and P0 proven against real
Postgres. What 509 deliberately did **not** wire (flagged in its report as "thin
follow-on adapters over the proven resolver, not blockers") are the two **runtime
integration points** that call into that proven engine:

1. **Live session-mint `Resolver.Derive` call.** Today `Derive` is exercised by tests
   but is not yet invoked on the live OIDC login / session-mint path, so an
   OIDC-claim-derived role does not yet flow end-to-end at runtime. This slice wires
   `Derive` into the session-mint flow (after token validation — P0-509-2 stays intact:
   only validated claims feed derivation) so a login reconciles the user's
   `origin='group-derived'` roles from their current group membership.

2. **Full SCIM `/scim/v2/Groups` REST resource.** Slice 508 shipped `/scim/v2/Users`;
   the SCIM `Group` resource was deferred. 509's resolver already accepts a
   SCIM-group-derived source; this slice adds the `/scim/v2/Groups` SCIM endpoints
   (Create / Get / List / Patch / Delete per RFC 7644) that populate the group
   membership 509 maps, scoped by the same per-tenant SCIM credential + RLS as 508.

Surfaced during slice 509, captured as a follow-up per continuous-batch policy.

## What ships

- Wire `grouprole.Resolver.Derive` into the OIDC session-mint path (post-validation),
  reconciling group-derived roles on login; behind the existing per-tenant IdP config.
- `/scim/v2/Groups` SCIM service-provider resource (RFC 7644), mounted on the slice-508
  SCIM router (outside the `/v1` chain, per-tenant SCIM credential, RLS-confined).
- Tests: an integration test proving a login derives roles end-to-end through the live
  path; SCIM `/Groups` CRUD + cross-tenant RLS tests (mirror slice 508's `/Users` suite).
  Hold the `internal/auth/*` + `internal/api/scim/*` coverage floors.

## Acceptance criteria

- [ ] **AC-1.** An OIDC login invokes `grouprole.Resolver.Derive` after token
      validation; the user's `origin='group-derived'` roles reflect current group
      membership; `origin='manual'` roles are untouched (509 AC-4 preserved at runtime).
- [ ] **AC-2.** `/scim/v2/Groups` supports Create / Get / List / Patch / Delete per RFC
      7644, authenticated by the per-tenant SCIM credential (508), RLS-confined.
- [ ] **AC-3.** A SCIM group membership change drives a re-derivation through the 509
      resolver (no new mapping logic — reuse `Derive`).
- [ ] **AC-4.** P0-509-1..4 remain intact at runtime (fail-closed, validated-source-only,
      last-admin guard, no auto-role-create); cross-tenant isolation proven.

## Dependencies

- **#509** (IdP group-to-role mapping — the resolver + mappings + CRUD) — `merged`.
- **#508** (SCIM 2.0 Users + the SCIM router/credential this extends) — `merged`.
- **#187-#192** (auth-substrate-v2: session mint) — `merged`.

## Anti-criteria (P0)

- **P0-733-1.** Does NOT re-implement the derivation logic — reuses `grouprole.Resolver`.
- **P0-733-2.** Does NOT derive from an unvalidated token/group source (509 P0-509-2).
- **P0-733-3.** Does NOT let `/scim/v2/Groups` escalate a role outside the 509 mapping.
- **P0-733-4.** Does NOT read/mutate across tenants (RLS-confined).
