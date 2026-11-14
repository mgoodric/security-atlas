# #2022 — `/v1/me/*` settings writes for every role: decisions log

Follow-up from #2015 (#2018).

detection_tier_actual: manual_review
detection_tier_target: unit

The gap was found in review of #2018, not by a test. The new authz table
tests cover it.

## D1 — Which self-service writes every role gets

All `/v1/me/*` paths resolve to authz resource type `me`. Before this
change only admin (`admin.rego`, everything) could write `me`. No other
role file lists `me` among its writable resources. grc_engineer's
`notifications` entry never matched `PATCH /v1/me/notifications/{id}/read`,
because that path resolves to `me`, not `notifications`. So viewer,
control_owner, auditor and grc_engineer could all read their settings
(#2015) but could not change any of them. The red run against the old
rego shows 32 denials: 8 routes for each of those 4 roles.

`defaults.rego` now admits these writes to any credential with a role:

| Method   | Path                                                                     | What it changes                       |
| -------- | ------------------------------------------------------------------------ | ------------------------------------- |
| `PATCH`  | `/v1/me`                                                                 | own display name and time zone        |
| `PATCH`  | `/v1/me/preferences`                                                     | own notification and UI preferences   |
| `PUT`    | `/v1/me/email-channel`, `/v1/me/slack-channel`, `/v1/me/webhook-channel` | own opt-in to each delivery channel   |
| `DELETE` | `/v1/me/sessions`                                                        | revoke own other sessions             |
| `DELETE` | `/v1/me/sessions/{id}`                                                   | revoke one own session                |
| `PATCH`  | `/v1/me/notifications/{id}/read`                                         | mark own notification read (the bell) |

The ticket names the first six as candidates. The two id-bearing routes
are included because they are the same decisions: revoking one session is
part of "revoke your own sessions", and marking a notification read is
how a viewer clears the bell that #2015 already lets it read. Leaving them
out would let a viewer see a session or notification it could not act on.

Nothing here touches another user, a role, a tenant or an audit-binding
record. The profile PATCH changes only display name and time zone.

## D2 — The handlers take the user from the credential only

Checked each handler in `internal/api/me/`:

| Handler                                              | User source                                                               |
| ---------------------------------------------------- | ------------------------------------------------------------------------- |
| `ProfileHandler.PatchMe`                             | `cred.UserID`, `cred.TenantID`                                            |
| `PreferencesHandler.PatchPreferences`                | `cred.UserID`                                                             |
| `EmailChannelHandler.Put`, `ChannelOptInHandler.Put` | `parseCredIDs(cred)`                                                      |
| `SessionsHandler.RevokeOtherSessions`                | `cred.UserID`, `cred.TenantID` → `RevokeOthersForUser`                    |
| `SessionsHandler.RevokeSession`                      | `cred.UserID` → `RevokeForUser(tenant, user, id)`; other user's id is 404 |
| `NotificationsHandler.MarkRead`                      | `store.MarkRead(id, cred.UserID)`                                         |

No handler reads a user id from the body or the path, so admitting the
route to every role does not let anyone write another user's row.

## D3 — Method and path pinned, id segment shape-checked

The six fixed routes are a set of `[method, path]` pairs. The two
id-bearing routes are matched by splitting the path: exactly one non-empty
segment for the id, and `read` as the final segment for notifications.
`/v1/me/sessions/`, `/v1/me/sessions/a/b`, `/v1/me/notifications//read`
and `/v1/me/notifications/n-1/read/extra` stay denied, with tests. A new
write under `/v1/me/` stays denied until someone lists it.

The #2015 test `TestMeSelfRead2015_WritesNotAdmitted` asserted that a
viewer could not make four of these writes. That was the open question
this ticket settles, so the test is removed. Its deny side lives on in
`TestMeSelfWrite2022_DeniesTheRest`, which also covers writes to the
read-only `/v1/me` paths (`tenants`, `tenancy`, `notifications`,
`acknowledgments`, `audit-period`).
