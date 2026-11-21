# #2822 — cc4_1: every received host checked in within 24 hours (decisions log)

Second half of [#2822](https://github.com/mgoodric/security-atlas/issues/2822).
cc6_6 shipped in #2914 (`2822-cc6-6-host-firewall-sql-check-decisions.md`)
and was reworked by #2920 (`2920-cc6-6-absent-firewall-inconclusive-decisions.md`).
cc4_1 waited on two blockers, both now on `main`:

- #2911 (PR #2918): the SQL sandbox's `evidence` relation carries a
  constant `evaluated_at`, the engine's evaluation instant (the as-of
  instant on a replay, the case's pinned instant in a bundle test). A
  guard refuses wall-clock functions in stock SQL.
- #2912 (PR #2916): `osquery.host_posture.v1` 1.1.0 carries
  `last_seen_at`, Fleet's `seen_time`, and omits unread fields.

- detection_tier_actual: none
- detection_tier_target: none

No bug surfaced in this change.

## D1 — "Reported within 24 h" is `last_seen_at` within 24 h of `evaluated_at`

`observed_at` is the connector's pull time, and Fleet lists offline
hosts, so a fresh `observed_at` shows only that the connector ran
(#2912 point 2). The host's own `last_seen_at` is the check-in time the
control asks about. The comparison is against `evaluated_at`, never
`now()`, so a replay and a frozen audit period get the answer they would
have got at their instant, and the bundle tests do not age (#2911). The
engine test `replay measures from the as-of instant` pins this: a check-in
51 hours before the wall clock passes when replayed 48 hours back.

The bound is inclusive: a host last seen exactly 24 hours before the
instant passes (`last-seen-exactly-24h-passes`), one minute more fails
(`last-seen-just-over-24h-fails`). A `last_seen_at` after the instant
(clock skew) counts as fresh; it is not a failure of monitoring.

## D2 — Shape follows cc6_6 after #2920

The dispatcher asked for #2929's cc6_6 pattern, and it fits:

- Records with no or empty `host_uuid` are left out (cc6_6 D2).
- Latest per host is `rank()` over `last_seen_at DESC NULLS LAST,
observed_at DESC`. `last_seen_at` is cast only when it matches
  `^YYYY-MM-DD[T ]HH:MM`, because the registry does not assert `format`
  (cc6_6 D3); a non-matching value sorts as missing.
- Per host: stale (`last_seen_at` older than 24 h), fresh, or unread (no
  readable `last_seen_at`). Across hosts: any stale fails; all fresh
  passes; otherwise inconclusive. No attributable record is inconclusive.

Ties need no all-or-nothing rule here. Records tied on rank have equal
`last_seen_at` (or both missing), so they agree on staleness. The
`bool_or` / `bool_and` aggregation is kept so the query reads like cc6_6.

## D3 — Missing `last_seen_at` is inconclusive, not pass or fail

The dispatcher's brief for this ticket set the rule: an absent
`last_seen_at` is inconclusive. A 1.0.0 record, or a host Fleet gave no
`seen_time`, says nothing about when the host reported. Failing it would
hold the control at fail for a connector-version gap. Passing it would
reintroduce the "connector ran" reading D1 removes. One observed stale
host still fails the control while others are unread, as an observed
firewall-off host does in cc6_6.

A consequence of NULLS LAST, pinned by
`later-record-without-last-seen-does-not-hide-a-stale-check-in`: if a
host's newest known check-in is stale and a later pull carries no
`last_seen_at` (a failed detail fetch), the stale check-in decides and the
control fails. The later pull does not show the host checked in since, so
the newest known check-in is the right evidence.

## D4 — Scope: received hosts only

The query sees only hosts with a record in the 7-day daily window. A
managed endpoint that never reported, or that last reported before the
window, is not seen. Completeness against a managed-endpoint inventory is
the pending half (ADR 0028; #2834), and the description names it. The
title changed from "Endpoint posture is continuously monitored via osquery
/ Fleet" to "Endpoints reporting posture have checked in within 24 hours",
and the query id from `every_endpoint_reported_in_24h` to
`every_reporting_host_seen_in_24h`, so neither claims managed-endpoint
coverage.

## D5 — Supersedes `soc2_cc4_1_continuous_monitoring`

The query's language, expression and id change, so the bundle supersedes
on the next boot (#1908). Owners, evidence, evaluations, exceptions and
risk links stay on the old row and have to be moved by hand (#2820 D1).
The PR carries the `supersedes-stock` label (#2891); the changelog
fragment carries the upgrade note.

**Upgrade note:** on the first start after upgrading,
`soc2_cc4_1_continuous_monitoring` is superseded by a new version.
Re-assign its owner and move exceptions and risk links to the new
version; the old version keeps its history. The control no longer rolls
up the connector's disk-encryption-plus-screen-lock verdict, so a tenant
whose cc4_1 passed can now fail (a host offline for over 24 hours) or go
inconclusive (records without `last_seen_at`, from a connector older than
`osquery.host_posture.v1` 1.1.0), and the reverse.
