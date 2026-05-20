# Audit logs — the append-only trio (and the rest)

<!-- prettier-ignore-start -->
!!! info "What you'll learn"

    - The three load-bearing audit logs: `decision_audit_log`,
      `evidence_audit_log`, `me_audit_log`
    - How they compose with the other six per-domain audit logs through
      the unified API
    - How to query them, export them, and route them to an external SIEM
<!-- prettier-ignore-end -->

security-atlas writes nine per-domain audit-log tables. Three of them
are the **operator-facing trio** — the ones you query when you need to
answer "who did what, when, with what authority, and what was the
outcome?" The other six cover per-primitive lifecycle events
(exception approvals, audit-period freezes, walkthrough captures,
etc.).

Every audit log is **append-only**: SELECT + INSERT only under
PostgreSQL FORCE ROW LEVEL SECURITY. No UPDATE / DELETE policy is
defined. A misconfiguration cannot retroactively rewrite the trail.

## The trio

### `decision_audit_log` — every authorization decision

Every API request that goes through the platform's OPA policy
evaluation produces one row.

| Column           | Meaning                                                              |
| ---------------- | -------------------------------------------------------------------- |
| `decision_id`    | Primary key.                                                         |
| `occurred_at`    | When the decision was made.                                          |
| `user_id`        | Who. Text — covers both human users and service accounts.            |
| `user_roles`     | Roles held at decision time.                                         |
| `action`         | What was attempted (e.g. `policies.publish`, `audit-period.freeze`). |
| `resource_type`  | What kind of thing (e.g. `policy`, `audit_period`).                  |
| `resource_id`    | Which specific instance.                                             |
| `result`         | `allow` or `deny`.                                                   |
| `reason`         | Why (Rego policy explanation).                                       |
| `policy_hits`    | Which OPA policy bundle paths matched.                               |
| `request_path`   | The HTTP path.                                                       |
| `request_method` | The HTTP method.                                                     |

The `result = 'deny'` slice is the one you scan during incident
response: who tried to do what, and what was the reason for the deny.

### `evidence_audit_log` — every ingest decision

Every call to the Evidence SDK's `IngestEvidence` API produces one row,
whether the record was accepted or rejected.

| Column            | Meaning                                                  |
| ----------------- | -------------------------------------------------------- |
| `id`              | Primary key.                                             |
| `credential_id`   | Which API key / OIDC token pushed.                       |
| `decision`        | One of the enumerated outcomes (see below).              |
| `reason_code`     | Free-text detail on the decision.                        |
| `idempotency_key` | The idempotency key the pusher sent (or NULL).           |
| `evidence_kind`   | The schema kind being pushed.                            |
| `record_id`       | The canonical record ID, if the decision was `accepted`. |
| `received_at`     | When the push hit the API.                               |

`decision` is one of:

- `accepted` — the record landed in the ledger.
- `deduplicated` — same idempotency key, already accepted.
- `rejected_validation` — payload did not match the registered schema.
- `rejected_unknown_kind` — `evidence_kind` is not registered.
- `rejected_idempotency_mismatch` — idempotency key collision with
  different content.
- `rejected_scope_violation` — `scope_id` does not resolve to a cell
  in the active tenant.
- `rejected_observed_at_skew` — `observed_at` is unreasonably in the
  future or past.
- `rejected_oversized` — payload exceeded the per-record cap.
- `rejected_rate_limit` — the pusher exceeded its rate limit.
- `rejected_unauthenticated` — bearer / OIDC token did not validate.
- `rejected_internal_error` — the platform itself failed; receipt
  carries an opaque error.

This is the table that answers "why didn't my CI push land?" — the
`reason_code` carries the specific schema validation error, scope
mismatch, etc.

### `me_audit_log` — every self-service action

Every action a user takes against their own profile, preferences, or
sessions, plus a small set of cross-cutting read events.

| Column        | Meaning                                            |
| ------------- | -------------------------------------------------- |
| `id`          | Primary key.                                       |
| `occurred_at` | When.                                              |
| `user_id`     | Which user (UUID).                                 |
| `action`      | What — see the enumerated set below.               |
| `before`      | JSONB snapshot of the field-set before the change. |
| `after`       | JSONB snapshot of the field-set after the change.  |

`action` (extended by later slices):

- `profile.update` — user updated their display name, time zone, etc.
- `preferences.update` — user updated notification preferences.
- `session.revoke` — user revoked one of their own sessions.
- `audit_log_query_unified` — user queried the unified audit log
  (recorded for meta-audit per slice 124).
- `risk_export`, `vendors_export`, `controls_export`, `evidence_export`,
  `policies_export`, `exceptions_export`, `samples_export` — bulk
  exports (slices 136–138).

The `before` / `after` JSONB pair makes diff reconstruction trivial.

## The other six

