# #2541 — a bound key's canonical-role label follows its owner's live roles — decisions log

**Ticket:** https://github.com/mgoodric/security-atlas/issues/2541 (follow-ups
from the reviews of #2530 / #2523 D2 and #2527 / #2150)
**Shape:** for an API key bound to a user, a label that names a canonical role
(`admin`, `grc_engineer`, `control_owner`, `auditor`, `viewer`) counts only
through the owner's live `user_roles`. Free-text labels still count, and
unbound keys don't change. There are two helpers in `internal/authz/roles.go`:
`boundKey` and `isCanonicalRole`.

detection_tier_actual: manual_review
detection_tier_target: unit

(Found reading #2530 in review. The gate-level unit tables in
`internal/authz` are where it belongs and where it is now pinned.)

## D1 — which gates change

Two kinds of gate counted a bound key's label as a grant:

| Helper                                                        | Before                                                          | After                                                                                      |
| ------------------------------------------------------------- | --------------------------------------------------------------- | ------------------------------------------------------------------------------------------ |
| `AckRoleLabels`                                               | live roles + every label, unless a user's JWT session           | the same, except a bound key's canonical-role labels are dropped                           |
| `IsTenantAdmin` / `IsTenantApprover` / `IsTenantControlOwner` | live role, or the matching label unless the credential is a JWT | live role, or the matching label only when the credential is neither a JWT nor a bound key |

`TenantRoleLabels` already had these semantics (#2505) and is unchanged.

The second row is in scope because the CSF edit gate is no longer
`HasOwnerRole`. #2540 moved it to `IsTenantApprover`, which counted a bound
key's `grc_engineer` label. Changing only `AckRoleLabels` would have fixed
attest and left CSF edit open, which was the second half of the ticket.

## D2 — call sites

`AckRoleLabels`:

- `internal/authz/roles.go` `HasOwnerRole`, which is what
  `internal/api/controls/attest.go` calls. Line 424 sets `CallerCanAttest`
  on the control read, and line 515 is the attest write gate.
- `internal/api/policyacks/handlers.go:378`: the policy-ack role match.
- `internal/api/needsattention/handler.go:168`: policies the caller owes an
  ack.

`IsTenantAdmin` / `IsTenantApprover` / `IsTenantControlOwner` are the
tenant-scoped handler gates listed in `gate_sites_2447_test.go` and the
#2428 agreement table. The CSF edit gate is
`internal/api/csfassessment/handler.go` `editContext`. Each one now refuses a
bound key whose label is the only source of the role.

## D3 — "bound" excludes an empty UserID

`boundKey(cred) = apiKey(cred) && cred.UserID != "" && cred.UserID != cred.ID`.
A `key_` credential with no UserID names no user, so it has no `user_roles` to
fall back on and its label stays its grant. This matches how the existing
`TestTenantGates2428_Helpers` rows treat `{ID: "key_8", OwnerRoles: [admin]}`,
so no unbound key loses access. `roleLabelIsGrant`, which `TenantRoleLabels`
uses, treats an empty UserID as bound. Nothing in production mints that
shape: apikeystore stamps UserID as the owner's uuid or the key's own id. I
left `roleLabelIsGrant` alone to keep this change narrow.

## D4 — fail closed on the live-role lookup

No new lookup is added. The owner's live roles are `cred.TenantRoles`, which
authzmw stamps from the decision's resolver call. If that call errors,
`engine.Decide` returns it and authzmw answers 503 (dependency outage) or 500
before any handler runs (`internal/api/authzmw/middleware.go`). So a gate never
sees a credential whose live roles could not be read. When the owner holds no
live role, `TenantRoles` is empty and a canonical label counts for nothing.

## D5 — tests are at the gate, not over HTTP (finding)

The ticket asked for a test in which a revoked owner's bound key is refused
for attest and CSF edit. I wrote it as an HTTP integration test first, and it
could not authenticate the key at all. Since slice 197 (#471) the HTTP router
accepts only JWTs: `jwtmw` plus `requireCredential` when a signer is attached,
which production always does (`cmd/atlas/main.go`). When no signer is
attached, no bearer middleware is mounted at all.
`httpAuthMiddlewareWithExemptions` is wired only in tests. An API key reaches
the platform only over gRPC (`internal/api/server.go` interceptor), and the
gRPC services call none of these gates.

So today a bound key cannot reach attest or CSF edit, and the hole is latent.
The fix stands as defence in depth for any future HTTP key path. An
end-to-end test would need a test-only auth hook, which the #2507 rule ("no
test-only claim-copy hook in non-test code") points away from. The
refusals are pinned on the exact helpers those handlers call:

- `TestHasOwnerRole2523` (the attest gate): bound key, `control_owner` label,
  owner's role revoked → refused; owner holds it live → admitted; free-text
  label → admitted; unbound key label → admitted.
- `TestTenantGates2428_Helpers` (`IsTenantApprover` is the CSF edit gate):
  bound key, `grc_engineer` or `admin` label, owner's role revoked → refused;
  owner holds it live → admitted; unbound `admin` key → admitted.
- `TestLiveRoleLabels2505` (`AckRoleLabels`): a stale canonical label drops,
  a free-text label stays.

## D6 — #2507

#2507 (still open) moves integration fixtures onto live roles and swaps the
`len(OwnerRoles) > 0` read gates. It does not reconcile bound-key labels, so
there was nothing to reuse. Those read gates count any label, bound or not.
They are #2507's to move and are untouched here.

## D7 — tidy-ups

- CSF error text: #2540 made the gate `IsTenantApprover`, so "admin" there
  now means a tenant admin or a super-admin (the ticket predates #2540 and
  said super-admin only). The message says both.
- The #2523 revoke-test doc comment in `attest_integration_test.go` now sits
  on `TestAttest2523_RevokedOwnerRole_RefusedWithoutRelogin`.
- Exceptions help and the `evaluation` help term now say that starting,
  ending or revoking an exception re-evaluates the control (#2498).
