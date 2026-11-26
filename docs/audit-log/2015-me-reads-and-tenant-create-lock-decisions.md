# #2015 — `/v1/me/*` reads for every role, serialised tenant create: decisions log

Follow-ups from #2003 (#1763).

detection_tier_actual: manual_review
detection_tier_target: unit

The `/v1/me/tenants` gap was caught in review of #2003, not by a test. No
test asked whether a viewer could load the app shell. The new authz table
tests do.

## D1 — Which `/v1/me/*` reads every role gets

All `/v1/me/*` paths resolve to authz resource type `me`
(`internal/authz/input.go` `resourceFromPath`). Before this change, only
auditor (all `me` reads), grc_engineer (all reads) and admin (everything)
could read them, plus the #1763 exact-path admit for `/v1/me/tenancy`. A
viewer or control_owner got `403` on its own profile.

`defaults.rego` now admits a read of these exact paths to any credential
with a role:

| Path                                                                     | Why every role needs it                       |
| ------------------------------------------------------------------------ | --------------------------------------------- |
| `/v1/me`                                                                 | the app shell's current-user profile          |
| `/v1/me/tenancy`                                                         | tenant chrome (ADR 0023 decision 2; #1763)    |
| `/v1/me/tenants`                                                         | the tenant switcher                           |
| `/v1/me/preferences`                                                     | the user's own settings                       |
| `/v1/me/notifications`                                                   | the notification bell                         |
| `/v1/me/acknowledgments`                                                 | pending policy acknowledgments for the caller |
| `/v1/me/sessions`                                                        | the user's own sessions list                  |
| `/v1/me/email-channel`, `/v1/me/slack-channel`, `/v1/me/webhook-channel` | the user's own channel opt-in toggles         |

Every handler behind them pins the user to the verified credential or JWT
claim, so a wider admit does not widen what rows a caller sees.

Stays denied to viewer and control_owner, with tests:

- `/v1/me/audit-period`, `/v1/me/audit-periods`: auditor-only
  (auditor.rego). Nothing in them is the caller's unless they hold an
  auditor assignment.
- Any sub-path or unlisted path (`/v1/me/`, `/v1/me/sessions/{id}`,
  `/v1/me/not-a-route`): the rule is an exact-path set, not a prefix, so
  an endpoint added under `/v1/me/` stays denied until someone lists it.
- Every write. The rule needs `is_read`. Whether a viewer may PATCH its own
  profile or preferences, or toggle a channel, is a separate decision and
  is not made here.

## D2 — The multi-tenant gate moved inside the create transaction

#1763 D1 counted tenants before the transaction. `Create` now takes an
install-wide `pg_advisory_xact_lock(0x201520152015)` inside the
transaction, after the per-actor slice-143 lock, and counts there. The lock
is only taken while `ATLAS_ALLOW_MULTI_TENANT` is off, so an opted-in
install keeps creating tenants concurrently.

The out-of-transaction count was removed rather than kept as an early
check. Keeping it would mean two counts of the same thing, and it would
also hide the lock from a test: with a session tenant present, the early
check refuses before the lock is reached. One consequence: a blocked create
with a malformed body now gets `400` for the body, not `403` for the gate.
Either refusal writes nothing.

Lock order is actor lock, then install lock, on the one path that takes
both, so they cannot deadlock. The key is distinct from the slice-142
(`0x142142142142`), slice-198 (`0x198198198198`) and slice-143
(`0x0143…`) keys.

The test (`TestCreate_GateSerialisedByInstallLock`) holds the install lock
from a second transaction, checks that a create waits behind it, then
releases the lock and checks the create gets `403`. Against the old
handler it fails: the create returned `403` at once.

## D3 — `tenant_count` stays visible to every role (optional item not taken)

The ticket's optional item would return `tenant_count` only to admins and
super_admins. Not done. The web app reads `multi_tenant` (#1764), and the
count names no tenant. Splitting the response by role would change a
contract #1764 already reads, for no data that needs protecting. It can be
done later if a reason turns up.
