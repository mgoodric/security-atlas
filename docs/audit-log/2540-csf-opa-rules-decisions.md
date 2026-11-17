# 2540 — OPA rules for the csf resource: decisions log

Issue: mgoodric/security-atlas#2540, found while building #2523 (#2530).
The rego bundle had no rule naming the `csf` resource, so `/v1/csf/*` fell
through to default-deny for most tenant roles. The CSF integration suite
dodged it with a client_credentials token and no authz middleware.

## Detection-tier classification

- **detection_tier_actual:** manual_review. Found building #2530; the
  suite's own workaround comment named the gap. Writing the live-role
  integration matrix for this ticket then found a second bug (D3): a tenant
  admin was refused every CSF write by the handler gate.
- **detection_tier_target:** integration. `TestRoleMatrix_EveryCSFRoute`
  runs every route as a user session per role through OPA with live roles.
  With `csf.rego` removed it fails 13 ways (below); with the #2530 handler
  gate restored it fails on the four admin writes.

## Inventory (before this change)

OPA below is the bundle on `main` at c26a1ec, decided per role through
`authz.BuildInput` (resource type `csf`, action `read` for GET, `write`
otherwise). The handler gate is `internal/api/csfassessment/handler.go`.

| Method | Route                                                 | Handler gate                                        | OPA admitted        | Intended            |
| ------ | ----------------------------------------------------- | --------------------------------------------------- | ------------------- | ------------------- |
| GET    | `/v1/csf/tier`                                        | `readContext` (any tenant credential)               | admin, grc_engineer | any tenant role     |
| GET    | `/v1/csf/profiles/{kind}`                             | `readContext`                                       | admin, grc_engineer | any tenant role     |
| GET    | `/v1/csf/gap`                                         | `readContext`                                       | admin, grc_engineer | any tenant role     |
| PUT    | `/v1/csf/tier`                                        | `editContext`: `HasOwnerRole(cred, "grc_engineer")` | admin               | admin, grc_engineer |
| PUT    | `/v1/csf/profiles/{kind}`                             | `editContext`                                       | admin               | admin, grc_engineer |
| PUT    | `/v1/csf/profiles/{kind}/selections`                  | `editContext`                                       | admin               | admin, grc_engineer |
| DELETE | `/v1/csf/profiles/{kind}/selections/{requirement_id}` | `editContext`                                       | admin               | admin, grc_engineer |

The issue says admin was refused too. At the OPA layer it was not:
`admin.rego` admits every non-super-admin-only resource, and
`grc_engineer.rego` admits every read. What refused admin was the handler
gate (D3). Viewer, auditor and control_owner reads, and grc_engineer writes,
were refused by OPA: 13 role-route pairs.

"Intended" is slice 515's stated cut (handler package doc, decisions-log D4):
reads for any authenticated tenant credential; writes for grc_engineer or
admin; viewer, auditor and control_owner cannot edit.

## Decisions

### D1 — reads for any tenant role, writes for grc_engineer; admin via admin.rego

This is the dispatcher's recommendation and slice 515's documented intent.
The read rule requires `count(input.user.roles) > 0`, the same shape as the
`/v1/people` and `/v1/approvals` admits in `defaults.rego`, so a session with
no live role is still refused. The auditor reads the assessment: the Tier
and Profiles are the program's self-assessment, the kind of artefact an
assessor reviews, and nothing on these routes is operator-internal.

### D2 — a new `csf.rego`, exact routes, mirrored byte for byte

The rules sit in their own file in `internal/authz/rego_bundle/` and are
copied unchanged to `policies/authz/csf.rego` (#2410). A new file keeps the
mirror identical for what this change touches without dragging in the
existing `defaults.rego` drift, which is #2410's to fix.

Each rule matches its exact paths, and writes also match the method, so the
csf admit opens nothing else: other sub-paths, other methods, and profile
kinds other than `current` / `target` stay denied
(`TestDecide2540_CSFRulesAreNarrow`). An unknown kind is now a 403 from OPA
for a non-admin where the handler would have said 400. Failing closed is the
better of the two.

### D3 — the edit gate moves from `HasOwnerRole` to `IsTenantApprover`

`authz.HasOwnerRole`'s admin wildcard is `cred.IsAdmin`, the super-admin
flag. A tenant admin's admin role is a live `user_roles` row, so #2530's gate
refused every tenant admin's CSF write, contrary to the handler's own doc.
`authz.IsTenantApprover` is the estate's "live tenant admin or grc_engineer"
gate (#2428), and it is exactly this cut. `editRole` was the only
non-free-text use of `HasOwnerRole`, so that function's free-text reasoning
(#2523 D2) does not apply here. Using it enrols the four write routes in the
#2428 route table and the #2447 gate-site inventory, so
`TestTenantGates2428_HandlerAgreesWithOPA` now checks, role by role, that
the handler admits exactly what OPA admits on them.

### D4 — the integration suite runs as user sessions with live roles

Every server in `internal/api/csfassessment/integration_test.go` now attaches
OPA and authzmw with `authz.NewDBRolesResolver`, and every caller is a
session: an active `users` row, `user_roles` rows, and a JWT whose subject is
`user:<uuid>` (#2507's fixture shape, kept local to this package). The
client_credentials engineer and the super-admin `AdminFor` claims are gone.
`TestRoleCut_LiveRolesNotTheClaim` checks that a claim differing from the
live roles follows the live roles, in both directions.
