# #2912: osquery host posture, unread fields, last-seen, and what Fleet returns (decisions log)

Found in #2822 (cc4_1 and cc6_6 as SQL checks over `osquery.host_posture.v1`).
No migration. No control bundle changes: the dispatcher sequences the
cc4_1 / cc6_6 ports with #2822, so this PR leaves both bundles untouched.

- detection_tier_actual: manual_review
- detection_tier_target: contract

## D1: Fleet's Get host has no firewall or screen-lock field (item 3)

Checked on 2026-11-21 against Fleet's REST API docs
(<https://fleetdm.com/docs/rest-api/rest-api>, "List hosts" and "Get host").
The raw Markdown source at `fleetdm/fleet/main/docs/REST API/rest-api.md`
returned 404, so the rendered page was read. Most of the page was read
verbatim. A middle span and the tail were read only through a summary, so
"no firewall or screen-lock key" is what the verbatim parts show, backed by
the summary. It is not a guarantee about the whole page.

| What the connector assumed       | What Fleet documents                                                                                                     |
| -------------------------------- | ------------------------------------------------------------------------------------------------------------------------ |
| `firewall_enabled` (bool)        | no such key in List hosts or Get host (macOS, iOS, Android examples)                                                     |
| `screenlock_enabled` (bool)      | no such key. Closest: Android `passcode_protected`, which is not screen lock                                             |
| `mdm_enrolled` (bool)            | no such key. MDM is `mdm.enrollment_status`, a string; examples are `"On (manual)"`, `"On (automatic)"`, `"Pending"`     |
| `disk_encryption_enabled` (bool) | present, top level. "omitted when Fleet doesn't know the host's disk encryption status", and for Linux unencrypted roots |
| (unused)                         | `seen_time`, RFC 3339, on both List hosts and Get host                                                                   |

So with Fleet, before this change, `firewall_enabled`, `screen_lock_enabled`
and `mdm_enrolled` decoded to Go's zero value and were pushed as `false` on
every real host. The verdict was then `fail` ("screen_lock_disabled") for
every host with disk encryption on, and `data_classification` was `unknown`
for every host. Fleet can surface firewall or screen-lock state only through
an operator-written policy (the Get host `policies[]` array, `response`
`pass`/`fail`/`""`), whose query and name are the operator's. Reading a
named policy is a design question for a separate ticket. It is not done
here.

The fixtures in `osqueryposture_test.go` and `integration_test.go` are now
trimmed from the documented example responses (keys, envelope and example
values), not invented.

The `mdm.enrollment_status` mapping (`On (...)` true, `Off` / `Pending` false,
anything else absent) is unverified beyond the three documented example
values. `"Off"` is taken from Fleet's UI wording and has not been checked
against a live Fleet.

## D2: unread fields are omitted, not false (item 1)

The four posture booleans are `*bool` in `osqueryposture.HostPosture`. `nil`
means not read, and `buildHostPostureRecord` leaves the key out. In 1.0.0
all four were already optional, so an omitted key validates against both
1.0.0 and 1.1.0. A detail-fetch failure now pushes no posture booleans at
all.

`evaluate` changes to match. A field observed `false` fails. Then any unread
field is `inconclusive`, with the unread fields named in `reason`. A host
passes only when both disk encryption and screen lock were observed on. In
Fleet mode screen lock is never read, so a Fleet host is now `fail` or
`inconclusive`, never `pass`. That is the honest verdict. It means cc4_1's
result roll-up will be `inconclusive` for a fleet with every disk encrypted.
That is accurate, and it is #2822's business, not this ticket's.

The local-mode SQL drops its `COALESCE(..., '0')`, so a NULL column stays
unread. The local transport is still not wired (`ErrLocalSocketNotWired`).

## D3: last_seen_at in the payload, observed_at stays pull time (item 2)

The ticket asks that the host's last-seen time be emitted. Making it the
record's `observed_at` is not possible:
`internal/evidence/ingest/ingest.go` rejects an `observed_at` more than
`MaxObservedAtSkew` (24 h) from receipt, and the connector aborts the run
on the first push error. So one offline host would stop every push.

Instead, 1.1.0 adds `last_seen_at` (`format: date-time`), from the list
entry's `seen_time` and refined by the detail call's. A host whose detail
fetch fails still carries it. `observed_at` stays the hour-truncated pull
time, which says truthfully when the connector looked, and the idempotency
key is unchanged. A "reported within 24 h" check reads
`payload->>'last_seen_at'`. Local mode sets `last_seen_at` to the query
time, because the queried host is the one answering.

## D4: 1.1.0, kind stays .v1

The new fields are `last_seen_at` and `reason`, both optional, and the
existing properties gain descriptions only. `CheckAdditiveOver(1.0.0,
1.1.0)` passes (`TestOsqueryHostPosture110_AdditiveOver100`), so this is
an additive minor under `osquery.host_posture.v1` (ADR 0028), the same shape
as #2815's S3 1.1.0. 1.0.0 stays registered. `DefaultSeed` lists 1.1.0. The
connector pushes `HostPostureSchemaVersion = "1.1.0"`.

The control bundles reference the kind, not the version, so cc4_1 and cc6_6
read 1.1.0 records without an edit. Porting them to SQL over the new fields
is sequenced by the dispatcher with #2822.

## D5: tenant data

Ledger rows already written by the connector carry `false` for fields it
never read. They are immutable (invariant 2) and are not corrected here. A
SQL check written for 1.1.0 should filter `schema_version = '1.1.0'`, or
treat a 1.0.0 osquery row's `firewall_enabled` / `screen_lock_enabled` as
unread. This is derived from the code. No running deployment was queried.

## Out of scope, noted

`internal/demoseed/fixtures.go` seeds osquery rows with
`firewall_enabled` and `screen_lock_enabled` both `true`, a shape the Fleet
connector can never produce. They are 1.0.0-valid and were left alone.
