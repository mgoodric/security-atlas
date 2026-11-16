# 2381 — Viewer and auditor JWTs stop deriving control_owner: decisions log

Issue: mgoodric/security-atlas#2381. `jwtmw` copies the JWT's role claim for
the current tenant into the synthesized credential's `OwnerRoles`, and
`authz.derivedRolesFor` mapped any non-empty `OwnerRoles` to `control_owner`.
So every signed-in viewer and auditor was evaluated as `control_owner` and
could read and write evidence, artifacts and walkthroughs. #2239 (PR #2373)
made the live `user_roles` rows apply to `user:<uuid>` subjects, but as a
union with the derived role, so it did not close this (its D5 says so).

## Detection-tier classification

- **detection_tier_actual:** manual_review. Found while building #2375
  (PR #2379), reading how the payload gate's `BuildInput` derives roles.
- **detection_tier_target:** integration.
  `TestJWTReadOnlyRoles2381_WritesRefused` (HTTP, real Postgres) and
  `TestDecide2381_RoleMatrixPerPrincipalKind` fail on the old derivation and
  pass with the fix.

## Decisions

### D1 — a viewer/auditor-only claim derives nothing, not its own roles

A JWT session whose role claim names only `viewer` and/or `auditor` derives
no credential role. Its roles are the live `user_roles` rows the resolver
merges in `Engine.Decide`.

Mapping the claim to `viewer` / `auditor` verbatim was the alternative. It
was rejected because the claim is a mint-time snapshot: after a revoke, the
token keeps the claim until it expires. Today such a token derives
`control_owner`; a verbatim mapping would hand it `auditor` instead, which
reaches routes `control_owner` never did (`GET /v1/me/audit-periods`, audit
notes). `TestPeriodAuditors2239_RealLoginAndLeftoverAssignment` pins that a
revoked auditor gets 403 there. Deriving nothing makes the fix a pure
narrowing, which the matrix test asserts.

### D2 — admin, grc_engineer and control_owner claims are unchanged

A claim containing `admin`, `grc_engineer` or `control_owner` (alone or with
viewer/auditor) still derives `control_owner`, exactly as before. The live
`user_roles` rows add the real role on top. In the Rego, `control_owner`'s
grants are a subset of `grc_engineer`'s, which are a subset of `admin`'s, so
these users lose nothing they are granted, and they gain nothing: the
derivation is identical. Mapping `admin` to `admin` from the claim was not
done for the same stale-claim reason as D1.

A claim holding a slice-011 owner-role label (anything outside the five
canonical roles) also keeps `control_owner`: those labels are how a control's
`owner_role` matches the credential.

### D3 — JWT sessions only; API keys keep their derivation

The fix keys on the jwtmw credential ID shape (`jwt:` + jti). API keys carry
`OwnerRoles` from the `api_keys.owner_roles` column, and a key minted with
`owner_roles = {viewer}` still derives `control_owner`. That is the same
shape of bug for keys, but the ticket is scoped to JWT sessions and keys
have no live role row to fall back on (an unbound key would be left with
nothing). Left for a follow-up ticket rather than widened here.

### D4 — the unified-log payload gate follows the routes

`adminauditlog`'s payload gate builds its input with `BuildInput`, so a
viewer or auditor no longer sees `evidence` and `walkthrough` payloads in
the unified log just because `control_owner` reads them. The integration
test's role model (`keeps`) now drops the control_owner union for viewer
and auditor. `TestKindSourceResource_CoversEveryKind` (grc_engineer probe)
is unchanged and green.

### D5 — overlap with #2157 (auditor ABAC rules never match)

Not fixed here. An auditor session now reaches OPA with `auditor` only
(from `user_roles`), no longer with `control_owner` beside it. Any auditor
read that worked only through the derived `control_owner` (evidence,
artifacts, framework scopes) now depends on the auditor rules, which is the
surface #2157 says never matches its ABAC predicate. #2157 may see auditor
reads that used to pass now fail; that is this fix working, and the
auditor rules are where it is repaired.

## Rollout

`scripts/dry-run-2381-roles.sql` is read-only and prints counts only: per
tenant, active users whose effective role set loses `control_owner`, the
users unchanged, and viewer/auditor-only users assigned as a control's owner
(who would lose writes on their control). Matt approves before rollout.
