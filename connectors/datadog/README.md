# Datadog connector

The Datadog connector (slice 488) brings monitor / alert configuration into the
platform's evidence pipeline — the recurring SOC 2 CC7.2 ("the entity monitors
system components") evidence demand. It follows the locked connector pattern
verbatim: register-per-run, a stable `actor_id`, an hour-truncated `observed_at`,
scope minimums, and vendor-native read-only auth. It emits three evidence kinds:

| Kind                         | Profile      | Source                                                                                                       |
| ---------------------------- | ------------ | ------------------------------------------------------------------------------------------------------------ |
| `monitoring.alert_config.v1` | pull         | Datadog API `GET /api/v1/monitor` (`monitors_read` scope)                                                    |
| `datadog.siem_rule.v1`       | pull         | Datadog API `GET /api/v2/security_monitoring/rules` (`security_monitoring_rules_read` scope) — slice 533     |
| `datadog.siem_signal.v1`     | bounded pull | Datadog API `GET /api/v2/security_monitoring/signals` (`security_monitoring_signals_read` scope) — slice 636 |

`monitoring.alert_config.v1` is shared with the Grafana connector.
`datadog.siem_rule.v1` is Datadog-specific: a Cloud-SIEM detection rule carries a
**severity** + a **detection-class** field (log / signal_correlation /
threshold) the operational alert-config shape lacks, so it gets its own sibling
kind rather than widening the shared monitoring kind (the slice-488 D1 split).
It serves the SOC 2 CC7.2 + ISO A.12 "prove threat-detection rules are
configured" evidence demand.

