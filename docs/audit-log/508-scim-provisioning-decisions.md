# Slice 508 — SCIM 2.0 user-lifecycle provisioning — JUDGMENT decisions log

**Slice:** `docs/issues/508-scim-user-lifecycle-provisioning.md`
**Type:** JUDGMENT (SCIM-attribute → atlas-user mapping)
**Date:** 2026-06-12

This slice implements RFC 7644 (you implement a spec, you do not invent endpoint
shapes). The subjective calls below are the SCIM-attribute mapping, the
bearer-scope isolation, and the deprovision semantics.

---

## D1 — SCIM `userName` maps to the atlas `email`; `displayName` → `display_name`; `externalId` → `scim_external_id`

The atlas `users` table has no separate `username` column — identity is
`(tenant_id, email)` (unique per tenant) plus the optional OIDC `(idp_issuer,
idp_subject)` pair. SCIM `userName` is the IdP's login identifier, which for
every credible IdP is the user's email/UPN. So `userName ↔ email` is the
natural, lossless mapping and makes a SCIM-provisioned row **reconcilable with a
later OIDC sign-in** (same email → same user). `displayName` (or
`name.formatted` when `displayName` is absent) maps to `display_name`;
`externalId` is stored in a new nullable `scim_external_id` (unique per tenant
when present) so the IdP can address the user by its own stable id.

Rejected: a dedicated `scim_user_name` column. It would duplicate `email` for no
benefit and split the OIDC-vs-SCIM identity, breaking the reconcile story.

`name` is read but only the `formatted` sub-attribute is honored (the platform
carries a single `display_name`, not given/family parts). `emails` on input is
ignored in favor of `userName` (they are the same value in practice); on output
the primary `work` email mirrors `userName` for IdP round-trip comfort.

## D2 — The SCIM credential is a SEPARATE table + credential type, not a flag on `api_keys` (P0-508-2)

The load-bearing security decision. `scim_credentials` is its own table and
`internal/scim.CredentialStore` its own store. The SCIM auth middleware
(`internal/api/scim.Middleware`) calls ONLY `CredentialStore.Authenticate`,
which returns a `scim.Credential` carrying nothing but `{id, tenant_id}` — no
`IsAdmin`, no roles, no scope predicate. There is **no code path** from a SCIM
token to a `credstore.Credential`, an atlas JWT, or a session.

The `/scim/v2/*` subtree is mounted OUTSIDE the `/v1` JWT + `requireCredential` +
authz + tenancy chain (the `/scim/` prefix is added to all three bypass lists)
and wrapped in its own `chi.Group` with the SCIM middleware. So a SCIM token
presented to `/v1/...` hits the `/v1` chain, which has no knowledge of
`scim_credentials` and 401s; and an atlas JWT presented to `/scim/...` is
ignored by the SCIM middleware (which only reads the `Bearer` and looks it up in
`scim_credentials`). The two credential domains are disjoint by construction.

Rejected: reusing `api_keys` with a `scim` scope flag. A flag is one boolean
away from a privilege-escalation bug (a mis-set `is_admin` on a "SCIM" key would
be catastrophic); a separate table + separate store makes the blast-radius
containment structural, not a runtime check.

## D3 — Deprovision = soft-disable + session-revoke, never hard-delete (P0-508-1 / AC-4)

