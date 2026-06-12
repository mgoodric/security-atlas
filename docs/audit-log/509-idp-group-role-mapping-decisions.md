# Slice 509 — IdP group-to-role mapping — JUDGMENT decisions log

**Slice:** `docs/issues/509-idp-group-to-role-mapping.md`
**Type:** JUDGMENT (group→role mapping precedence + conflict resolution)
**Date:** 2026-06-12

This is the authorization-derivation sibling of slice 508: 508 provisions the
user, 509 maps their IdP groups to atlas roles. Because it ASSIGNS ROLES (the
thing 508 deliberately deferred per P0-508-3), the security model is the
load-bearing part. The subjective calls below are the mapping precedence, the
conflict resolution between manual and group-derived roles, the manual-survival
contract, and the last-admin guard.

**Detection-tier classification (slice 353 / Q-13):**

- `detection_tier_actual`: integration
- `detection_tier_target`: integration

One bug surfaced during the slice: the admin-CRUD HTTP integration test's first
draft used Go 1.22 `http.ServeMux` (`{id}` pattern) instead of a chi router, so
`chi.URLParam(r,"id")` resolved empty and Delete 400'd. Caught at the
integration tier (the test that drives the real handler), which is exactly where
a router-wiring mismatch should be caught — `actual == target`. No production or
unit gap; the pure-Go plan logic + the resolver behavior were correct first
pass.

---

## D1 — `idp_config_id` is NULLABLE; NULL = the SCIM / IdP-config-agnostic source

AC-6 keys the mapping by `(tenant, idp_config_id, group)` so a tenant with two
OIDC configs maps each IdP's groups independently. But the SCIM-Group source has
no `oidc_idp_configs` row — the SCIM push channel is the tenant's single
credential, not an OIDC relying-party config. Rather than invent a synthetic
"SCIM config" row, `idp_config_id` is **NULLABLE**: a NULL-source mapping is the
SCIM / IdP-agnostic one; a non-NULL value scopes the mapping to a specific OIDC
config. The resolver matches with `IS NOT DISTINCT FROM` so NULL only matches
NULL and a config only matches itself.

Consequence: PostgreSQL treats NULLs as distinct in a plain UNIQUE constraint,
which would let the SCIM source insert duplicate `(group, role)` rows. The unique
index therefore `COALESCE`s `idp_config_id` to the nil UUID so the SCIM source is
also de-duplicated and the mapping stays idempotent (AC-1) for both source kinds.

Rejected: a non-nullable `source TEXT` column plus a separate nullable
`idp_config_id`. It would split the lookup into two predicates and complicate the
`IS NOT DISTINCT FROM` scoping for no benefit — the nullable config id already
encodes "OIDC config X vs the SCIM channel" cleanly.

## D2 — `user_roles.origin` ('manual' | 'group-derived'); DEFAULT 'manual'

The manual-survival contract (AC-4) requires distinguishing a manual admin
assignment from a group-derived one. A single `origin TEXT NOT NULL DEFAULT
'manual'` column with a 2-value CHECK does it minimally. The DEFAULT is
load-bearing: every pre-existing `user_roles` row (slice 035/478 INSERT paths +
older integration fixtures) is implicitly `'manual'`, so the column lands without
a backfill and the existing manual-assignment surface (`adminusers/assign.go`)
keeps writing manual rows unchanged. Re-derivation only ever DELETEs/INSERTs
`origin='group-derived'` rows; the `DeleteGroupDerivedRole` query carries an
`AND origin='group-derived'` predicate as the safety belt so a manual row with
the same `(tenant, user, role)` is NEVER removed by a group re-derivation.

Rejected: a separate `user_group_roles` table. It would fork the role model into
two tables the authz middleware must union on every request — the hot RBAC path
reads `user_roles` once today, and adding a second table is a query-shape
regression for a distinction one column expresses.

## D3 — Precedence: manual + group-derived COEXIST; the union is the effective set

A user can hold a role manually AND have it group-derived; both rows coexist
(idempotent on the composite PK). The effective role set the authz layer reads is
the union across origins. This is simpler and safer than a "manual overrides
group" precedence: there is no role-level conflict because a role is either held
or not — the only thing `origin` controls is **who may revoke it**. Manual rows
are revoked only by an admin (the existing `adminusers/revoke` path); group-
derived rows are revoked only by re-derivation. The two revocation authorities
never touch each other's rows.

A user in MULTIPLE mapped groups gets the **union** of all mapped roles (AC-3) —
the resolver's `ResolveRolesForGroups` returns the DISTINCT role set across the
group list. An UNMAPPED group has no mapping row, so it contributes nothing
(fail-closed, P0-509-1) — the mapping table IS the allow-list.

