# #2556 — viewers read the risk register and decisions — decisions log

**Ticket:** https://github.com/mgoodric/security-atlas/issues/2556 (found in
#2151 / #2543 D5)
**Decision (Matt, 2026-11-17):** viewers can read the risk register and the
decision log, and never write either.
**Shape:** one live-role helper, `authz.IsTenantRiskReader`, used by the five
risk-module read gates. `decisions` added to `viewer.rego`. The gates move to
the #2428 agreement table and come off the #2507 `OwnerRoles` allow-list.

detection_tier_actual: manual_review
detection_tier_target: unit

(The defect was found by reading code for a help page (#2151 D5). The #2428
agreement test would have caught it if the read gates had been in its table.)

## D1 — what was actually wrong

#2151 D5 said a viewer passed OPA and was then refused by the handler.
Reading both layers role by role, the picture was different:

- **Risk register, hierarchy, export:** OPA admitted viewer (`viewer.rego`
  lists `risks`, `org_units`, `themes`). The handler gate
  (`IsAdmin || IsApprover || len(OwnerRoles) > 0`) admitted a viewer _by the
  session's role claim_, which jwtmw copies into `OwnerRoles`. So a viewer
  who had just signed in got through. A viewer whose claim no longer named
  the role was refused, even when the live grant was still there. The gate
  also admitted anyone whose claim had _been_ viewer after the grant was
  revoked (#2381). The defect was what the gate counted, not that it
  refused viewers.
- **Decision log:** OPA refused viewer on every `/v1/decisions` read.
  `decisions` is in no role file except admin's wildcard and grc_engineer's
  read-all. A viewer got 403 from OPA before the handler ran.

## D2 — the gate: one helper matching OPA

`authz.IsTenantRiskReader(cred) = IsTenantApprover(cred) || viewer ∈
TenantRoleLabels(cred)`:

- admin and grc_engineer come through `IsTenantApprover`, by the admin or
  approver flag, a live role, or an API key's label. That is the same rule
  every #2428 gate already uses.
- viewer comes through `TenantRoleLabels`: the live `user_roles` row authzmw
  resolved, or the label of a credential that names no user (an unbound key
  or a machine token). A session's claim never counts. Nor does a bound
  key's label, which is a copy made when the key was minted (#2427).
- A bare push credential (no flag, no label) is still refused. OPA's
  `derivedRolesFor` maps it to grc_engineer, so the handler stays the
  narrower layer for it, as slice 067 intended.

I added a named helper rather than calling `HasTenantRole`. `HasTenantRole`
admits _any_ role, and also a bare credential through `derivedRolesFor`,
which would widen the gate past OPA for control_owner, auditor and push
keys.

## D3 — control_owner and auditor

The old gate admitted both roles, because any `OwnerRoles` entry passed it.
OPA has refused both on `risks`, `decisions` and `org_units` reads all
along: `control_owner.rego` and `auditor.rego` list none of the three, and
`auditor.rego` says outright that the auditor does not see `/v1/risks`. So
in production neither could reach the register, and the handler was the
wider of the two layers. That is the same bug the other way round, which
the #2428 table never allows. The new gate refuses both, and nothing either
role could reach before changes.
`TestRiskReads2556_OtherRolesUnchanged` pins it: both roles are refused
every risk-module read and write, except `GET /v1/themes`, a catalog read
(`defaults.rego` `catalog_resources`) that every signed-in role gets.

## D4 — inventory of risk-module read routes

"Before" is `main` at fb841bb. "Handler" is the handler's own role gate;
"none" means OPA alone decides.

| Route                                     | OPA, viewer: before → after         | Handler gate: before → after                                   | Viewer end to end, after                        |
| ----------------------------------------- | ----------------------------------- | -------------------------------------------------------------- | ----------------------------------------------- |
| `GET /v1/risks` (register)                | allow → allow                       | claim/label, any role → `IsTenantRiskReader`                   | 200                                             |
| `GET /v1/risks/heatmap`                   | allow → allow                       | none                                                           | 200                                             |
| `GET /v1/risks/theme-heatmap` (hierarchy) | allow → allow                       | claim/label → `IsTenantRiskReader`                             | 200                                             |
| `GET /v1/risks/categories` (taxonomy)     | allow → allow                       | none                                                           | 200                                             |
| `GET /v1/risks/review-cadence`            | allow → allow                       | none                                                           | 200                                             |
| `GET /v1/risks/export`                    | allow → allow                       | claim/label → `IsTenantRiskReader`                             | 200                                             |
| `GET /v1/risks/{id}` (detail)             | allow → allow                       | none                                                           | 200                                             |
| `GET /v1/risks/{id}/aggregation`          | allow → allow                       | none                                                           | past the gates (404 on a non-roll-up)           |
| `GET /v1/risks/{id}/children`, `/parents` | allow → allow                       | none                                                           | 200                                             |
| `GET /v1/risks/{id}/audit-log`            | allow → allow                       | none                                                           | 200                                             |
| `GET /v1/risks/{id}/action-plans`         | allow → allow                       | actionplans `hasProgramRead`, claim/label — **unchanged** (D5) | 200 by claim                                    |
| `GET /v1/themes`                          | allow (catalog) → allow             | none                                                           | 200                                             |
| `GET /v1/org_units` (hierarchy)           | allow → allow                       | claim/label → `IsTenantRiskReader`                             | 200                                             |
| `GET /v1/org_units/{id}`                  | allow → allow                       | none                                                           | 200                                             |
| `GET /v1/decisions`                       | **deny → allow**                    | claim/label → `IsTenantRiskReader`                             | 200                                             |
| `GET /v1/decisions/overdue`               | **deny → allow**                    | none                                                           | 200                                             |
| `GET /v1/decisions/{id}`                  | **deny → allow**                    | none                                                           | past the gates                                  |
| `GET /v1/decisions/{id}/audit-log`        | **deny → allow**                    | none                                                           | past the gates                                  |
| `GET /v1/saved-views`                     | allow → allow (any role, own views) | none                                                           | not risk-scoped; unchanged                      |
| `GET /v1/aggregation-rules`, `/{id}`      | deny → deny                         | none                                                           | rule configuration, not the register; unchanged |

Every risk-module **write** stays refused for viewer at OPA: 34 routes,
listed in `riskWrites2556` and in the integration test. These are creating,
editing, treating, closing and reopening a risk; the acceptance moves;
delete; control, child and theme links; categories; themes; org units; and
decision create, update, supersede, link and unlink. The new `decisions`
admit sits under `viewer.rego`'s `is_read` rule, so it covers reads only.

## D5 — what stays on #2507

`GET /v1/risks/{id}/action-plans` is a risk read whose gate is actionplans'
`hasProgramRead`. That gate also guards `/v1/controls/{id}/action-plans`
and the `/v1/action-plans` reads. Moving it means deciding the action-plan
read set for control_owner and auditor, which is outside this ticket. It
stays on the #2507 allow-list. A viewer reads it today through the claim, as
before. The other `hasProgramRead` copies (dashboard, personnel security,
the export gates) stay on #2507 too.

## D6 — fixtures

The risks and decisions integration fixtures (`testServer`) used to
authenticate with a role claim on a server without authzmw. A live-role
gate never sees a live role there, so they now go through
`liveRoleBearer`: a seeded `users` + `user_roles` row, the real OPA bundle
with the DB roles resolver, and a JWT whose subject is that user. That is
the #2507 pattern. risks runs as a grc_engineer, its register operator.
decisions runs as a viewer: its tests only read, so the decisions suite
itself now proves the viewer path. Every test in both packages passes under
OPA unchanged.

## D7 — the agreement table

The five gated reads join `routes2428` under `riskReaderGate`, so
`TestTenantGates2428_HandlerAgreesWithOPA` checks the handler and OPA admit
the same roles there. The site scanner (`gate_sites_2447_test.go`) now
inventories `IsTenantRiskReader` calls too. Two tests assumed no gate admits
a viewer, the #2447 "admin in A, viewer in B" row and the unbound viewer
key row. They now expect what a viewer gets (`viewerStatus2447`), in the
unit tier and the integration tier alike.