`datadog.siem_signal.v1` (slice 636) is the CC7.3 sibling of the rule kind:
slice 533 reads detection-rule **configuration** (CC7.2 — which rules exist);
this surface reads what **fired** and how it was **triaged** (CC7.3 incident
response — "rules actually fired and were triaged over the audit period, when,
and by whom"). It carries triage **metadata only** — the signal id, firing rule
id, severity, triage status, timeline timestamps, and the opaque triager handle.
Anchors: SCF **THR-01** (detection lineage) + **IRO-09** (incident reporting).

The connector is **API-based**, not an in-host agent — consistent with the
"no closed proprietary collector agents" anti-pattern. It reads the read-only
Datadog API.

The connector reads **configuration + triage metadata only**. It never collects
the secret webhook URL behind an integration, an integration token, a recipient
email address, the monitor query, the SIEM detection query, a **signal message
body, matched log samples, matched-event payloads, or signal-body tags**,
dashboard JSON, or metric time-series. The Datadog API+APP keys stay source-side
and never enter an evidence record or a platform push (canvas invariant #3).

## Profile + interval — honest, not "continuous monitoring"

The connector runs on the **pull** profile: each invocation is one bounded
read-and-push pass. It is **operator-scheduled** (cron / scheduler) — the
recommended cadence is **every 24h**. This is deliberately **not** "continuous
monitoring": the interval is named honestly.

The signal-history surface (`datadog.siem_signal.v1`) is a **bounded pull** over
a look-back window (`--siem-lookback`, default 24h) of the security-signals
search API. It is **not event-driven**: Datadog's security-signals API is a
search/poll surface and offers no first-class push this connector receives
(signal notifications route to the operator's own Slack/PagerDuty/webhook
integrations, not back to this connector). The window is named honestly; the
platform-side wire stays push (invariant #3). See the slice-636 decisions log
(`docs/audit-log/636-datadog-siem-signal-history-decisions.md`, D2) for the
profile-shape rationale.

## Auth — least-privilege read-only scope

The connector authenticates to Datadog with a **pair** of keys: an API key
(`DD-API-KEY`) and an Application key (`DD-APPLICATION-KEY`). The Application key
carries the authorization scope; create it with **exactly** the read-only
`monitors_read` + `security_monitoring_rules_read` +
`security_monitoring_signals_read` scopes.

| Credential      | Minimum scope                      | Why                                         |
| --------------- | ---------------------------------- | ------------------------------------------- |
| Application key | `monitors_read`                    | list monitors (read-only)                   |
| Application key | `security_monitoring_rules_read`   | list Cloud-SIEM detection rules (read-only) |
| Application key | `security_monitoring_signals_read` | list Cloud-SIEM signal history (read-only)  |
| API key         | (no scope; org-level)              | required alongside the Application key      |

Run `atlas-datadog permissions` to print this.

**Banned grants.** Do **not** create the Application key with `monitors_write`,
`security_monitoring_rules_write`, `security_monitoring_signals_write`, admin, or
any broad scope "to be safe." The connector has no write code path; the only
operations it issues are read `GET /api/v1/monitor`,
`GET /api/v2/security_monitoring/rules`, and
`GET /api/v2/security_monitoring/signals`.

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

| Flag                    | Subcommand | Required | Default                              | Notes                                                       |
| ----------------------- | ---------- | -------- | ------------------------------------ | ----------------------------------------------------------- |
| `--endpoint`            | both       | yes      | env `SECURITY_ATLAS_ENDPOINT`        | platform gRPC endpoint                                      |
| `--token`               | both       | yes      | env `SECURITY_ATLAS_TOKEN`           | security-atlas bearer token                                 |
| `--insecure`            | both       | no       | `false`                              | disables TLS; loopback endpoints only                       |
| `--environment`         | `run`      | yes      | —                                    | environment scope tag; records are never emitted un-scoped  |
| `--monitor-control`     | `run`      | no       | `scf:MON-01`                         | control id attached to `monitoring.alert_config.v1` records |
| `--siem-control`        | `run`      | no       | `scf:THR-01`                         | control id attached to `datadog.siem_rule.v1` records       |
| `--siem-signal-control` | `run`      | no       | `scf:IRO-09`                         | control id attached to `datadog.siem_signal.v1` records     |
| `--siem-lookback`       | `run`      | no       | `24h`                                | bounded look-back window for the signal-history pull        |
| `--site`                | `run`      | no       | env `DATADOG_SITE` / `datadoghq.com` | Datadog site override                                       |

The Datadog keys are **only** read from `DATADOG_API_KEY` / `DATADOG_APP_KEY` —
never a CLI flag — so they never land in shell history. They are never logged and
never enter an evidence record (the resolved credential redacts both keys on
every format path).

`register` announces `name=datadog-connector`,
`supported_kinds=[monitoring.alert_config.v1, datadog.siem_rule.v1, datadog.siem_signal.v1]`,
and `profiles_supported=[pull]` to `ConnectorRegistryService.Register`.
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
`siemrules` for the SIEM-rule surface, `siemsignals` for the signal-history
surface), where `<version>` is the build's module version (or `dev` under
`go run`).

## What the connector never collects (the load-bearing guard)

The connector collects **rule name / type-or-detection-class / enabled state +
severity + notification TARGET HANDLES** (rules) and **signal id / firing rule
id / severity / triage status / timeline timestamps / opaque triager handle**
(signals) only. It never reads, materializes, or emits:

- the secret webhook URL behind an integration
- integration tokens
- recipient email addresses (an `@user@example.com` mention is **dropped** —
  recipient PII never becomes a target) and triager email addresses (an
  email-shaped triager value is **dropped** — never enters a signal record)
- the monitor query / the SIEM **detection query**
- a **signal message body, the detection query's matched raw log samples,
  matched-event payloads, or signal-body tags/facets** (Cloud-SIEM)
- dashboard JSON / metric time-series / log query results

For both SIEM surfaces this is a **structural** guard: the collector's
`RawRule`/`Rule` (rules) and `RawSignal`/`Signal` (signals) structs have no
field capable of holding a signal message, a log sample, a matched event, a
secret target, a body tag, or the raw query — a reflection test
(`TestStructuralOverCollectionGuard` in each package) pins the field set, so the
struct physically cannot over-collect. Tests feed a fixture containing a message,
samples, a raw query, and a `user.email` tag and assert none reaches an emitted
record.

## Bounded reads — DoS / over-collection guard

Both SIEM surfaces read the cursor-paginated Datadog v2 API with a **bounded
page loop**: a hard per-run page cap (`maxPages=50` × `pageSize=100` ⇒ 5,000
records) and a 60s run timeout. If the set exceeds the cap the run stops and
reports `ErrRuleCapExceeded` / `ErrSignalCapExceeded` honestly rather than
reading unbounded. The signal surface additionally bounds the read by a
look-back window (`--siem-lookback`, default 24h: `filter[from]=now-lookback`).
(The monitor surface reads a bounded first page, `page_size=1000`.)

## Not in v0 (follow-ons)

- rule-suppression / exclusion-list evidence (slice 635-band follow-on)
- alert-firing-history (event-driven) profile (slice 535)
- signal triage-note / disposition-reason metadata (a separate slice with its
  own PII review — deliberately out of slice 636's metadata-only boundary)

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
