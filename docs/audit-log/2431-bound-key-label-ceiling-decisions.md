# 2431 — A bound API key's label caps its owner's live roles: decisions log

Issue: mgoodric/security-atlas#2431, from #2427 (#2402) D2. An API key
bound to a user (`issued_by` set) acted with its owner's full live
`user_roles`, whatever its `owner_roles` label said. A `{viewer}` key minted
for an admin acted as admin, at OPA and, through `Decision.LiveRoles` ->
`cred.TenantRoles` (#2428), at the handler gates.

In this log, a key's "label" means its `owner_roles` role label, not the
`api_keys.label` display name that #2384 added. That name grants nothing.

## Detection-tier classification

- **detection_tier_actual:** manual_review. Recorded as a known gap in
  #2402's D2 and pinned "until #2431" in `TestTenantGates2447_APIKeyRows`.
- **detection_tier_target:** integration.
  `TestDecide2431_BoundKeyRoleMatrix` (real Postgres) printed
  `0 bound keys narrowed; 72 unchanged` and failed on the old code, and
  passes with the fix. Without D2's new case it fails on the unlabelled
  rows (`48 bound keys narrowed; 24 unchanged`); with it, it prints
  `53 bound keys narrowed; 19 unchanged`.

## Decisions

### D1: the label is a ceiling, read per entry (Matt's call, 2026-11-18)

For a bound key with a non-empty label:

- **Ceiling.** Each label entry gives the role it derives on an unbound key.
  `viewer`, `auditor`, `control_owner`, `grc_engineer` and `admin` give
  themselves. A free-text owner-role label (slice 011, e.g. `platform-eng`)
  gives `control_owner`. Mixed labels union their entries, so
  `{viewer, control_owner}` has the ceiling `{viewer, control_owner}`.
- **Effective roles** = ceiling ∩ the owner's live roles. The one exception
  is an owner holding live `admin`, who gets the whole ceiling.
  `admin.rego` admits everything, so that is still a narrowing. No other
  role dominates another: a `grc_engineer` owner of a `{control_owner}` key
  gets nothing.
- An owner with no live roles, or a disabled owner (whose rows the
  resolver already drops, #2239), gives no access.

### D2: an empty label means exactly the owner's live roles (Matt's call, 2026-11-18)

An unlabelled bound key is not capped. It acts as its user, with exactly
the owner's live roles. `derivedRolesFor` has a new case before its
`default`: a bound key with no label and neither flag derives nothing.
Before, it fell through to the default and derived `grc_engineer`, which
was merged with the owner's roles.

The first version of this PR kept that `grc_engineer` ("no cap, existing
keys don't change"). Matt then ruled that an unlabelled key gets the
owner's live roles only. So this narrows unlabelled bound keys too:

- An owner with no live roles, or a disabled owner, now gives the key
  nothing. Before, it had `grc_engineer`.
- **Connector and push keys minted by low-role owners lose `grc_engineer`.**
  A key bound to a `viewer`, `auditor` or `control_owner` owner can no
  longer push evidence, write controls, policies or risks, or approve
  anything that `grc_engineer` granted. It keeps only what the owner's own
  role grants.
- An owner holding `admin` or `grc_engineer` loses nothing.

Unbound keys (`issued_by` NULL) are unchanged. An unlabelled one still
derives `grc_engineer`, as connectors minted through
`POST /v1/admin/credentials` do. A unit row and a matrix row pin that.

### D3: settles #2427's D2 by capping in Decide, after the resolver

#2427 D2 said an intersection doesn't fit the input shape: `Input.User.Roles`
is a set that `Decide` unions with the resolver's rows. So `BuildInput` now
records the ceiling in an unexported `Input.roleCeiling` (it never reaches
OPA), and derives nothing for such a key. `Decide` caps the resolver's rows
with it before merging them. Because `Decision.LiveRoles` is built from the
capped rows, the handler gates (`IsTenantAdmin`, `IsTenantApprover`,
`TenantRoleLabels`, ...) see the same set OPA did. A caller that fills
`Input.User.Roles` itself is not capped, as before.

### D4: out of scope

- Keys with the `is_admin` or `is_approver` flag keep their own cases in
  `derivedRolesFor`. Matt's call.
- `AckRoleLabels` / `HasOwnerRole` still match a bound key's free-text
  label for policy acks and attestation (#2505, #2541). Matt's call.
- Unbound keys, JWT sessions and OAuth clients are unchanged.

### D5: tests that pinned the old behaviour

- `TestTenantGates2447_APIKeyRows`: a `{viewer}` key bound to a tenant admin
  now gets a viewer's status on every gate. An unlabelled key bound to the
  same admin still passes every one.
- `TestReadGates2507_FreeTextLabelOPAAdmitsHandlerRefuses`: the bound key
  leaves the recorded OPA/handler split. With an owner holding no role, both
  layers refuse it. With a `control_owner` owner, OPA admits it.
- `TestDecide2402_APIKeyRoleMatrix`: its bound-key rows move to
  `TestDecide2431_BoundKeyRoleMatrix`, which compares the #2402 derivation
  plus live roles with the capped one for 11 label shapes x 8 owners. It
  allows only narrowing, and checks each decision against the ceiling rule
  written out separately in the test. An unlabelled key must equal its
  owner's live roles (D2), and unbound keys must not change. No rego changed.

## Rollout

`scripts/dry-run-2431-bound-key-ceiling.sql` is read-only and prints counts
only. Per tenant and `owner_roles` shape (`unlabelled` included), it counts
bound keys by outcome (`none`, `narrowed` or `unchanged` role set). It also
counts, before and after, the keys that hold any role, `grc_engineer`, and
`admin`, and the live keys out of scope. Before rollout:

- A key that must keep a role its label doesn't name has to be re-minted
  with that label.
- An unlabelled connector key whose owner holds neither `admin` nor
  `grc_engineer` needs its owner granted `grc_engineer`, or must be
  re-minted as an unbound key.

There is no live install today. Matt approves before merge.
