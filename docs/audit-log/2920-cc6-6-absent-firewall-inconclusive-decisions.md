# #2920 — cc6_6: absent firewall state is inconclusive (decisions log)

Follows [#2822](https://github.com/mgoodric/security-atlas/issues/2822)
(`2822-cc6-6-host-firewall-sql-check-decisions.md`), whose D2 and D3
this replaces. Context: #2912 found that Fleet's Get host API has no
firewall field. #2916 made the osquery connector omit fields it did not
read (`osquery.host_posture.v1` 1.1.0, which also added `last_seen_at`),
and #2921 added `firewall_source = fleet_policy:<name>` (1.2.0). The
#2822 query counted a missing `firewall_enabled` as off, so every Fleet
host failed cc6_6 and the control reported every firewall as off.

- detection_tier_actual: manual_review
- detection_tier_target: contract

The defect was a schema change (absent now means "not read") that the
query's fail-closed coalesce did not follow. It was found by reading
Fleet's API during #2912, not by a test. A contract test between the
schema's field semantics and the stock queries that read them would have
caught it.

## D1 — Absent is inconclusive, observed off fails, any off wins

Matt's decision on the ticket (2026-11-21). Per host, the latest record
is either on (`firewall_enabled` is JSON `true`), off (present and
anything else), or unread (absent). Across hosts: any off fails the
control; otherwise all on passes; otherwise (some unread) inconclusive.
An off host fails the control even while others are unread, because one
observed failure is enough to say the control does not hold.

A present value that is not JSON `true` (a string `"true"`, `null`)
counts as off, not unread. The schema makes the field a boolean, so such
a value is malformed. Treating it as off keeps the #2822 fail-closed
posture for anything the connector did write. A bundle test pins it.

## D2 — Records without `host_uuid` are left out, not failed

The ticket allowed either. The schema requires a non-empty `host_uuid`,
so ingest rejects such a record before the query sees it. The query
filters `coalesce(payload->>'host_uuid', '') <> ''` so a record that
gets through anyway does not form a NULL (or empty-string) "host" that
decides the control for hosts it isn't. Leaving it out over failing the
control means a malformed record cannot hold the control at fail for the
whole window. When nothing attributable remains, the control is
inconclusive (bundle test `only-records-without-host-uuid-is-inconclusive`).

## D3 — Latest per host orders by `last_seen_at`, then `observed_at`; ties are all-or-nothing

`observed_at` is the connector's pull time, and Fleet lists offline
hosts, so the host's own `last_seen_at` is the better recency key
(ticket AC). Ordering is `last_seen_at DESC NULLS LAST, observed_at
DESC`. NULLS LAST, not `coalesce(last_seen_at, observed_at)`: a 1.0.0
record without `last_seen_at` would otherwise use its pull time, which is
later than the host's real check-in on a newer record, and win.

`last_seen_at` is cast only when it matches
`^YYYY-MM-DD[T ]HH:MM`. The registry does not assert JSON Schema
`format`: `compileSchema` (`internal/api/schemaregistry/service.go`)
never calls `AssertFormat()`, and santhosh-tekuri/jsonschema v6 treats
`format` as an annotation under draft 2020-12 without it. So
`last_seen_at` can arrive as any string, and a failed cast would error
the whole query to inconclusive. A non-matching value sorts as missing. A value
that matches the prefix but is still not a timestamp (month 13) errors
the query, which the engine maps to inconclusive with the error shown.

`rank()`, not `DISTINCT ON`, so records that tie on both keys are all
kept. A host is on only when every tied record is on, and off when any
tied record is off; a tie of on and unread is unread. `DISTINCT ON`
picked one tied row arbitrarily, so the verdict could change between runs.

The sandbox appends the author SQL after its own `WITH evidence AS
(...)`, so the query uses nested subqueries; a second `WITH` would be a
syntax error.

## D4 — Description rewritten

The #2822 description said an unread host is "pushed with
`firewall_enabled` false" and counts as off. That was stale from #2916.
The new text states the per-host and across-host rules, why absent is
inconclusive, that `firewall_source` names the Fleet policy the value
came from, that a host outside a platform-targeted Fleet policy is
unread (#2923 review note), and that with no firewall policy named the
control stays inconclusive. The query reads `firewall_enabled` only; it
does not check `firewall_source`.

## D5 — Supersedes `soc2_cc6_6_boundary_protection` again

The query's `expression` changes, so the bundle supersedes on the next
boot (#1908). The query `id` and `evidence_kind` are unchanged. Owners,
evidence, evaluations, exceptions and risk links stay on the old row and
have to be moved by hand (#2820 D1). This PR carries the
`supersedes-stock` label (#2891); the changelog fragment carries the
upgrade note.

**Upgrade note:** on the first start after upgrading,
`soc2_cc6_6_boundary_protection` is superseded by a new version.
Re-assign its owner and move exceptions and risk links to the new
version; the old version keeps its history. A Fleet tenant that saw
cc6_6 fail on every host will see it go inconclusive until a firewall
Fleet policy is named for the connector (#2921).
