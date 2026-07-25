# Slice 733 — live group-role derivation wiring + SCIM /Groups — JUDGMENT decisions log

**Slice:** `docs/issues/733-idp-group-role-live-wiring-scim-groups.md`
**Type:** JUDGMENT (session-mint integration point + SCIM Group resource/membership shape)
**Date:** 2026-06-12

This is the runtime-integration completion of the IdP-provisioning line: slice
508 provisions SCIM users, slice 509 shipped the **proven** group-to-role
derivation engine, and this slice wires that **same** engine into the two
runtime integration points 509 deferred (the live OIDC session-mint `Derive`
call + the SCIM `/Groups` resource). The defining constraint — and the source of
every subjective call below — is that the slice **REUSES** the slice-509
resolver and re-implements **no** mapping/derivation logic (P0-733-1). The
JUDGMENT calls are: where in the login flow to invoke `Derive`, the SCIM Group
resource + membership table shape, and how to feed a SCIM membership change back
into the resolver.

**Detection-tier classification (slice 353 / Q-13):**

- `detection_tier_actual`: integration
- `detection_tier_target`: integration

One bug surfaced during the slice — a filtered-member PATCH path
(`members[value eq "x"]`) returned 400 in the first integration-test draft. The
**root cause was the test, not the handler**: the test built the PatchOp body
with `fmt.Sprintf("...%q...")`, which emitted an unescaped inner quote inside a
JSON string and made the request body invalid JSON, so the handler's
`decodeSCIMBody` correctly 400'd. Fixed by building the path string via
`json.Marshal` (correct escaping), exactly as a real IdP sends it. Caught at the
integration tier (the test that drives the real handler against Postgres) —
`actual == target`. No production or unit gap; the `PlanGroupPatch` member-op
parsing was correct first pass (its pure-Go unit test passed throughout).

---

## D1 — Session-mint integration point: invoke `Derive` in the OIDC callback, after user upsert, before session create

AC-1 requires the live OIDC login to reconcile group-derived roles from the
validated `groups` claim. The exact insertion point matters for both
correctness and security:

- **After ID-token validation (P0-733-2).** `oidc.HandleCallback` already
  validates signature + issuer + audience + nonce and surfaces the validated
  claim on `CallbackResult.Groups` (slice 509). The `Derive` call sits in the
  HTTP handler strictly after that returns, so only validated claims ever feed
  derivation. A forged/failed callback returns at the validation guard, well
  before the derive step — proven by `TestOIDCCallback_ForgedTokenDoesNotDerive`
  (zero `Derive` calls on a CSRF-tripped flow).
- **After the user upsert, before `sessions.Create`.** The user row must exist
  (both the bootstrap-first-install branch and the standard upsert branch
  resolve `usr` first) so the derived `user_roles` rows key on a real user. We
  derive **before** minting the session so the session reflects current
  membership; a derivation error fails the login cleanly (500) rather than
  stranding a half-provisioned session.
- **`usr.ID.String()` is the resolver `UserID`.** `user_roles.user_id` is TEXT
  (slice 018) and the bootstrap path already inserts `userID.String()`; the
  derive call uses the same text form so it reconciles the same rows.

The call is made through an **injected `OIDCGroupDeriver` interface**, not a
direct `grouprole` import, so `internal/api/auth` does not import `grouprole`
(no cycle, and the wiring stays explicit). `cmd/atlas` attaches the concrete
adapter (`oidcGroupDeriver`) over a real `grouprole.NewResolver(pool)`. When no
deriver is attached the login keeps its pre-733 behavior (opt-in; never a
silent regression).

## D2 — Surface the login's `idp_config_id` on `CallbackResult` for AC-6 fidelity

509's resolver matches mappings with `idp_config_id IS NOT DISTINCT FROM`, so a
tenant with two OIDC configs maps each IdP's groups independently (509 AC-6). To
preserve that on the live path, the OIDC callback must pass the **specific**
config the login flowed through. `HandleCallback` already resolves that config
(`cfg`) internally but did not expose its id. This slice adds
`CallbackResult.IDPConfigID = cfg.ID`, and the handler passes it into
`DeriveOnLogin`. `uuid.Nil` (e.g. the local-mode resolver) falls back to the
NULL-source mappings, which is the correct degenerate behavior.

## D3 — SCIM Group resource: identity + membership ONLY; no role attribute anywhere (P0-733-3)

