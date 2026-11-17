# 2523 — Attestation owner-role gate reads live roles: decisions log

Issue: mgoodric/security-atlas#2523, found in the review of #2508 (#2505).
`credstore.Credential.HasOwnerRole` decided who may attest a control
(`POST /v1/controls/{id}/attestations`, and `caller_can_attest` on the attest
form) by matching the control's free-text `owner_role` against
`cred.OwnerRoles`. For a user's JWT session that is the token's role claim,
which outlives a revoke until the token expires (#2381). The same method also
gated CSF assessment edits (`csfassessment.editContext`, role `grc_engineer`).

## Detection-tier classification

- **detection_tier_actual:** manual_review. Found reviewing #2508.
- **detection_tier_target:** integration.
  `TestAttest2523_RevokedOwnerRole_RefusedWithoutRelogin` holds one session
  across a revoke and a re-grant of `control_owner`. With the old matching
  restored it fails (201 after the revoke); with the fix it passes.

## Decisions

### D1 — the gate moves to `authz.HasOwnerRole`; the credstore method goes

`credstore` cannot import `authz` (authz imports credstore), and the live
roles rule lives in `authz`. The method is deleted rather than kept beside
the new function, so no caller can reach the claim-trusting version, and its
entry leaves the #2505 `OwnerRoles` ban-test allow-list.

### D2 — match against `AckRoleLabels`, not `TenantRoleLabels`

`owner_role` is free text set by the control bundle, like a policy's
`acknowledgment_required_roles`. A user holds a non-canonical owner role only
through an `api_keys.owner_roles` label, which an admin sets when minting the
key and which goes when the key is revoked. `TenantRoleLabels` drops a bound
key's label (#2505 D2), which would leave a bound key unable to attest any
control whose `owner_role` is not a canonical tenant role. `AckRoleLabels`
counts live `user_roles`, plus the label for every credential except a user's
JWT session. That removes the stale part (the session claim) and keeps what
keys, machine tokens and in-memory credentials could do before.

Consequence: a bound key whose owner lost `control_owner` but whose label
still says `control_owner` can still pass this gate. That matches #2505 D3
and the existing `IsTenant*` helpers (#2505 D2's out-of-scope note); OPA
decides first in any case.

### D3 — admin stays the `IsAdmin` wildcard; a live tenant admin role is not one

"Admin can still attest" keeps the existing rule: `cred.IsAdmin` (an admin
key, or the re-checked super-admin bit on a session) admits any `owner_role`,
including an empty one. A live `admin` user_role was never a wildcard here
(the old method matched it only verbatim) and still is not; widening it is
a separate decision.

### D4 — CSF assessment edits move with it

`csfassessment.editContext` called the same method with `grc_engineer`. It
now calls `authz.HasOwnerRole`, so a session with a stale `grc_engineer`
claim is refused there too (`TestRoleCut_...` asserts the 403).

### D5 — OPA admits control_owner at the attestations path, and nowhere else new

Added to this ticket by the dispatcher (also #2150 D5). The attest route maps
to `controls`/`write`, and `control_owner.rego` granted controls write only at
`/evaluate` (#2110), so a user whose only role was `control_owner` was
refused before the handler ran, although the handler, the GLOSSARY and the
help say owners attest. A rule mirroring the #2110 one admits `control_owner`
for `POST /v1/controls/{uuid}/attestations`, anchored on the uuid and the
exact suffix, in `policies/authz/control_owner.rego` and its
`internal/authz/rego_bundle` copy (#2410). `authz.HasOwnerRole` stays the real
gate.

`TestDecide2523_AttestRoleMatrix` and
`TestDecide2523_ControlOwnerAttestRuleIsNarrow` pin the OPA layer.
`TestAttest2523_RoleMatrix` runs it end to end: an owner on its own control
gets 201, a viewer gets 403 from authz, and an owner on another role's control
gets 403 from the handler. The attest harness's owner now holds
`control_owner` alone.

## Findings (not fixed here)

- **OPA refuses `/v1/csf/*` for every role but super-admin.** No rego rule
  names the `csf` resource, so with the authz middleware attached (as
  `cmd/atlas` attaches it) a live grc_engineer is refused with
  `default-deny: role [grc_engineer] cannot write csf`, and a viewer cannot
  read. The CSF integration suite runs without the middleware, so its
  engineer is now a client_credentials token, whose claim is its grant.
