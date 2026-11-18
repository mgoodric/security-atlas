# 2431 — A bound API key's label caps its owner's live roles: decisions log

Issue: mgoodric/security-atlas#2431, from #2427 (#2402) D2. An API key
bound to a user (`issued_by` set) acted with its owner's full live
`user_roles`, whatever its `owner_roles` label said. A `{viewer}` key minted
for an admin acted as admin, at OPA and, through `Decision.LiveRoles` ->
`cred.TenantRoles` (#2428), at the handler gates.

## Detection-tier classification

- **detection_tier_actual:** manual_review. Recorded as a known gap in
  #2402's D2 and pinned "until #2431" in `TestTenantGates2447_APIKeyRows`.
- **detection_tier_target:** integration.
  `TestDecide2431_BoundKeyRoleMatrix` (real Postgres) printed
  `0 bound keys narrowed; 72 unchanged` and failed on the old code, and
  passes with the fix.

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

### D2: an empty label means no cap (Matt's call, 2026-11-18)

An unlabelled bound key keeps acting as its user, so existing keys don't
change. It keeps the derivation it had before (`grc_engineer`, merged with
the owner's live roles). The matrix asserts every route is unchanged for
it.

That is "no change", which is not quite "equals the owner's live roles":
the old derivation still adds `grc_engineer`. Dropping that is a narrowing
of its own and was not asked for here.

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
  written out separately in the test. No rego changed.

## Rollout

`scripts/dry-run-2431-bound-key-ceiling.sql` is read-only and prints counts
only. Per tenant, it counts in-scope bound keys by label shape and outcome
(`none`, `narrowed` or `unchanged` role set), plus the live keys out of
scope. A key that must keep a role its label doesn't name has to be
re-minted with that label first, or left unlabelled. There is no live
install today. Matt approves before merge.
