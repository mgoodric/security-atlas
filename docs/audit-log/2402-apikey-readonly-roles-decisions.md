# 2402 — Viewer and auditor API keys stop deriving control_owner: decisions log

Issue: mgoodric/security-atlas#2402, from #2381's D3 (PR #2390). An API key
carries `api_keys.owner_roles` into the credential's `OwnerRoles`, and
`authz.derivedRolesFor` mapped any non-empty `OwnerRoles` to
`control_owner`. So a key minted with `owner_roles = {viewer}` or
`{auditor}` could read and write evidence, artifacts and walkthroughs.
#2381 closed this for JWT sessions only.

## Detection-tier classification

- **detection_tier_actual:** manual_review. Found while building #2381,
  where it was left out of scope (its D3).
- **detection_tier_target:** integration.
  `TestDecide2402_APIKeyRoleMatrix` (real Postgres) and
  `TestBuildInput2381_DerivedRoles` both fail on the old derivation and pass
  with the fix. The red run printed `0 principals narrowed`.

## Decisions

### D1: an unbound key derives exactly its label (Matt's call, 2026-11-16)

A key with no owner (`issued_by` NULL; its `UserID` is the key id) whose
`owner_roles` names only `viewer` and/or `auditor` now derives exactly those
roles. Such a key has no live `user_roles` to fall back on, so deriving
nothing (as #2381 does for JWTs) would leave it with no access at all.

This is not a pure narrowing. The matrix shows three reads that the label's
own role grants and `control_owner` did not:

| Key label | Gains (GET only)                        |
| --------- | --------------------------------------- |
| `viewer`  | `/v1/risks`                             |
| `auditor` | `/v1/dashboard`, `/v1/me/audit-periods` |

`/v1/me/audit-periods` returns an empty list for an unbound key, which has
no `auditor_assignments`. The alternative was for these keys to derive
nothing, a pure narrowing, but every existing viewer or auditor key would
then answer 403 everywhere. Matt chose label mapping (option A). The matrix
allows a gain only when it is a GET that the label's role grants on its own,
and never a write.

### D2: a bound key derives nothing and holds its owner's live roles (Matt's call, 2026-11-16)

A key with an owner (`issued_by` set; its `UserID` is the owner's uuid)
whose label names only `viewer` and/or `auditor` derives no credential role.
Its roles are the owner's live `user_roles` rows, which the resolver already
merged in `Engine.Decide`. This matches #2381 for JWT sessions and #2239 for
live roles: an owner whose roles are revoked, or whose `users` row is no
longer active, leaves the key with nothing. Owners keep what their own live
roles grant. A viewer key owned by an admin still acts as admin, exactly as
it did before. The matrix shows this is a pure narrowing.

Intersecting the label with the owner's roles was not done. The previous
derivation was a union too, and an intersection is not something a role
set can express in the current input shape.

### D3: what keeps the old derivation

- `is_admin` and `is_approver` keys: their cases come first and are
  unchanged.
- A label that names anything besides `viewer`/`auditor` (`control_owner`,
  `grc_engineer`, `admin`, or a slice-011 owner-role label such as
  `platform-eng`) keeps `control_owner`. Owner-role labels are how a
  control's `owner_role` matches the credential.
- A key with an empty label keeps `grc_engineer` (that is #2401's ticket).
- JWT sessions and OAuth clients: unchanged here.

The key case keys on the apikeystore credential ID shape (`key_` + row id),
the same way #2381 keys on jwtmw's `jwt:` + jti.

### D4: the #2381 matrix hands its key rows to the #2402 matrix

`TestDecide2381_RoleMatrixPerPrincipalKind` asserted that viewer and auditor
keys were unchanged, which is now false by design. Their rows move into
`TestDecide2402_APIKeyRoleMatrix`, which also covers a bound key whose owner
holds the label's role, one whose owner holds no role, one whose owner is
admin, mixed labels, admin and approver keys, and a JWT control row. No
rego changed, so `internal/authz/rego_bundle/` and `policies/authz/` are
untouched.

## What each key loses (from the matrix)

Every viewer/auditor-only key loses `POST /v1/evidence:push`,
`POST /v1/artifacts:upload`, `POST /v1/walkthroughs` and `POST /v1/evidence`,
plus `GET /v1/evidence` and `GET /v1/evidence/{id}`. Unless its owner's live
roles grant them, it also loses:

- viewer (unbound, or bound with a viewer owner): `GET /v1/artifacts/{id}`,
  `GET /v1/walkthroughs`.
- bound, owner with no live role: every route `control_owner` gave
  (controls, policies, framework scopes, calendar, evidence freshness, and
  the above).

## Rollout

`scripts/dry-run-2402-key-roles.sql` is read-only and prints counts only.
Per tenant, it shows live keys whose derivation changes, by binding and
label. It shows the bound ones by what the owner's live roles leave them
(`none` / `read-only` / `writes`), and the live keys that do not change.
A key that needs to keep writing has to be re-minted with a `control_owner`
label, or its owner has to be granted the role, before rollout. There is no
live install today. Matt approves before merge.
