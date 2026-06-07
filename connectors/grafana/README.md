# Grafana connector

The Grafana connector (slice 488) brings alert-rule + notification-policy
configuration into the platform's evidence pipeline — the recurring SOC 2 CC7.2
("the entity monitors system components") evidence demand. It follows the locked
connector pattern verbatim: register-per-run, a stable `actor_id`, an
hour-truncated `observed_at`, scope minimums, and vendor-native read-only auth.
It emits one evidence kind, shared with the Datadog connector:

| Kind                         | Profile | Source                                                                              |
| ---------------------------- | ------- | ----------------------------------------------------------------------------------- |
| `monitoring.alert_config.v1` | pull    | Grafana provisioning API `GET /api/v1/provisioning/alert-rules` + `/contact-points` |

The connector is **API-based**, not an in-host agent — consistent with the
"no closed proprietary collector agents" anti-pattern. It reads the read-only
Grafana provisioning API.

The connector reads **alert-rule + notification-policy configuration only**. It
never collects a contact point's secret settings (where the webhook URL /
integration token / recipient email live), dashboard JSON, metric time-series, or
query results. The Grafana service-account token stays source-side and never
enters an evidence record or a platform push (canvas invariant #3).

## Profile + interval — honest, not "continuous monitoring"

The connector runs on the **pull** profile: each invocation is one bounded
read-and-push pass. It is **operator-scheduled** (cron / scheduler) — the
recommended cadence is **every 24h**. This is deliberately **not** "continuous
monitoring": the interval is named honestly. An event-driven profile is a
documented follow-on, not part of v0.

## Auth — least-privilege read-only role

The connector authenticates to Grafana with a **service-account token**. Create
the service account with **exactly** the read-only **Viewer** role.

| Credential            | Minimum role | Why                                      |
| --------------------- | ------------ | ---------------------------------------- |
| Service-account token | `Viewer`     | list alert rules + contact points (read) |

Run `atlas-grafana permissions` to print this.

**Banned grants.** Do **not** grant the service account `Editor` or `Admin`. The
Viewer role can list alert rules + contact points; the connector has no write
code path and never reads a contact point's secret settings.

## Subcommands

```sh
# Announce this connector instance to the platform.
atlas-grafana register \
  --endpoint platform.example.com:443 \
  --token "$SECURITY_ATLAS_TOKEN"

# Read Grafana alert-rule + contact-point inventory, push evidence records.
# The Grafana token is read from the environment (never the CLI, so it stays out
# of shell history):
export GRAFANA_URL=https://grafana.example.com
export GRAFANA_TOKEN=<viewer-role-service-account-token>

atlas-grafana run \
  --endpoint platform.example.com:443 \
  --token "$SECURITY_ATLAS_TOKEN" \
  --environment prod

# Print the least-privilege role.
atlas-grafana permissions
```

| Flag             | Subcommand | Required | Default                       | Notes                                                      |
| ---------------- | ---------- | -------- | ----------------------------- | ---------------------------------------------------------- |
| `--endpoint`     | both       | yes      | env `SECURITY_ATLAS_ENDPOINT` | platform gRPC endpoint                                     |
| `--token`        | both       | yes      | env `SECURITY_ATLAS_TOKEN`    | security-atlas bearer token                                |
| `--insecure`     | both       | no       | `false`                       | disables TLS; loopback endpoints only                      |
| `--environment`  | `run`      | yes      | —                             | environment scope tag; records are never emitted un-scoped |
| `--rule-control` | `run`      | no       | `scf:MON-01`                  | control id attached to records                             |
| `--grafana-url`  | `run`      | no       | env `GRAFANA_URL`             | Grafana base URL override                                  |

The Grafana token is **only** read from `GRAFANA_TOKEN` — never a CLI flag — so it
never lands in shell history. It is never logged and never enters an evidence
record (the resolved credential redacts the token on every format path).

`register` announces `name=grafana-connector`,
`supported_kinds=[monitoring.alert_config.v1]`, and `profiles_supported=[pull]`
to `ConnectorRegistryService.Register`. `profiles_supported` describes how the
connector retrieves data **from Grafana** (a scheduled pull); the platform-side
wire is always push (invariant #3).

## Scope minimums

| Scope key     | Value                    |
| ------------- | ------------------------ |
| `service`     | `grafana` (constant)     |
| `environment` | the `--environment` flag |

`run` fails loudly when `--environment` is unset rather than pushing an un-scoped
record.

`source_attribution.actor_id` follows the cross-connector convention
`connector:grafana:alerts@<version>`.

## What the connector never collects (the load-bearing guard)

The connector collects **rule title / type / enabled state / folder + the contact
point NAME each rule routes to**. It never reads, materializes, or emits:

- a contact point's `settings` blob (the secret webhook URL / integration token /
  recipient email address live there — the client never decodes it)
- dashboard JSON / metric time-series / query results

Tests assert no contact-point secret enters an evidence record (the
`ContactPoint` struct has no `settings` field, so it cannot), and that the
Grafana token never appears in any formatted credential.

## Not in v0 (follow-ons)

- Grafana SAML / RBAC config evidence (slice 534)
- alert-firing-history (event-driven) profile (slice 535)

## Tests

```sh
go test ./connectors/grafana/...
```

Unit tests fake the Grafana provisioning API (no live Grafana, no real token) and
pin the alert-rule + contact-point join, the receiver-name → target mapping, the
credential redaction, and the read-only role contract. The in-package
integration test (bufconn platform — no Postgres) exercises the full collect →
build → SDK `Push` → receipt round-trip, asserts two same-hour pushes collapse to
one `record_id`, that emitted payloads carry config + contact-point-name metadata
only, and that the credential never surfaces in a formatted log.
