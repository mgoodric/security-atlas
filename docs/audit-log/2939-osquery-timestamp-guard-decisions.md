# #2939: full-shape timestamp guard and future cap on the osquery SQL checks (decisions log)

Follow-up from #2822 (PR #2932). cc4_1 guards `last_seen_at` with an
anchored RFC 3339 regex plus `pg_input_is_valid(value, 'timestamptz')`, and
reads a value more than 1 h after `evaluated_at` as unreadable. Four other
stock SQL checks over `osquery.host_posture.v1` still used a start-anchored
prefix guard and had no cap:

| Bundle                           | Query id                                | From  |
| -------------------------------- | --------------------------------------- | ----- |
| `soc2_cc6_6_boundary_protection` | `every_reporting_host_firewall_enabled` | #2929 |
| `soc2_cc6_8_malware_protection`  | `antimalware_active_and_recent`         | #2937 |
| `soc2_cc7_2_anomaly_detection`   | `edr_running_on_every_managed_endpoint` | #2937 |
| `soc2_a1_1_capacity_monitoring`  | `production_capacity_healthy`           | #2937 |

- detection_tier_actual: manual_review
- detection_tier_target: unit

The bug is that a `last_seen_at` such as `2026-13-45T00:00:00Z` or
`2026-02-30T05:00:00Z` passed the prefix guard, and the `::timestamptz`
cast then errored the whole query. One bad host made the control
inconclusive and hid every other host's result. Review of #2932 caught it
by reading. The target is `unit`, because a string-level lint over the
stock bundles now refuses the pattern (D3).

## D1: Same guard and cap as cc4_1, in the rank key

In these four checks `last_seen_at` is used only in the `rank()` that picks
each host's latest record. The `ORDER BY` CASE now casts only a value that
matches `eval.RFC3339Pattern` and passes `pg_input_is_valid`. The cast sits
in a nested `CASE ... THEN`, not in the same `AND`, because Postgres does
not guarantee `AND` operand order, and only a `CASE` forces it. A value
more than an hour after `evaluated_at` gives NULL. With `DESC NULLS LAST`,
a NULL ranks below every readable value, the same place an absent
`last_seen_at` already ranked.

The stricter regex also drops two shapes the prefix guard accepted: a space
separator, and minute-only precision. The osquery connector writes
`p.LastSeen.UTC().Format(time.RFC3339)` (`cmd_run.go`), so it never emits
either, and every existing case value already has the full shape.

## D2: Deviation from the AC wording. A malformed host is decided on its field, not inconclusive

The ticket's third AC says "one malformed host is inconclusive". The
dispatcher wrote that with cc4_1 in mind and approved this reading for the
four checks (2026-11-22).

In cc4_1, `last_seen_at` is what the check asks about, so an unreadable
value is inconclusive. Here it only orders records. The verdict comes from
`firewall_enabled`, `antimalware_protected`, `edr_healthy` or the capacity
readings. A record with no `last_seen_at` (a 1.0.0 record) already decides
on its field, and existing cases rely on that. Making a malformed value
inconclusive would treat it more harshly than an absent one, and would
change each control's semantics beyond the guard. So a malformed or future
value sorts as missing, and the checked field decides.

Cases, three per bundle:

- `malformed-last-seen-does-not-hide-a-failing-host`: a month-13 host next
  to a failing host. Expected `fail`; on `main` the query errored.
- `malformed-last-seen-host-is-decided-on-its-field`: a February 30th host
  whose field is fine, next to a passing host. Expected `pass`, which shows
  the query ran; on `main` it errored.
- `future-last-seen-sorts-as-missing`: one host with a newer-observed record
  whose `last_seen_at` is 24 h ahead (field fine), and an older record with
  a readable `last_seen_at` (field bad). Expected `fail`; on `main` the
  future record ranked first and the control passed.

All 12 fail with `main`'s `control.yaml` and pass with this one
(`TestStockSQLBundleTests_PassAgainstPostgres` against a scratch
Postgres 16; PR body has the output).

## D3: One spec snippet plus a lint, and the cap is not linted

`docs/spec/control-bundle.md` gains "Reading a payload timestamp": the
snippet, why each part is there, and how a NULL is read in each kind of
check. `eval.SQLUnguardedTimestampCasts` finds every
`(payload->>'f')::timestamptz` (also `timestamp`, `date`, `time`) and
reports the field unless the expression also contains
`payload->>'f' ~ '<RFC3339Pattern>'` and
`pg_input_is_valid(payload->>'f', 'timestamptz')`.
`TestStockSQLChecksGuardTimestampCasts` sits next to the `wallClockSQL`
test (#2911) and holds the stock bundles to it. Before the SQL change it
failed on exactly these four bundles; cc4_1, cc1_4 and cc7_1 already
passed.

It is a string check, not a parse. It does not prove the guard and the
cast are in the same `CASE`. It catches the pattern that shipped: a
bare cast, a prefix guard, or a guard with only one of the two parts.
The future cap is not linted. It sits on a derived column whose shape
varies between checks (a nested `CASE` here, a subquery in cc4_1), and a
string match on it would be brittle. Each bundle's
`future-last-seen-sorts-as-missing` case covers it instead.

No other stock SQL check casts a payload timestamp without the guard:
cc1_4 (`created_at`, `resolved_at`, `completed_at`) and cc7_1
(`created_at`, `resolved_at`) already carried it from #2824/#2829.

## Upgrade note (supersedes-stock)

The `expression` of each of the four bundles changed, so on the next boot
each is superseded by a new version: `soc2_cc6_6_boundary_protection`,
`soc2_cc6_8_malware_protection`, `soc2_cc7_2_anomaly_detection` and
`soc2_a1_1_capacity_monitoring`. Owners, evidence, exceptions and risk
links stay with the old version and have to be moved by hand. Verdicts
change only for a host whose `last_seen_at` is malformed (the control
used to be inconclusive) or more than an hour in the future (that record
no longer outranks the host's readable ones). `stock_manifests_gen.go`
was regenerated, and the diff only adds lines.
