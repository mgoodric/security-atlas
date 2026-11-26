# 2447 — Tenant-role gates follow-ups: decisions log

Issue: mgoodric/security-atlas#2447, the follow-ups from the review of #2442
(#2428). Builds on `docs/audit-log/2428-tenant-role-handler-gates-decisions.md`.

## Detection-tier classification

- **detection_tier_actual:** manual_review. The three claim-reading gates
  were left in place by #2428 D5 and named in that PR's review.
- **detection_tier_target:** unit. `TestGateSites2447_TableMatchesSource`
  now fails when a handler's helper gate changes without the #2428 route
  table, and the package tests pin revoked-while-live on each gate.

One test that leaned on the stale path surfaced while building this:
`internal/api/scfimport/overlayremap_integration_test.go` ran the overlay
remap API with no authz middleware attached, so its tenant-admin JWT passed
the overlay gate on the role claim alone. It now attaches authzmw with a
live-role resolver, as `posture_targets_integration_test.go` does.

## Decisions

### D1 — the three gates are IsTenantApprover, with OPA's role set

`risks.requireTaxonomyAdmin` (category and theme writes),
`posturetargets.Handler.Set` and `mappingoverlays.requireOverlayWrite` call
`authz.IsTenantApprover`. The private `hasTaxonomyAdmin`, `hasTargetAdmin`
and `hasOverlayWrite` are removed. All three admitted admin or grc_engineer
before, and OPA admits exactly admin and grc_engineer on each of their 16
routes (`TestTenantGates2428_HandlerAgreesWithOPA`, with the routes added to
`routes2428`). No route needed a handler-narrower entry, and none is
narrower or wider in role than before.

What changes is where the role is read. A JWT's role claim (`OwnerRoles`)
no longer counts, so a revoked grant is refused while its token is live;
the live `user_roles` row does count. One consequence goes the other way: a
person granted grc_engineer after their token was issued now passes these
gates on their next request, where the old claim read refused them until
they signed in again. OPA already admitted that person, so this aligns the
handler to OPA rather than widening past it.

An API key's `owner_roles` label still counts, as in #2428 D2.

### D2 — the API-key rows pin current behaviour, not the target

A key bound to a user (`UserID` is the owner, not the key id) derives no
role from a viewer label and takes the owner's live roles (#2402 D2). So a
viewer-labelled key bound to a tenant admin passes every approve and
tenant-admin gate. `TestTenantGates2447_APIKeyRows` pins that as it is
today. #2431 is to make the label a ceiling; when it lands, that row flips
to 403 and the test changes with it. An unbound viewer key is refused
every gate.

### D3 — the drift guard is a source scan, not a route walk

The guard parses every non-test Go file under `internal/api` and records
the `authz.IsTenantAdmin` / `authz.IsTenantApprover` calls per function.
`gateSites2447` must match that inventory exactly; each site names the
`routes2428` rows it guards or says why it guards none (the /v1/me profile
flags, two checklist reads, and three tenant-admin gates that predate #2428:
LLM routing, control-import settings, period auditors). A second test checks
every `routes2428` row is guarded by a site calling its gate.

Resolving route to handler to gate through chi registration and call graphs
was rejected: it needs type information across packages and would be
brittle. The scan is source only, so it cannot flake. It does not follow a
gate through a wrapper in another package, which no gate does today.

`cred.IsAdmin` reads are not inventoried. They are the `/v1/admin/*` and
install-wide gates #2216 D2 keeps super-admin only, outside `routes2428`. A
handler moved from a helper to the flag still fails the guard, because its
helper call disappears.

### D4 — sites outside routes2428 stay outside

The three pre-#2428 tenant-admin gates were not added to `routes2428`. Their
GET routes would need handler-narrower entries for reader roles, and
pinning their OPA agreement is beyond this ticket. They are inventoried, so
a change to their helper still fails the guard.

### D5 — every tenant-role helper in roles.go is decided (#2706)

`internal/authz/roles.go` exported three tenant-role helpers the D3
inventory did not cover. Each is now decided, and
`TestRoleHelpers2706_EveryHelperDecided` parses `roles.go` and fails on any
exported `IsTenant*` or `Has*Role` function that is in neither
`isHelper2447` nor `nonGate2447` (which carries a reason per name).

- **`IsTenantControlOwner` is inventoried.** It is in `isHelper2447`, and
  the `controldetail/authz.go readsUnscopedEvidence` site lists it after
  `IsTenantApprover`. It has no OPA-agreement row: its one caller filters
  the per-control evidence list (it drops the `control_ref` leg for a
  period-scoped caller) and never refuses a request, like the
  `buildProfile` not-a-route entry.
- **`HasTenantRole` is a non-gate.** Its three call sites in
  `internal/api/me/profile.go` only set `has_tenant_role` on `/v1/me`; it
  never allows or denies a request. The gate for that route is the Rego
  roleless self-read allow in `internal/authz/rego_bundle/defaults.rego`
  (`roleless_self_read_paths`, #2494).
- **`HasOwnerRole` is a per-resource ownership check, outside the
  tenant-role inventory.** It does deny: `internal/api/controls/attest.go`
  returns 403 when the caller does not hold the control's `owner_role`. But
  it checks ownership of one control against `AckRoleLabels`, not a tenant
  role, and Rego hands that check to the handler by design: the #2523
  attest rule in `internal/authz/rego_bundle/control_owner.rego` admits the
  `control_owner` role at `POST /v1/controls/{id}/attestations` and leaves
  ownership to `authz.HasOwnerRole`. The 403 is covered by
  `TestAttest_Rejects_WhenCallerLacksOwnerRole` and the "owner_role differs"
  leg of `TestAttest2523_RoleMatrix`
  (`internal/api/controls/attest_integration_test.go`), and the role layer
  by `TestHasOwnerRole2523`. Its doc comment no longer claims the CSF
  assessment edit gate, which moved to `IsTenantApprover` in #2540.
