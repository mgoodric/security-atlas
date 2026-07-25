# Grafana connector

The Grafana connector (slice 488) brings alert-rule + notification-policy
configuration into the platform's evidence pipeline — the recurring SOC 2 CC7.2
("the entity monitors system components") evidence demand. It follows the locked
connector pattern verbatim: register-per-run, a stable `actor_id`, an
hour-truncated `observed_at`, scope minimums, and vendor-native read-only auth.
It emits two evidence kinds (the first shared with the Datadog connector):

| Kind                         | Profile      | Source                                                                                                       |
| ---------------------------- | ------------ | ------------------------------------------------------------------------------------------------------------ |
| `monitoring.alert_config.v1` | pull         | Grafana provisioning API `GET /api/v1/provisioning/alert-rules` + `/contact-points`                          |
| `grafana.access_config.v1`   | pull         | Grafana API `GET /api/v1/sso-settings` + `/api/teams/search` + `/api/access-control/assignments` (slice 534) |
| `monitoring.alert_firing.v1` | bounded pull | Grafana alerting state-history API `GET /api/v1/rules/history` (`Viewer` role) — slice 535                   |

The `monitoring.alert_firing.v1` kind (slice 535) is the **firing-history
sibling** of the shared config kind — vendor-neutral, emitted by **both** the
Grafana and Datadog connectors. Slice 488 reads which alerts are **configured**
(CC7.2); this reads what actually **fired** and resolved (CC7.3/CC7.4). One record
per alert-instance state transition: `rule_id` (rule UID), `source_vendor`,
`state` (alerting / resolved / no_data / pending), `fired_at`, `resolved_at`, and
the routing-target **contact-point handle**. Anchors: SCF `MON-01` + `IRO-09`
(the spec candidate IRO-02 is absent from the bundled SCF fixture; IRO-09 is the
closest present incident anchor — see slice-535 decisions log D3).

The `grafana.access_config.v1` kind (slice 534) is the authn/authz surface 488
deferred (P0-488-7): it proves SSO is enforced and access is role-based for SOC 2
CC6.1/CC6.2/CC6.3 — an IAM surface, so it is a **sibling** kind, not a widening of
the monitoring kind. Default SCF anchors `IAC-06` (Authenticator Management) +
`IAC-22` (Least Privilege).

The connector is **API-based**, not an in-host agent — consistent with the
"no closed proprietary collector agents" anti-pattern. It reads the read-only
Grafana API.

