# #2822 — cc6_6 host firewall as a SQL payload check (decisions log)

Parent: [#1860](https://github.com/mgoodric/security-atlas/issues/1860).
Implements ADR 0028 tier 1, check 2, following the cc6_1 tracer's pattern
(`docs/audit-log/2821-cc6-1-sql-payload-check-decisions.md` D1). Check 3,
cc4_1, ships in a later PR on the same ticket (D4).

- detection_tier_actual: none
- detection_tier_target: unit

No bug surfaced while building. The connector gap in D3 was found by
reading the connector, not by a test.

## D1 — Each host's latest record decides, not every record

ADR 0028 states the check as "every host-posture record has the host
firewall enabled". The connector pushes one record per host per run, so a
7-day daily window holds several records for one host. Read literally, a
host that had its firewall off on Monday and turned it on Tuesday would
fail the control until the next Monday. The query takes each host's latest
in-window record by `host_uuid` (`DISTINCT ON ... ORDER BY observed_at
DESC`), the same shape cc6_1 uses per policy (#2821 D2), and requires
`firewall_enabled` true on all of them. A bundle test pins each
direction: on after off passes, off after on fails.

## D2 — A missing or non-boolean `firewall_enabled` counts as off

`coalesce(payload->'firewall_enabled' = 'true'::jsonb, false)` per row.
Without the inner coalesce, `bool_and` would skip the NULL that a missing
field produces, and a host that never reported the field would pass. The
schema doesn't require the field, but the connector always emits it.

## D3 — A host the connector could not read counts as firewall off

When Fleet's per-host detail call fails, `PullFromFleet` still emits the
host with `Result=inconclusive`, and `buildHostPostureRecord`
(`connectors/osquery/cmd/atlas-osquery/cmd_run.go`) always writes the four
booleans, so the payload says `firewall_enabled: false`. The query reads
the payload and not the verdict (#2821 D1), so it can't tell "off" from
"unread", and one such host fails the control. That is fail-closed, and
the description says so.

The fix belongs in the connector: omit fields it did not read.
[#2912](https://github.com/mgoodric/security-atlas/issues/2912) tracks
that, along with the host last-seen time cc4_1 needs. Once the connector
omits the field, D2 still fails the host. To make such a host inconclusive
rather than failed, the query would have to return `inconclusive` for a
missing field. That is a separate change, made when #2912 lands.

**Unverified:** I didn't check Fleet's API docs or a live Fleet for
whether `GET /api/v1/fleet/hosts/{id}` returns `firewall_enabled`. The
connector's `HostDetail` decodes it, and its tests supply it. If Fleet
doesn't return it, every real host decodes as false and this control
fails. #2912 covers checking that.

## D4 — cc4_1 waits on #2911 and #2912

cc4_1 ("every host seen has reported in the last 24 hours") needs the
evaluation instant. The SQL sandbox doesn't receive it: its relation is
`evidence(result, observed_at, payload)`. Using `now()` would break replay
and the pinned bundle tests. Matt chose to pass the evaluation instant to
the sandbox, as an ADR 0028 amendment.
[#2911](https://github.com/mgoodric/security-atlas/issues/2911) does that.
cc4_1 also needs the host's own last-seen time, because the connector's
`observed_at` is its pull time, and Fleet lists offline hosts too (#2912).
cc4_1 is unchanged in this PR.

## D5 — Supersedes the stock bundle `soc2_cc6_6_boundary_protection`

The query's `language`, `expression` and `id` change, so the bundle
supersedes on the next boot (#1908). Owners, evidence, evaluations,
exceptions and links stay on the old row (#2820 D1). Since it supersedes
anyway, the stale strings go too:

- the query id `host_firewall_enabled_on_managed_endpoints` becomes
  `every_reporting_host_firewall_enabled`. Nothing checks "managed"; that
  waits on the inventory (#2834);
- the query description says "every host's latest posture record", not
  "every managed endpoint";
- the title "Production endpoints have host-firewall enabled" becomes
  "Endpoints reporting posture have the host firewall enabled", because
  nothing reads whether a host is production;
- the Rego input comment is gone with the Rego.

The changelog fragment carries the upgrade note.