A SCIM Group's job here is to record **who is in the group**, never **what role
that confers**. The role is derived exclusively by the 509 resolver against the
mapping table (the allow-list). To make P0-733-3 **structurally** impossible to
violate:

- The wire `Group` struct (`internal/scim/groups.go`) has **no** role/roles
  field. A unit test round-trips a wire Group through JSON and asserts no
  `role`/`roles` key exists.
- `GroupPatchIntent` (the pure PATCH planner output) has **no** role field, so a
  `roles` op in a PatchOp value object is silently dropped — there is nowhere
  for it to land (mirrors slice 508's `planPatch` allow-list for users).
- The discovery `Schemas` Group document advertises only `displayName`,
  `externalId`, and `members`.

## D4 — Membership store shape: `scim_groups` + `scim_group_members`, `group_ref` snapshotted

The membership edge lives in a separate `scim_group_members` table (rather than
a JSONB array on `scim_groups`) so the resolver-feeding read — "every group a
user is in" — is an indexed join, and so a single-member add/remove is a single
row op. Two shape decisions:

- **`user_id` is TEXT**, matching `user_roles.user_id` (slice 018) — the exact
  value the resolver derives roles for. No UUID round-trip.
- **`group_ref` is snapshotted at membership-write time** as the value the
  resolver matches mappings against: the group's `externalId` when present, else
  its `displayName` (`DomainGroup.GroupRef()`). Snapshotting means a later
  re-derivation reuses the exact identifier the `oidc_idp_group_mappings` table
  is keyed on, without re-resolving it. Both tables are under four-policy FORCE
  RLS (invariant #6); both are NEW so no fixture backfill is needed.

`DeleteGroup` soft-disables (`active=false`) and clears membership, retaining
the row (invariant #2), mirroring slice 508's user soft-delete.

## D5 — Re-derivation on membership change: gather the user's FULL group set, call `Derive` (AC-3 / P0-733-1)

When a SCIM membership op changes who is in a group, the affected users' roles
must reconcile. The handler does **not** compute roles — it computes the set of
**affected users** (added ∪ removed, via `symmetricDiff` for wholesale
replaces; the added/removed ids for incremental adds/removes) and, for each,
gathers the user's **FULL current validated group set** (`ListGroupRefsForUser`
— every active group they remain a member of) and calls `Resolver.Derive` with
`Source=SCIM`, `IDPConfigID=Nil`. Passing the full set (not just the changed
group) is what lets the resolver reconcile correctly: removing a user from one
group leaves their roles from their other groups intact, and the resolver
revokes only the roles no longer backed by any group. The resolver's fail-closed
behavior, last-admin guard, no-auto-create, and manual-role preservation all
hold unchanged because they live inside the reused resolver. A
`recordingDeriver` unit test asserts the handler feeds the resolver the
store-provided group set (it did not invent roles — P0-733-1); the integration
suite proves the end-to-end grant + revoke against real Postgres.

The `RoleDeriver` is injected as an interface (the cmd-level `scimGroupDeriver`
adapter maps it to the concrete `grouprole.Resolver`), so `internal/api/scim`
carries no `grouprole` import and the "reuse, don't re-author" boundary is
explicit in the type system.

## D6 — `/Groups` rides the slice-508 SCIM router + credential + RLS unchanged (AC-2 / P0-733-4)

The Group routes mount inside the **same** `root.Group` subtree that the 508
`/Users` routes use, wrapped by the **same** `scimapi.Middleware(scimCredStore)`
— so the per-tenant SCIM credential authenticates them and the tenant RLS
context is set identically. No new auth surface, no new credential type. A
Tenant-A credential getting a 404 (not 403, no oracle) on a Tenant-B group is
proven by a two-harness integration test against real Postgres (P0-733-4). The
`/scim/` prefix is already bypassed by the `/v1` JWT/authz/tenancy chain (508),
so a SCIM token still cannot reach a `/v1` handler.

## D7 — openapi drift: `/scim/v2/Groups` is correctly out of the drift-check scope

`scripts/check-openapi-drift.sh` discovers chi route registrations matching the
`/v1/|/auth/|/health` prefixes only; the SCIM resource routes (`/scim/v2/...`)
are deliberately outside that set, exactly as slice 508's `/Users` routes are.
No `RouteSpecs`/`docs/openapi.yaml` change is required, and the drift check
passes clean (253 routes documented). Confirmed locally.
