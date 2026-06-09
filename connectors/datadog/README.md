# Datadog connector

The Datadog connector (slice 488) brings monitor / alert configuration into the
platform's evidence pipeline — the recurring SOC 2 CC7.2 ("the entity monitors
system components") evidence demand. It follows the locked connector pattern
verbatim: register-per-run, a stable `actor_id`, an hour-truncated `observed_at`,
scope minimums, and vendor-native read-only auth. It emits two evidence kinds:

| Kind                         | Profile | Source                                                                                                   |
| ---------------------------- | ------- | -------------------------------------------------------------------------------------------------------- |
| `monitoring.alert_config.v1` | pull    | Datadog API `GET /api/v1/monitor` (`monitors_read` scope)                                                |
| `datadog.siem_rule.v1`       | pull    | Datadog API `GET /api/v2/security_monitoring/rules` (`security_monitoring_rules_read` scope) — slice 533 |

`monitoring.alert_config.v1` is shared with the Grafana connector.
`datadog.siem_rule.v1` is Datadog-specific: a Cloud-SIEM detection rule carries a
**severity** + a **detection-class** field (log / signal_correlation /
threshold) the operational alert-config shape lacks, so it gets its own sibling
kind rather than widening the shared monitoring kind (the slice-488 D1 split).
It serves the SOC 2 CC7.2/CC7.3 + ISO A.12 "prove threat-detection rules are
configured" evidence demand.

The connector is **API-based**, not an in-host agent — consistent with the
"no closed proprietary collector agents" anti-pattern. It reads the read-only
Datadog API.

