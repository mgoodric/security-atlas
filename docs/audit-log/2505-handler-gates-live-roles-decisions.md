# 2505 — Handler gates and device approval read live roles: decisions log

Issue: mgoodric/security-atlas#2505, the follow-up #2496 D4 reported. After
#2500, OPA counts only live `user_roles` for a user's JWT session. Some
handler code still read `cred.OwnerRoles`, which for a session is the
token's role claim and outlives a revoke (#2381):

- `oauth/device_approve.go` copied it into the approved device token, so a
  revoked role was re-minted with a fresh expiry.
- `adminvendors` / `adminauditperiods` `callerAllowedExport`,
  `admindemo.requireAdmin` and `policyacks`' ack caller matched role names
  against it.
- Seventeen read gates admitted on `IsAdmin || IsApprover ||
len(OwnerRoles) > 0`.

## Detection-tier classification

- **detection_tier_actual:** manual_review. Found reviewing #2500.
- **detection_tier_target:** unit, for the gates; integration, for device
  approval. `TestCallerAllowedExport2505`, `TestRequireAdmin2505` and
  `TestAckCallerFromCred2505` hold a stale claim with a narrower live role.
  `TestIntegrationDeviceApprove2505_MintsLiveRolesNotClaim` grants admin,
  revokes it for viewer, then revokes everything, all with the same session.

## Decisions

### D1 — one rule for which labels a gate counts: `authz.TenantRoleLabels`

A gate counts the live roles authzmw resolved (`cred.TenantRoles`). It also
counts the credential's own `OwnerRoles`, but only when the credential names
no user: an unbound API key, a client_credentials token, or an in-memory
credential. Those have no `user_roles` to resolve, so the label is their
grant. A user's session counts live roles alone, as #2496 made OPA do.

`HasAnyTenantRole` is `IsAdmin || IsApprover || len(TenantRoleLabels) > 0`.
It replaces the read gates' `len(OwnerRoles) > 0`. It is still stricter than
`derivedRolesFor`, which grants a bare connector credential grc_engineer.

### D2 — a bound API key counts its owner's live roles, not its label

A key bound to a user (`issued_by` set) carries a label copied when it was
minted. #2427 D2 made such a key's read-only labels derive nothing in OPA, so
its roles are its owner's live `user_roles`. The handler gates follow the
same rule for every label (fail closed). A bound key whose owner has lost
every role is now refused by these gates, while OPA can still admit it on
a privileged label's `control_owner` derivation.

Out of scope, and left for Matt: `IsTenantAdmin`, `IsTenantApprover` and
`IsTenantControlOwner` (#2428) still count a bound key's label. Changing them
would remove access from bound admin and approver keys on routes that #2428's
role table pins, so it needs its own ticket and dry-run counts.

### D3 — policy acks count a bound key's label (`authz.AckRoleLabels`)

`acknowledgment_required_roles` is free text (`employee`,
`security-engineering`), not an RBAC role. The only place a user holds such a
role is an `api_keys.owner_roles` label, and the ack roster counts its
denominator from that column. Under D2 a bound key would lose every
free-text role, so its user would be on the roster with no way to
acknowledge. Acks therefore count live roles plus the label for every
credential except a user's session. The session's claim is the stale part,
and an ack grants nothing.

### D4 — device approval reads `user_roles` itself; nil resolver mints nothing

`/oauth/device_authorization/approve` is outside authzmw, so `TenantRoles`
is never set there. The endpoint takes an `authz.RolesResolver`
(`DBRolesResolver` in `cmd/atlas`) and reads the approver's live roles at
approval. The snapshot-at-approval invariant is unchanged. A resolver error
fails the approval with 500 rather than minting a token. A nil resolver
mints an empty role list. A role-less approver mints an empty list, and the
token derives nothing (#2401).

### D5 — a source scan bans new direct `OwnerRoles` reads

`TestOwnerRolesReads2505_OnlyAllowListed` parses `internal/` and `cmd/`,
outside `internal/authz` and the generated `internal/db/dbx`, and lists every
function that reads `x.OwnerRoles`. Each must be in an allow-list with the
reason it is not a role gate: persistence, display on `/v1/me`, key
rotation, fixtures, the `AckCaller` field, or the slice-011 attestation
match. The scan reads syntax, not types, so other structs' `OwnerRoles`
fields are listed too.

`credstore.Credential.HasOwnerRole` is allow-listed as it stands. It matches
a control's free-text `owner_role` for attestations, not a tenant role.
Whether a session's claim should count there is a separate question,
reported rather than changed here.