| Table                        | Owning slice | What it records                                      |
| ---------------------------- | ------------ | ---------------------------------------------------- |
| `exception_audit_log`        | 021          | Exception requested / approved / rejected / expired. |
| `sample_audit_log`           | 026          | Population sampled — `n`, `seed`, sample-ID set.     |
| `audit_period_audit_log`     | 028          | AuditPeriod created / opened / frozen / archived.    |
| `aggregation_rule_audit_log` | 053          | OPA aggregation rule created / modified / retired.   |
| `feature_flag_audit_log`     | 059          | Feature flag flipped (per-tenant or global).         |
| `walkthrough_audit_log`      | 027          | Walkthrough captured / amended / linked.             |

All nine ride the same append-only RLS pattern and all are tenant-
isolated.

## Querying the unified API

The aggregator (slice 124) provides a single endpoint that UNIONs across
all nine tables, time-windowed, with cross-table filters:

```sh
curl -fsS -X GET "http://localhost:8080/v1/admin/audit-log/unified?since=2026-04-01T00:00:00Z&until=2026-06-30T23:59:59Z&user=alice%40example.com" \
  -H "Authorization: Bearer $ATLAS_TOKEN"
```

Query parameters:

| Parameter        | Meaning                                                       |
| ---------------- | ------------------------------------------------------------- |
| `since`, `until` | Time window. Required.                                        |
| `user`           | Filter to one user-id substring.                              |
| `action`         | Filter to one action substring.                               |
| `result`         | `allow` / `deny` (only applies to `decision_audit_log`).      |
| `kind`           | Filter to one `evidence_kind` (only applies to evidence log). |
| `limit`          | Default 100, max 1000 per page.                               |
| `cursor`         | Opaque pagination cursor.                                     |

The response is a chronologically-sorted union, one row per event, with
an `audit_log_source` field telling you which underlying table the row
came from.

**Every successful unified query writes a `me_audit_log` row with
`action = 'audit_log_query_unified'`** — meta-auditing the audit-log
queries themselves. Closing the loop.

## Exporting the audit logs

The audit-log export library (slice 135) supports CSV, JSON-lines, and
XLSX out:

```sh
curl -fsS -X GET "http://localhost:8080/v1/admin/audit-log/export?since=2026-04-01&until=2026-06-30&format=jsonl" \
  -H "Authorization: Bearer $ATLAS_TOKEN" \
  -o audit-2026-Q2.jsonl
```

The export is gated by:

- The `admin.audit-log.export` OPA policy (default: admin role only).
- A per-(tenant, user) concurrency cap (slice 145).
- A row-count cap (configurable per format).

Every export itself writes a `me_audit_log` row with the appropriate
action value — `audit_log_export`, `risk_export`, etc.

## Routing to an external sink

The external audit-log sink (slices 126 + 129 + 130) lets you stream
the trio plus the other six to an operator-owned destination:

| Sink              | Configured via                                               |
| ----------------- | ------------------------------------------------------------ |
| File (local)      | Path on disk; rotated per the configured `audit_log_rotate`. |
| Webhook (POST)    | HTTPS endpoint; signed with HMAC.                            |
| Object store      | S3-compatible; configurable batching window.                 |
| Syslog / RFC 5424 | For SIEM ingestion (Splunk, Elastic, Sumo, etc.).            |

Configure the sink in **Settings → Audit log → External sink** as an
admin. The sink runs as a bounded-channel writer goroutine; if the
sink fails (network, disk full, broken pipe) the failure lands in
`audit_sink_failures` (which is itself append-only and tenant-scoped).
This protects the primary audit chain from a misbehaving downstream.

## Operator playbook — common queries

**"Who has been denied access in the last hour?"**

```sh
curl -fsS "http://localhost:8080/v1/admin/audit-log/unified?since=$(date -u -v-1H +%FT%TZ)&result=deny" \
  -H "Authorization: Bearer $ATLAS_TOKEN" \
  | jq '.events[] | {time: .occurred_at, user: .user_id, action: .action, reason: .reason}'
```

**"Why didn't last night's CI push land?"**

```sh
curl -fsS "http://localhost:8080/v1/admin/audit-log/unified?since=$(date -u -v-1d +%FT%TZ)&audit_log_source=evidence_audit_log" \
  -H "Authorization: Bearer $ATLAS_TOKEN" \
  | jq '.events[] | select(.decision != "accepted")'
```

**"Who froze the SOC 2 Q2 2026 audit period?"**

```sh
curl -fsS "http://localhost:8080/v1/admin/audit-log/unified?action=audit-period.freeze" \
  -H "Authorization: Bearer $ATLAS_TOKEN" \
  | jq '.events[]'
```

## Next steps

- [Evidence →](primitives/evidence.md) — what produces evidence-log
  events
- [First audit →](first-audit.md) — what produces audit-period-log
  events
- [CI hardening →](ci-hardening.md) — what produces decision-log events
  at CI scale

---

## Was this helpful?

Tell us in [GitHub
Discussions](https://github.com/mgoodric/security-atlas/discussions).