`active=false` (Patch or Replace) and `DELETE` both: set `users.active=false`,
mirror `status='disabled'`, and revoke **every** active session for the user
(`RevokeAllSCIMUserSessions`) in the SAME transaction as the audit write. The
row is retained so the actor's historical evidence/decision records survive
(invariant #2 — the append-only ledger references the user as actor). DELETE
returns 204 but is semantically a deprovision, NOT a row removal.

`users.active` (boolean) is a NEW column kept in lockstep with the pre-existing
`users.status` text enum (`active`/`disabled`). Two representations is mild
redundancy, but: SCIM speaks `active` (boolean) natively, and the rest of the
platform already gates on `status`. Keeping both in lockstep (the SCIM store
sets them together) means SCIM deprovision flows through the existing
`status='disabled'` access checks with zero changes to the `/v1` surface. A
future cleanup could collapse them, but that is a cross-cutting refactor out of
this slice's scope.

## D4 — SCIM is identity + `active` ONLY; roles are dropped, never rejected (P0-508-3 / AC-7)

`planPatch` walks the PatchOp operations and extracts ONLY `active` and
`displayName`. A `roles`/`groups`/unknown path (including a URN-qualified one
like `...:User:roles`) is **silently skipped** — not an error. A no-path replace
value object reads only `active`/`displayName` keys and ignores the rest. The
SCIM wire `User` struct has no `roles` field at all, so a role can neither be
read in nor written out.

Drop-not-reject is deliberate: an IdP that sends a roles op (some do, by
default) should still successfully provision the identity, not get a 400 that
breaks the whole sync. The roles op is a no-op; role assignment remains a slice
509 / manual-admin concern. The unit test (`planPatch` drops roles) and the
integration test (`user_roles` count stays 0 after a roles patch) both pin this.

## D5 — RLS confinement; the tenant comes FROM authentication, never the body (P0-508-4 / AC-6)

The SCIM credential's `tenant_id` is what `Authenticate` RETURNS (the lookup is
by token hash under the BYPASSRLS auth pool, before any tenant is known — same
pattern as `apikeystore`). The middleware then sets `app.current_tenant` from
that returned tenant, and every provisioning query runs `tenancy.ApplyTenant`
under it. There is no `tenant_id` field on any SCIM request. A cross-tenant Get
returns SCIM 404 with the SAME body as a genuine not-found (no oracle
distinguishing "exists in another tenant" from "does not exist"), because RLS
makes the cross-tenant row invisible at the DB layer.

## D6 — Discovery endpoints are authenticated (not anonymous)

RFC 7644 §4 permits `ServiceProviderConfig` / `ResourceTypes` / `Schemas` to be
served anonymously. We require the SCIM bearer anyway: an IdP always holds the
token before it probes, and requiring it stops an unauthenticated party from
fingerprinting the deployment's SCIM capabilities. Low cost, small hardening
win.

## D7 — Filter support is `userName eq "x"` only; anything else is 400 invalidFilter

The AC-1 minimum. An unsupported filter returns 400 `invalidFilter` rather than
silently falling back to the full tenant list — silently returning everything on
an unrecognized filter is an information-disclosure footgun (STRIDE-I). The full
SCIM filter grammar (and the `Group` resource) is genuinely out of scope; it is
slice 509's concern (or a later spillover) and is NOT dropped from any AC here.

## Scope honored / spillover

All seven ACs (AC-1..7) and all four P0s (P0-508-1..4) are met as a coherent
whole. Group→role mapping and the SCIM `Group` resource are the explicitly
deferred slice-509 scope and were not touched. No new spillover slice was
required — the AC-required filter minimum (`userName eq`) is implemented; the
broader grammar is already owned by 509. No AC was under-delivered.

## Detection-tier classification (slice 353 Q-13)

- **detection_tier_actual:** `none` — no bug surfaced during the slice. The
  SCIM "omitted `active` = enabled" JSON gotcha was caught at design time (the
  inbound type uses `*bool` to distinguish absent from explicit-false) before it
  could become a runtime defect, so it never manifested as a caught bug.
- **detection_tier_target:** `unit` — had the active-presence gotcha slipped, it
  would (and does) get caught by the `inboundUser.activeOrDefault` /
  presence-decode unit test. The security P0s are pinned at `integration`
  (cross-tenant RLS, deprovision+session-revoke, no-role-escalation) because
  they require real Postgres + RLS; the no-role-escalation invariant is ALSO
  pinned at `unit` (`planPatch`) for fast-loop regression coverage.
