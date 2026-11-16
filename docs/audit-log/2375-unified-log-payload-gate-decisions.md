# #2375: actorFrom fails closed; unified-log payloads follow the source read

detection_tier_actual: manual_review
detection_tier_target: integration

Follow-ups from the review of #2367 (#1954). Two parts.

## Part 1: no audit write without an actor

`actorFrom` in `internal/api/aggregationrules` and
`internal/api/mappingoverlays` returned `"system"` when the request had no
credential, or one without an id. The write then landed in
`aggregation_rule_audit_log` / `fw_to_scf_overlay_edge_audit_log` under an
actor nobody holds. Both now write 401 `credential missing` and the handler
returns before the store is reached, the same rule #1954 gave
`internal/api/risks`.

**D1. No explicit system actor was needed.** Every caller of these six
routes (rule create, activate, deactivate; overlay create, retire, import)
is an HTTP request behind the auth middleware. No job, seeder or migration
calls the handlers, and the stores take the actor as an argument, so a
non-HTTP writer names its own actor there.

## Part 2: payload content follows the source resource

### Who reads the unified log

| Surface                       | Admit                                             | Rows                                                                                     |
| ----------------------------- | ------------------------------------------------- | ---------------------------------------------------------------------------------------- |
| `/v1/admin/audit-log/unified` | admin, auditor, grc_engineer (handler role probe) | every kind, every user's me rows                                                         |
| `/v1/admin/audit-log/export`  | same                                              | same                                                                                     |
| `/v1/activity/unified`        | all five roles (`activity` resource type)         | admin/auditor/grc_engineer: as above. viewer/control_owner: no feature_flag, own me rows |

Before this change every row on all three surfaces carried its full
`payload_json`.

### Role x kind: payload a role saw that its source read denies

"source" is the OPA resource type of the route that reads the kind's
subject. `leak` = the role saw the payload before this change and cannot
read the source; `-` = no leak (source readable, or the row is not in the
role's view). admin and grc_engineer read every resource and never leak.

| Kind             | Source (OPA type)                | auditor | control_owner | viewer     |
| ---------------- | -------------------------------- | ------- | ------------- | ---------- |
| decision         | `admin` (`/v1/admin/audit-log`)  | leak    | leak          | leak       |
| evidence         | `evidence`                       | leak    | -             | leak       |
| exception        | `exceptions`                     | -       | leak          | leak       |
| sample           | `audit-periods` (D3)             | -       | leak          | leak       |
| audit_period     | `audit-periods`                  | -       | leak          | leak       |
| aggregation_rule | `aggregation-rules`              | leak    | leak          | leak       |
| feature_flag     | `admin` (`/v1/admin/features`)   | leak    | - (hidden)    | - (hidden) |
| me (other user)  | self only (D4)                   | leak    | - (hidden)    | - (hidden) |
| walkthrough      | `walkthroughs`                   | -       | -             | leak       |
| framework_scope  | `framework-scopes`               | -       | -             | -          |
| taxonomy         | `risks` (`/v1/risks/categories`) | leak    | leak          | -          |
| mapping_overlay  | `mapping-overlays`               | leak    | leak          | leak       |
| control_import   | `controls`                       | -       | -             | -          |
| risk             | `risks` (stripped by #2367)      | n/a     | n/a           | n/a        |

The table is for a caller holding exactly one role. A human signed in by
JWT also holds `control_owner` in OPA (side finding 2), so in production
the auditor and viewer columns lose their `evidence` and `walkthrough`
leaks: they can read those sources too.

### Decisions

**D2. One mechanism, payload-only.** `internal/api/adminauditlog/payloadgate.go`
maps each kind to its source resource type and asks the OPA engine for the
caller's `read`, once per resource per request. A denied row keeps its
envelope (occurred_at, actor, kind, target_type, target_id, action, row_id)
and returns `payload_json: null` (an empty cell in CSV/XLSX). No row is
hidden, so the who-did-what-when trail stays complete for an auditor. The
same gate runs on the unified list, the activity feed and the export. The
decision is the one the authz middleware would make for the source route:
same `authz.BuildInput` from the credential, same engine. Matt approved
this shape through the dispatcher before it was built.

**D3. Sample rows use the `audit-periods` read.** The `samples` rule for
auditors needs the row's `audit_period_id` attribute, which a kind-wide
decision does not have. Samples are reached through their audit period, so
the period read is the honest kind-wide source.

**D4. me rows keep payload only for their own user or an admin.** A me row
is one user's own settings and actions (`/v1/me` is self-only). "Admin" is
the credential's `IsAdmin` flag or an OPA `write` on the `admin` type, which
only the admin role (and super_admin's tenant routes) passes. grc_engineer
reads the `admin` type but cannot write it, so grc_engineer no longer sees
other users' me payloads.

**D5. Fail closed.** A kind missing from the map, a nil engine, or an engine
error withholds the payload from a non-admin. A new audit-log kind shows
its envelope only until someone maps it.

**D6. Export JSON renders every empty payload as `null`.** Before, only
`?include_payload=false` did. No unified-log branch produces an empty
payload (each is `to_jsonb(row) - ...`), so the only new nulls are withheld
payloads.

### Side findings (not fixed here)

1. **The auditor admit on `/v1/admin/audit-log/unified` and `/export` is
   probably dead.** `authz.resourceFromPath` resolves `/v1/admin/...` to
   resource type `admin`, not `audit-log-unified` / `audit-log-export`. The
   rego entries and `internal/authz/slice124_test.go` /
   `slice135_test.go` test a type the real path never produces. Auditors
   reach the same rows through `/v1/activity/unified`. The dispatcher is
   filing a ticket.
2. **Every JWT human also holds `control_owner` in OPA.** `jwtmw` puts the
   JWT's tenant roles in `Credential.OwnerRoles`, and
   `authz.derivedRolesFor` maps any non-empty `OwnerRoles` to
   `control_owner`, merged with the user's real roles. So a JWT viewer or
   auditor can read, and write, `evidence`, `artifacts` and `walkthroughs`.
   The payload gate inherits this because it reuses the middleware's
   decision. It needs its own ticket.
3. **A JWT caller's `UserID` carries a `user:` prefix that me_audit_log's
   `user_id` lacks.** The gate strips it (`jwtmw.SubjectUserID`) to recognize
   a caller's own me rows. `ActivityList` passes the prefixed id as
   `CallerUserID` to the SQL predicate, so a non-privileged JWT caller
   probably never sees their own me rows on the activity feed. Not changed
   here.

### Tests

- `internal/api/adminauditlog/payloadgate_integration_test.go`: seeds one
  row of eleven kinds plus the caller's own me row. For each role it reads
  the activity feed, and for auditor and grc_engineer also the unified list
  and the JSON export. Every row's payload must be present exactly when the
  role's table says so, and every kind must still be returned. A role-only
  admin keeps every payload. All three tests fail with the gate disabled.
- `internal/api/adminauditlog/payloadgate_test.go`: unit cases for the
  admin flag, nil engine, own and other me rows, an unmapped kind, and a
  real viewer decision.
- `internal/api/aggregationrules/actor_test.go`,
  `internal/api/mappingoverlays/actor_test.go`: the 401 with no credential
  or no id, against a nil store, so a write would panic.
