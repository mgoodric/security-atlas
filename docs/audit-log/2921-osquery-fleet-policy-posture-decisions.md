# #2921: osquery connector reads firewall and screen lock from named Fleet policies (decisions log)

Follow-up to #2912 / #2916. Fleet's host API has no firewall or screen-lock
field, so cc6_6 could not evaluate against a Fleet estate. No migration. No
control bundle changes: cc6_6 reads `firewall_enabled` by kind, not by
version, so 1.2.0 records reach it unchanged.

- detection_tier_actual: none
- detection_tier_target: none

## D1: what Fleet documents about `policies[]`

Checked on 2026-11-21 against <https://fleetdm.com/docs/rest-api/rest-api>,
"Get host" (`GET /api/v1/fleet/hosts/:id`). The example response's
`policies` array was read verbatim. Each object carries `id`, `name`,
`query`, `description`, `resolution`, `platform`, `response`, `critical`.
The three example `response` values are `"fail"`, `""` and `"pass"`. The
page has no sentence listing the allowed values of `response`, and no
sentence saying what `""` means. Reading `""` as "no result for this host
yet" is our inference from the example, so the connector treats it, and any
value other than `pass`/`fail`, as unread.

"List hosts" also accepts `populate_policies=true`. The connector already
makes a Get host call per host, so it reads `policies[]` there and adds no
request.

The fixture in `osqueryposture_test.go` (host 1) is that documented array,
with Fleet's example names (`SomeQuery`, `SomeQuery2`, `SomeQuery3`) kept so
it stays traceable to the docs.

## D2: policies are named by exact `name`, not id

The ticket suggested `--firewall-policy "<name>"`. A name is what an
operator sees in the Fleet UI and survives re-creating the policy on
another Fleet. An id would be unambiguous but opaque in a cron line.
Matching is exact, with no case folding or trimming: a near-miss name
leaves the field unread, which is visible (inconclusive, field absent) and
does not guess.

A global policy and a team policy can share a name. If two entries carry
the name and their results agree, that result is used. If they disagree,
or one has no result, the field is unread.

## D3: schema 1.2.0 adds `firewall_source` and `screen_lock_source`

ADR 0028 decision 8: a schema change is a new schema version. 1.1.0's
descriptions said firewall and screen lock are "never present on a
Fleet-sourced record", which is no longer true, and the source fields are
new properties under `additionalProperties: false`. So 1.2.0 is a new file,
additive over 1.1.0 (`TestOsqueryHostPosture120_AdditiveOver110`), and the
kind stays `.v1`. Both 1.0.0 and 1.1.0 stay registered.

The value is `fleet_policy:<name>` (schema pattern `^fleet_policy:.+$`), so
a reviewer of a record can see which operator query produced the boolean.
The pattern allows only the Fleet source. A local-osqueryd source would
widen it in a later minor. A source is emitted only alongside its boolean.

The ticket text spells the fields `firewall_enabled` / `screenlock_enabled`.
The registered schema has used `screen_lock_enabled` since 1.0.0, and that
name is kept.

## D4: the connector does not create the policies

The connector holds a read-only Fleet token (`observer` / `observer_plus`)
and its anti-criteria forbid mutating Fleet. The operator writes the
policy, and what the query checks is what `true` means. The README ships
example queries:

- macOS firewall, macOS screen lock, Windows firewall (domain profile) and
  Windows screen lock are verbatim from Fleet's standard query library
  (`fleetdm/fleet` `main`,
  `docs/01-Using-Fleet/standard-query-library/standard-query-library.yml`,
  downloaded with curl on 2026-11-21 and grepped).
- `alf.global_state` values are from osquery's table spec
  (`osquery/osquery` `specs/darwin/alf.table`).
- Linux screen lock: the README points to Fleet's "Ubuntu GNOME lock screen
  after 5 minutes" library query rather than restating it.
- Linux firewall: Fleet's library has none. The README's query is ours. It
  uses `file_lines` and `systemd_units` (`id`, `active_state` per
  `specs/linux/systemd_units.table`). It is marked unverified on a live
  host.

None of these queries was run against a live host or a live Fleet.

## D5: `--mode=local` rejects the policy flags

They name Fleet policies. Accepting and ignoring them in local mode would
let a misconfigured cron line look configured.

## D6: platform-targeted policies

A Fleet policy targeted at one platform has no `policies[]` entry on other
hosts, so those hosts' field is omitted, not false. The README says so and
gives the two ways to cover a mixed fleet: one cross-platform query, or one
connector run per platform-specific Fleet team. A per-platform flag
(`--firewall-policy-darwin` and so on) was not added. It can come later if
operators ask for it.