## D4 — The pure-Go reconciliation plan is the heart; the resolver applies it

The precedence + conflict + guard JUDGMENT lives in a pure function
(`planReconcile`) with NO DB or I/O, exhaustively table-tested (11 cases mapping
to each AC/P0). The DB-backed `Resolver.Derive` loads the state (resolved target
set, current group-derived roles, tenant admin count, whether the user holds
admin manually), calls the pure plan, and applies grants/revokes + writes audit
rows in ONE transaction. This is the slice-353 Q-2 fast-loop convention: the
intricate decision logic is unit-tested in milliseconds; the integration tier
proves the wiring (RLS, real reconciliation, audit, cross-tenant) against real
Postgres.

ONE resolver, TWO sources (AC-2): both the OIDC-claim path and the SCIM-Group
path call `Resolver.Derive` with an already-validated group set. The only
difference is the `Source` label (recorded in the audit row) and the
`idp_config_id` scoping. The mapping logic cannot diverge because there is
exactly one resolver. An integration test (`TestDerive_BothSourcesIdenticalMapping`)
drives the same mapping from both sources and asserts identical derivation.

## D5 — Last-admin guard (AC-5 / P0-509-3): suppress, do not error

When a group re-derivation would revoke a user's group-derived `admin` role, the
guard fires only when ALL of: the role is `admin`, the user does NOT also hold
`admin` manually (a manual admin survives the re-derivation, so the tenant is not
stranded), and the tenant has at most one admin user (this user). In that case
the revoke is **suppressed** — the user keeps the group-derived admin role and
the result reports it in `SuppressedRevokes` for the audit trail — rather than
erroring the whole derivation. Suppressing (not failing) is the right call: a
group re-derivation is a background reconciliation triggered by an IdP membership
change; failing it would leave the user's OTHER role changes unapplied. Keeping
the last admin while applying everything else is the fail-safe outcome.

The guard reads `COUNT(DISTINCT user_id) WHERE role='admin'` (any origin), so a
second admin via ANY path (manual or group-derived, same or different user)
lifts the guard. `<= 1` is treated as stranding (the `0` case is defensive — it
should not occur if the user currently holds group-derived admin, but the
conservative branch is the safe one).

## D6 — P0-509-4: a mapping may only target an EXISTING canonical atlas role

Mappings never auto-create roles. The `oidc_idp_group_mappings.role` column
carries the SAME 5-role CHECK as `user_roles`, and the CRUD handler +
store both call `ValidateMappingRole` (→ `authz.IsCanonical`) BEFORE the INSERT
so a non-existent role is a clean 400, with the DB CHECK as the backstop. A group
named "SuperUsers" does not conjure a "superuser" role — it simply has no mapping
until an admin maps it to one of the five canonical roles.

## D7 — OIDC `groups` claim captured at the verified-token point (P0-509-2)

The validated-source contract is enforced at the one place the token is proven:
`oidc.HandleCallback` reads the `groups` claim into `CallbackResult.Groups` ONLY
after signature + issuer + audience + nonce verification. The resolver never
accepts raw group input — its `DeriveInput.Groups` is documented as "already
validated by the caller", and the only callers are the verified-JWT claim reader
and the authenticated SCIM-group handler. An integration test proves a
derivation only ever runs on mapped + validated groups; there is no code path
that maps an unvalidated group.

## Scope honesty — what this slice ships vs. defers

**Ships (proven):** the migration (mappings table + audit ledger + origin
column, four-policy FORCE RLS, reversible), the unified resolver (both sources,
fail-closed, union, manual-survival, last-admin guard, multi-IdP, audit), the
admin CRUD surface (AC-8, admin-gated, P0-509-4 role validation), and the OIDC
`groups`-claim capture at the verified-token point.

**Deferred wiring (named honestly):** the two SOURCE call sites that invoke the
resolver in the live login / SCIM-membership flows are thin adapters over the
proven resolver. The OIDC callback now CAPTURES the validated groups on
`CallbackResult`; calling `Resolver.Derive` from the session-mint handler after
the user upsert, and standing up a full SCIM `/Groups` REST resource (member
add/remove triggering re-derivation), are follow-on wiring that depend on the
merged 508 SCIM surface + the auth session-mint flow. They are deliberately not
built here to avoid destabilizing the just-merged 508 + auth-substrate surfaces.
The resolver — the load-bearing AC-2 deliverable — is complete and both source
paths are integration-proven to flow through it. See the PR body for the
remaining-wiring note.