The connector reads **configuration + counts only**. For the alert-config
surface: it never collects a contact point's secret settings (where the webhook
URL / integration token / recipient email live), dashboard JSON, metric
time-series, or query results. For the access-config surface: it never collects a
SAML private key, an OAuth client secret, an LDAP bind password, a signing
certificate, or any individual user / team-member / role-assignment identity
(name / email / login) — only enabled flags, provider types, org-role mapping
RULE names, and COUNTS. The Grafana service-account token stays source-side and
never enters an evidence record or a platform push (canvas invariant #3).

## Profile + interval — honest, not "continuous monitoring"

The connector runs on the **pull** profile: each invocation is one bounded
read-and-push pass. It is **operator-scheduled** (cron / scheduler) — the
recommended cadence is **every 24h**. This is deliberately **not** "continuous
monitoring": the interval is named honestly.

The **firing-history** surface (`monitoring.alert_firing.v1`) is a **bounded
pull** over a look-back window (`--grafana-firing-lookback`, default 24h) of the
alerting state-history API. It is **not event-driven**: Grafana's state-history
API is a query surface with no first-class push this connector receives. Same
honest-interval discipline; see the slice-535 decisions log
(`docs/audit-log/535-monitoring-alert-firing-decisions.md`, D1).

## Auth — least-privilege read-only role

The connector authenticates to Grafana with a **service-account token**. The two
surfaces have different least-privilege read minimums:

| Surface       | Minimum read permission                                                       | Why                                                         |
| ------------- | ----------------------------------------------------------------------------- | ----------------------------------------------------------- |
| alert-config  | `Viewer` role                                                                 | list alert rules + contact points (read)                    |
| alert-firing  | `Viewer` role                                                                 | read alert state-history via `GET /api/v1/rules/history`    |
| access-config | `fixed:settings:reader` (`settings:read` on `settings:auth.*`)                | read SSO settings via `GET /api/v1/sso-settings`            |
| access-config | `fixed:roles:reader` (`roles:read` + `users.roles:read` + `teams.roles:read`) | enumerate RBAC role assignments via `/api/access-control/…` |

Run `atlas-grafana permissions` to print this.

**Banned grants.** Do **not** grant the service account `Editor` or `Admin` "to
be safe". Reading SSO settings + RBAC assignments needs only the two read-only
fixed roles above _in addition to_ Viewer — never a write/admin role. The
connector has no write code path, never reads a contact point's secret settings,
and never materializes a SAML private key, an OAuth client secret, an LDAP bind
password, or any user identity.

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

| Flag                        | Subcommand | Required | Default                       | Notes                                                       |
| --------------------------- | ---------- | -------- | ----------------------------- | ----------------------------------------------------------- |
| `--endpoint`                | both       | yes      | env `SECURITY_ATLAS_ENDPOINT` | platform gRPC endpoint                                      |
| `--token`                   | both       | yes      | env `SECURITY_ATLAS_TOKEN`    | security-atlas bearer token                                 |
| `--insecure`                | both       | no       | `false`                       | disables TLS; loopback endpoints only                       |
| `--environment`             | `run`      | yes      | —                             | environment scope tag; records are never emitted un-scoped  |
| `--rule-control`            | `run`      | no       | `scf:MON-01`                  | control id attached to `monitoring.alert_config.v1` records |
| `--access-control`          | `run`      | no       | `scf:IAC-06`                  | control id attached to `grafana.access_config.v1` records   |
| `--firing-control`          | `run`      | no       | `scf:IRO-09`                  | control id attached to `monitoring.alert_firing.v1` records |
| `--grafana-firing-lookback` | `run`      | no       | `24h`                         | bounded look-back window for the alert-firing-history pull  |
| `--grafana-url`             | `run`      | no       | env `GRAFANA_URL`             | Grafana base URL override                                   |

The Grafana token is **only** read from `GRAFANA_TOKEN` — never a CLI flag — so it
never lands in shell history. It is never logged and never enters an evidence
record (the resolved credential redacts the token on every format path).

`register` announces `name=grafana-connector`,
`supported_kinds=[monitoring.alert_config.v1, grafana.access_config.v1, monitoring.alert_firing.v1]`,
and `profiles_supported=[pull]` to `ConnectorRegistryService.Register`.
`profiles_supported` describes how the connector retrieves data **from Grafana**
(a scheduled pull); the platform-side wire is always push (invariant #3).

## Scope minimums

| Scope key     | Value                    |
| ------------- | ------------------------ |
| `service`     | `grafana` (constant)     |
| `environment` | the `--environment` flag |

`run` fails loudly when `--environment` is unset rather than pushing an un-scoped
record.

`source_attribution.actor_id` follows the cross-connector convention:
`connector:grafana:alerts@<version>` for the alert-config records,
`connector:grafana:ssoconfig@<version>` for the access-config records, and
`connector:grafana:firing@<version>` for the alert-firing-history records.

## What the connector never collects (the load-bearing guard)

The alert-config surface collects **rule title / type / enabled state / folder +
the contact point NAME each rule routes to**; the access-config surface collects
**SSO enabled flags / provider types / org-role mapping RULE names / team COUNTS
/ RBAC role-assignment COUNTS**. It never reads, materializes, or emits:

- a contact point's `settings` blob (the secret webhook URL / integration token /
  recipient email address live there — the client never decodes it)
- a SAML private key, an OAuth client secret, an LDAP bind password, or a signing
  certificate (the `sso-settings` `settings` blob carries them — the decode
  struct omits every secret key, so `json.Decode` discards them)
- any individual user / team-member / role-assignment identity (name / email /
  login) — team membership and RBAC assignments are reported as COUNTS only
- dashboard JSON / metric time-series / query results

The guard is **structural**: the secret-free record types (`ContactPoint`,
`RawProvider` / `RawTeamStats` / `RawRoleStats` / `Provider` / `AccessConfig`)
have no field that can hold a secret or an identity, and a reflection test
(`TestStructuralOverCollectionGuard`) fails the build the moment one is added. A
drop test (`TestClient_NeverLeaksSecretOrPII`) feeds a fake response embedding a
SAML private key, OAuth client secret, LDAP bind password, signing cert, and a
user email/login/name, and asserts none reaches a record. The Grafana token never
appears in any formatted credential.

The **firing-history** surface (`monitoring.alert_firing.v1`) carries the same
structural guard at the shared `firing.RawFiring`/`firing.Firing` layer: those
structs hold only `rule_id` / `state` / `fired_at` / `resolved_at` / the
contact-point handle — no field for the alert **annotation/message body**, the
triggering **metric values** (the state-history line's `values` map), the secret
**contact-point settings**, or recipient PII. The state-history client decodes
only the rule UID, the state, the timestamp, and the contact-point label;
`TestStructuralOverCollectionGuard` (in `connectors/monitoring/firing`) pins the
field set, and the Grafana drop test feeds a state-history line carrying an
annotation body, a `values` map, a webhook secret, and a recipient-email label and
asserts none reaches a record. The read is **bounded** (`maxTransitions=5000`, a
60s run timeout, and the `--grafana-firing-lookback` window) — the load-bearing
DoS bound, since a flapping alert can transition thousands of times.

## Not in v0 (follow-ons)

- a genuine event-driven `subscribe` profile if/when Grafana ships a state-history
  webhook the connector can receive (today it is a query surface — bounded pull)

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