The connector reads **configuration only**. It never collects the secret webhook
URL behind an integration, an integration token, a recipient email address, the
monitor query, the SIEM detection query, **firing signals, matched log samples,
matched-event payloads**, dashboard JSON, or metric time-series. The Datadog
API+APP keys stay source-side and never enter an evidence record or a platform
push (canvas invariant #3).

## Profile + interval — honest, not "continuous monitoring"

The connector runs on the **pull** profile: each invocation is one bounded
read-and-push pass. It is **operator-scheduled** (cron / scheduler) — the
recommended cadence is **every 24h**. This is deliberately **not** "continuous
monitoring": the interval is named honestly. A webhook / event-driven profile is
a documented follow-on, not part of v0.

## Auth — least-privilege read-only scope

The connector authenticates to Datadog with a **pair** of keys: an API key
(`DD-API-KEY`) and an Application key (`DD-APPLICATION-KEY`). The Application key
carries the authorization scope; create it with **exactly** the read-only
`monitors_read` + `security_monitoring_rules_read` scopes.

| Credential      | Minimum scope                    | Why                                         |
| --------------- | -------------------------------- | ------------------------------------------- |
| Application key | `monitors_read`                  | list monitors (read-only)                   |
| Application key | `security_monitoring_rules_read` | list Cloud-SIEM detection rules (read-only) |
| API key         | (no scope; org-level)            | required alongside the Application key      |

Run `atlas-datadog permissions` to print this.

**Banned grants.** Do **not** create the Application key with `monitors_write`,
`security_monitoring_rules_write`, admin, or any broad scope "to be safe." The
connector has no write code path; the only operations it issues are read
`GET /api/v1/monitor` and `GET /api/v2/security_monitoring/rules`.

## Subcommands

```sh
# Announce this connector instance to the platform.
atlas-datadog register \
  --endpoint platform.example.com:443 \
  --token "$SECURITY_ATLAS_TOKEN"

# Read Datadog monitor inventory, push evidence records.
# The Datadog keys are read from the environment (never the CLI, so they stay
# out of shell history):
export DATADOG_API_KEY=<api-key>
export DATADOG_APP_KEY=<monitors_read-scoped-app-key>
export DATADOG_SITE=datadoghq.com   # or datadoghq.eu / us3.datadoghq.com / ...

atlas-datadog run \
  --endpoint platform.example.com:443 \
  --token "$SECURITY_ATLAS_TOKEN" \
  --environment prod

# Print the least-privilege scope.
atlas-datadog permissions
```

| Flag                | Subcommand | Required | Default                              | Notes                                                       |
| ------------------- | ---------- | -------- | ------------------------------------ | ----------------------------------------------------------- |
| `--endpoint`        | both       | yes      | env `SECURITY_ATLAS_ENDPOINT`        | platform gRPC endpoint                                      |
| `--token`           | both       | yes      | env `SECURITY_ATLAS_TOKEN`           | security-atlas bearer token                                 |
| `--insecure`        | both       | no       | `false`                              | disables TLS; loopback endpoints only                       |
| `--environment`     | `run`      | yes      | —                                    | environment scope tag; records are never emitted un-scoped  |
| `--monitor-control` | `run`      | no       | `scf:MON-01`                         | control id attached to `monitoring.alert_config.v1` records |
| `--siem-control`    | `run`      | no       | `scf:THR-01`                         | control id attached to `datadog.siem_rule.v1` records       |
| `--site`            | `run`      | no       | env `DATADOG_SITE` / `datadoghq.com` | Datadog site override                                       |

The Datadog keys are **only** read from `DATADOG_API_KEY` / `DATADOG_APP_KEY` —
never a CLI flag — so they never land in shell history. They are never logged and
never enter an evidence record (the resolved credential redacts both keys on
every format path).

`register` announces `name=datadog-connector`,
`supported_kinds=[monitoring.alert_config.v1, datadog.siem_rule.v1]`, and
`profiles_supported=[pull]` to `ConnectorRegistryService.Register`.
`profiles_supported` describes how the connector retrieves data **from Datadog**
(a scheduled pull); the platform-side wire is always push (invariant #3).

## Scope minimums

Every emitted record sets the minimum scope dimensions the connector-pattern
convention requires:

| Scope key     | Value                    |
| ------------- | ------------------------ |
| `service`     | `datadog` (constant)     |
| `environment` | the `--environment` flag |

`run` fails loudly when `--environment` is unset rather than pushing an un-scoped
record.

`source_attribution.actor_id` follows the cross-connector convention
`connector:datadog:<service>@<version>` (`monitors` for the monitor surface,
`siemrules` for the SIEM-rule surface), where `<version>` is the build's module
version (or `dev` under `go run`).

## What the connector never collects (the load-bearing guard)

The connector collects **rule name / type-or-detection-class / enabled state +
severity (SIEM only) + notification TARGET HANDLES only**. It never reads,
materializes, or emits:

- the secret webhook URL behind an integration
- integration tokens
- recipient email addresses (an `@user@example.com` mention is **dropped** —
  recipient PII never becomes a target)
- the monitor query / the SIEM **detection query**
- **firing signals, the detection query's matched raw log samples, or matched-
  event payloads** (Cloud-SIEM)
- dashboard JSON / metric time-series / log query results

For the SIEM surface this is a **structural** guard: the collector's `RawRule` /
`Rule` structs have no field capable of holding a signal, a log sample, a
matched event, a secret target, or the raw query — a reflection test
(`TestStructuralOverCollectionGuard`) pins the field set, so the struct
physically cannot over-collect. Tests assert no secret URL / token / recipient
email enters a record, and that the Datadog keys never appear in any formatted
credential.

## Bounded reads — DoS / over-collection guard

The SIEM-rule surface reads the cursor-paginated
`GET /api/v2/security_monitoring/rules` with a **bounded page loop**: a hard
per-run page cap (`maxPages=50` × `pageSize=100` ⇒ 5,000 rules) and a 60s run
timeout. If the rule set exceeds the cap the run stops and reports
`ErrRuleCapExceeded` honestly rather than reading unbounded. (The monitor
surface reads a bounded first page, `page_size=1000`.)

## Not in v0 (follow-ons)

- SIEM signal-history (event-driven) profile (slice 635-band follow-on)
- rule-suppression / exclusion-list evidence (slice 635-band follow-on)
- alert-firing-history (event-driven) profile (slice 535)

## Tests

```sh
go test ./connectors/datadog/...
```

Unit tests fake the Datadog API (no live Datadog, no real keys) and pin the
monitor normalization, the handle parser + email-recipient drop, the credential
redaction, and the read-only scope contract. The in-package integration test
(bufconn platform — no Postgres) exercises the full collect → build → SDK `Push`
→ receipt round-trip, asserts two same-hour pushes collapse to one `record_id`,
that emitted payloads carry config + target-name metadata only, and that the
credential never surfaces in a formatted log.
