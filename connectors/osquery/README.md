# osquery / Fleet endpoint connector

Emits one evidence kind:

| Kind                      | Profile | Source                                                                    |
| ------------------------- | ------- | ------------------------------------------------------------------------- |
| `osquery.host_posture.v1` | pull    | Fleet REST API (`/api/v1/fleet/hosts`) OR local osqueryd extension socket |

Schema reused unchanged from slice 014; no new schemas in this slice.

osquery is the agent. The connector is a read-only consumer of either
Fleet's REST surface or a local osqueryd extension socket. Canvas §1.6
explicitly rejects "proprietary collector agents on endpoints" as an
anti-pattern; this connector honours that rule by riding the open
osquery agent that ops teams already operate.

## Modes

The `run` subcommand selects one upstream via `--mode`:

| Mode    | Auth                      | Notes                                                                                                                                                                                                                                                 |
| ------- | ------------------------- | ----------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- |
| `fleet` | `Authorization: Bearer …` | Default. Two-call pull: list hosts then per-host detail (the detail call is where the boolean policy fields live).                                                                                                                                    |
| `local` | filesystem permission     | Dials a root-owned Unix socket. Slice 047 wires the configuration surface; the in-process transport returns `ErrLocalSocketNotWired` so callers get a clear "use fleet mode" rather than a silent fallthrough. A follow-up slice lands the transport. |

## Auth — least-privilege Fleet roles

Fleet uses role-based access. The connector requires only **read** roles.
The `atlas-osquery scopes` subcommand prints this list at runtime.

| Token kind                           | Role                       | Access | Gates                                                             |
| ------------------------------------ | -------------------------- | ------ | ----------------------------------------------------------------- |
| Fleet API token (observer role)      | `observer` (global)        | Read   | `osquery.host_posture.v1` (GET /api/v1/fleet/hosts + /hosts/{id}) |
| Fleet API token (observer_plus role) | `observer_plus` (per-team) | Read   | `osquery.host_posture.v1` when scoped to a single Fleet team      |

**Banned roles:** `admin`, `maintainer`. The `DocumentedScopes` unit test
rejects any future widening that includes `write`, `delete`, `admin`, or
`maintainer` keywords in either the Access or Name field.

## Subcommands

```sh
# Announce this connector instance to the platform.
atlas-osquery register \
  --endpoint platform.example.com:443 \
  --token "$SECURITY_ATLAS_TOKEN"

# Pull host posture from Fleet and push evidence.
FLEET_API_TOKEN=... \
atlas-osquery run \
  --endpoint platform.example.com:443 \
  --token "$SECURITY_ATLAS_TOKEN" \
  --mode fleet \
  --fleet-base-url https://fleet.example.com \
  --org example \
  --environment prod \
  --firewall-policy "Firewall enabled (macOS)" \
  --screenlock-policy "Screen lock enabled (macOS)" \
  --antimalware-policy "Anti-malware current" \
  --edr-policy "EDR healthy" \
  --production-label "Production" \
  --antimalware-definitions-report "AV definitions" \
  --antimalware-definitions-column last_updated

# Local osqueryd socket mode (configuration surface only in slice 047).
atlas-osquery run \
  --endpoint platform.example.com:443 \
  --token "$SECURITY_ATLAS_TOKEN" \
  --mode local \
  --osqueryd-socket /var/osquery/osquery.em \
  --org example \
  --environment prod

# Print the canonical least-privilege Fleet roles.
atlas-osquery scopes
```

## Evidence shape

One `osquery.host_posture.v1` record per host, at schema 1.4.0
(additive over 1.3.0, #2936; 1.3.0 over 1.2.0, #2826; 1.2.0 over 1.1.0,
#2921; 1.1.0 over 1.0.0, #2912):

- `host_uuid` (required)
- `hostname` (required)
- `platform`
- `os_version`
- `disk_encryption_enabled`
- `screen_lock_enabled`
- `firewall_enabled`
- `mdm_enrolled`
- `last_seen_at` (1.1.0): the host's own last check-in, Fleet's `seen_time`
- `reason` (1.1.0): why the result is what it is
- `firewall_source` (1.2.0): `fleet_policy:<name>`, present only with `firewall_enabled`
- `screen_lock_source` (1.2.0): `fleet_policy:<name>`, present only with `screen_lock_enabled`
- `disk_used_percent` (1.3.0): 0-100
- `host_role` (1.3.0): `production`, from the `--production-label` label
- `host_role_source` (1.3.0): `fleet_label:<name>`, present only with `host_role`
- `antimalware_protected` (1.3.0), with `antimalware_source` (`fleet_policy:<name>`)
- `edr_healthy` (1.3.0), with `edr_source` (`fleet_policy:<name>`)
- `antimalware_definitions_updated_at` (1.4.0): RFC 3339 UTC, with
  `antimalware_definitions_source` (`fleet_report:<name>`), present only
  with the date

Schema 1.3.0 also declares `memory_used_percent` and `cpu_p95_5m_percent`,
which this connector never emits: Fleet's host API has no memory-use or
CPU-use field, and a 5-minute p95 needs sampling a snapshot can't give
(#2935). Another pusher may supply them. From 1.3.0 every `*_source` is
valid only alongside its value (`dependentRequired`).

Schema is `additionalProperties: false` — the connector emits only the
declared fields. The slice's issue spec mentions `os_patch_level`; it is
not in the schema and is not emitted here.

**A posture boolean is present only when the connector read it.** `false`
means observed off; an absent key means not read. In Fleet mode:

| Field                     | Source in Fleet's Get host response                                             |
| ------------------------- | ------------------------------------------------------------------------------- |
| `disk_encryption_enabled` | `disk_encryption_enabled`; Fleet omits it when unknown, and so does the record  |
| `mdm_enrolled`            | `mdm.enrollment_status`: `On (...)` is true, `Off`/`Pending` false, else absent |
| `last_seen_at`            | `seen_time` (from the list, refined by the detail call)                         |
| `screen_lock_enabled`     | `policies[]`: the `--screenlock-policy` policy's `response` (see below)         |
| `firewall_enabled`        | `policies[]`: the `--firewall-policy` policy's `response` (see below)           |
| `antimalware_protected`   | `policies[]`: the `--antimalware-policy` policy's `response` (see below)        |
| `edr_healthy`             | `policies[]`: the `--edr-policy` policy's `response` (see below)                |
| `disk_used_percent`       | 100 − `percent_disk_space_available`; absent if `gigs_total_disk_space` is 0    |
| `host_role`               | `labels[]`: `production` when the `--production-label` label is on the host     |

`antimalware_definitions_updated_at` is not in Get host; it comes from a
Fleet report (see "Anti-malware definitions date" below).

Without `--screenlock-policy`, a Fleet host's `result` is `fail` (disk
encryption off) or `inconclusive` (screen lock not reported), never `pass`.
A host whose detail fetch fails is `inconclusive` with no posture booleans
at all.

`observed_at` is the connector's pull time, hour-truncated. It is not the
host's last check-in (Fleet lists offline hosts too); read `last_seen_at`
for that. It cannot be `seen_time` because ingest rejects an `observed_at`
more than 24 h from receipt.

### Firewall, screen lock, antimalware and EDR from Fleet policies (#2921, #2826)

Fleet's host API reports no firewall, screen-lock, antimalware or EDR
state. It does report
each host's result for every Fleet policy that applies to it, in the Get
host response's `policies[]` array (`name`, `response`). So the operator
creates one Fleet policy per question, and names it on the `run` command:

| Flag                   | Field it sets           | Source written to the record              |
| ---------------------- | ----------------------- | ----------------------------------------- |
| `--firewall-policy`    | `firewall_enabled`      | `firewall_source: fleet_policy:<name>`    |
| `--screenlock-policy`  | `screen_lock_enabled`   | `screen_lock_source: fleet_policy:<name>` |
| `--antimalware-policy` | `antimalware_protected` | `antimalware_source: fleet_policy:<name>` |
| `--edr-policy`         | `edr_healthy`           | `edr_source: fleet_policy:<name>`         |

Per host, the named policy's `response` maps as:

| `response`                            | Field   |
| ------------------------------------- | ------- |
| `"pass"`                              | `true`  |
| `"fail"`                              | `false` |
| `""` (no result yet for this host)    | omitted |
| no policy of that name on the host    | omitted |
| two policies of that name that differ | omitted |

Names match exactly, including case and spaces. A policy targeted at one
platform only (say, macOS) has no entry on other hosts, so their field is
omitted, not false. To cover a mixed fleet with one flag, write one policy
whose query handles every platform, or run the connector once per
platform-specific Fleet team. The flags apply to `--mode=fleet` only;
`--mode=local` rejects them.

The connector does not create or edit the policies (it holds a read-only
token, and the anti-criteria forbid mutating Fleet). The operator owns
the query, and whatever it checks is what `true` means.

**Example policy queries.** The macOS and Windows queries are verbatim
from Fleet's standard query library
(`docs/01-Using-Fleet/standard-query-library/standard-query-library.yml`
in `fleetdm/fleet`, read 2026-11-21), where they are named "Firewall
enabled (macOS)", "Screen lock enabled (macOS)", "Firewall enabled, domain
profile (Windows)" and "Screen lock enabled (Windows)". None has been run
against a live host from this repo.

macOS firewall. osquery's `alf.global_state` is 1 with exceptions, 2 for
block-all, else 0:

```sql
SELECT 1 FROM alf WHERE global_state >= 1;
```

macOS screen lock. Fleet tags this "MDM required": it reads the managed
`askForPassword` setting, so it fails on a Mac without that profile even
if the user turned screen lock on by hand:

```sql
SELECT 1 FROM managed_policies WHERE name='askForPassword' AND value='1';
```

Windows firewall. Fleet's library checks the Group Policy registry key
for each profile (domain, private, public); this is the domain one. A
firewall turned on outside Group Policy does not set this key:

```sql
SELECT 1 FROM registry WHERE path LIKE 'HKEY_LOCAL_MACHINE\Software\Policies\Microsoft\WindowsFirewall\DomainProfile\EnableFirewall' AND CAST(data as integer) = 1;
```

Windows screen lock (machine inactivity limit of 30 minutes or less):

```sql
SELECT 1 FROM registry WHERE path = 'HKEY_LOCAL_MACHINE\Software\Microsoft\Windows\CurrentVersion\Policies\System\InactivityTimeoutSecs' AND CAST(data as INTEGER) <= 1800;
```

Linux screen lock. Fleet's library has an Ubuntu GNOME query ("Ubuntu
GNOME lock screen after 5 minutes") that reads the dconf system database
through `file_lines`. Copy it from the library rather than from here.

Linux firewall. Fleet's library has no Linux firewall policy, so this one
is ours and unverified on a live host. It uses only documented osquery
tables (`file_lines`, and `systemd_units` with its `id` and
`active_state` columns) and passes when ufw is enabled in its config or
firewalld's unit is active:

```sql
SELECT 1 WHERE
  EXISTS (SELECT 1 FROM file_lines WHERE path = '/etc/ufw/ufw.conf' AND trim(line) = 'ENABLED=yes')
  OR EXISTS (SELECT 1 FROM systemd_units WHERE id = 'firewalld.service' AND active_state = 'active');
```

**Antimalware and EDR policies.** What passes is the operator's to
define, and the stock controls take the policy's word for it:
`soc2_cc6_8_malware_protection` expects the antimalware policy to pass
only when an engine is active **and** its definitions were updated within
7 days, and `soc2_cc7_2_anomaly_detection` expects the EDR policy to pass
only when the agent is installed, running and healthy. Write the query
for the engine and agent the org runs. Unless a definitions report is
named (next section), the 7-day bound lives only in the policy query.

An unverified Windows starting point, not taken from Fleet's library and
not run against a live host: osquery's `windows_security_products` table
lists Security Center products with `type`, `state` and
`signatures_up_to_date`.

```sql
SELECT 1 FROM windows_security_products
WHERE type = 'Antivirus' AND state = 'On' AND signatures_up_to_date = 1;
```

`signatures_up_to_date` is Security Center's own judgement, not a 7-day
bound; tighten it if the org needs the exact bound. EDR agents differ too
much for a generic example: check the vendor's own health signal (a
running service or process plus its status file or registry key).

### Anti-malware definitions date from a Fleet report (#2936)

No osquery table reports when Defender, XProtect or Falcon definitions
were last updated (the osquery table specs were checked on 2026-11-22;
`windows_security_products` has only the `signatures_up_to_date` flag),
and Fleet's Get host has no such field. So the date comes from a query the
operator writes. Save it as a Fleet report (a saved query whose per-host
results Fleet stores, `discard_data: false`), and name the report and the
result column that holds the date:

| Flag                               | What it names                                 |
| ---------------------------------- | --------------------------------------------- |
| `--antimalware-definitions-report` | the report, by exact name                     |
| `--antimalware-definitions-column` | the column of its results that holds the date |

The two are set together, and apply to `--mode=fleet` only. Per host, the
connector finds the report with `GET /api/v1/fleet/hosts/{id}/reports`
(exact name match, after Fleet's keyword search) and reads its rows with
`GET /api/v1/fleet/hosts/{id}/reports/{report_id}`:

| Host's report                                         | Record                                                |
| ----------------------------------------------------- | ----------------------------------------------------- |
| one row, column is RFC 3339 with a zone or epoch secs | `antimalware_definitions_updated_at` (UTC)            |
| several rows                                          | the oldest readable one; `reason` counts skipped rows |
| a row missing the column, or not a date               | skipped, and named in `reason`                        |
| a row in epoch milliseconds (over `1e11`)             | skipped, and named in `reason`                        |
| a row more than 1 h after the connector's pull time   | skipped, and named in `reason`                        |
| no rows, no report of that name, or two of that name  | omitted; `reason` says which                          |
| the lookup fails (HTTP error)                         | omitted; `reason` names the error                     |

The reports list is followed through `meta.has_next_results`, up to 50
pages of 100.

`antimalware_definitions_source` is then `fleet_report:<name>`.
`soc2_cc6_8_malware_protection` fails a host whose date is more than 168
hours before the evaluation instant, and judges a host with no date on
`antimalware_protected` alone.

**The date is only as good as the operator's query.** The connector checks
that the column parses as a date, not that it is a definitions date. No
query for any engine has been verified against a live host, and the Fleet
report endpoints were built from Fleet's REST API docs (read 2026-11-22),
not exercised against a running Fleet. Where an engine keeps its last
update time is the operator's to find; Microsoft documents it for Defender
as `MSFT_MpComputerStatus.AntivirusSignatureLastUpdated` (WMI), which
osquery has no table for.

### Host role from a Fleet label (#2826)

`soc2_a1_1_capacity_monitoring` checks production hosts only. Fleet has no
role field, so the operator creates a Fleet label (manual or dynamic) that
holds the production hosts, and names it with `--production-label`. A
host carrying a label of exactly that name is pushed with
`host_role: production` and `host_role_source: fleet_label:<name>`. Other
hosts carry no `host_role`. With no label named, no host is production and
a1_1 stays inconclusive. The flag applies to `--mode=fleet` only.

### Capacity (#2826)

`disk_used_percent` is `100 - percent_disk_space_available` from Get host.
When `gigs_total_disk_space` is missing or 0 (Fleet has no disk reading
yet), or the percentage is outside 0-100, it is omitted rather than read as
a full disk. Fleet reports no memory or CPU use, so a Fleet-only tenant's
a1_1 fails on disk use or is inconclusive (#2935). In `--mode=local` the
query reads disk use from osquery's `mounts` row for `/`; that mode's
transport is still not wired.

### Scope tagging (AC-4)

Each record carries four scope dimensions:

| Key                   | Value                     | Notes                                                                                                                                           |
| --------------------- | ------------------------- | ----------------------------------------------------------------------------------------------------------------------------------------------- |
| `org`                 | `--org` flag              | Operator-provided organization slug.                                                                                                            |
| `environment`         | `--environment` flag      | `prod`, `staging`, etc.                                                                                                                         |
| `cloud_account`       | `workforce` (constant)    | Distinguishes endpoint posture from server/cloud-account scoping used by AWS/GCP/Azure.                                                         |
| `data_classification` | `restricted` or `unknown` | Inferred per host: MDM-enrolled → `restricted` (managed corporate device). Un-enrolled → `unknown` (BYOD/transient — evaluator surfaces these). |

### Idempotency (AC-5)

```
idempotency_key = sha256("osquery.host_posture|" + host_uuid + "|" + RFC3339(hour-truncated observed_at))
```

A host observed twice in the same UTC hour collapses to the same record.
A host with empty `host_uuid` is skipped (no fabricated keys).

## Rate limiting

Fleet enforces per-IP rate limits. On HTTP 429 the connector surfaces
the upstream `Retry-After` header inside an `APIError` rather than
auto-retrying — the operator's cron schedule owns back-off.

## Anti-criteria (P0)

- Require admin or maintainer Fleet token → REJECTED. Documented roles
  are read-only (`observer` / `observer_plus`). Enforced by
  `TestDocumentedScopes_NoWriteOrDeleteOrAdminOrMaintainer`.
- Log Fleet token or socket auth material → REJECTED.
  `osqueryauth.Credential.String()` redacts; `%s` / `%v` / `%+v`
  formatting paths all covered by `TestCredential_StringRedacts`.
- Push without `idempotency_key` derived from `host_uuid` → REJECTED.
  `buildHostPostureRecord` returns an error on empty `host_uuid` and
  `idem.HostPostureKey("", …)` returns the empty string for an
  unambiguous reject signal.
- Mutate Fleet state → REJECTED. The connector has zero `POST` / `PATCH`
  / `DELETE` HTTP code paths against the Fleet API. Read-only by
  construction.
- Expose the local osqueryd socket beyond the process boundary →
  REJECTED. Local mode reads only; it never binds, listens, or proxies
  the socket. The root-owned socket's filesystem permission remains the
  sole security boundary.

## Tests

```sh
go test ./connectors/osquery/...
```

Unit tests use `httptest.NewServer` to replay realistic Fleet REST
payloads. Platform-side integration tests use `bufconn` against
`internal/api` exactly like slices 044 / 045 / 046.

## References

- canvas §4.2 (osquery / Fleet in v1 roster)
- canvas §1.6 (anti-pattern: "collector agent on every laptop")
- canvas §4.1 (Evidence SDK — pull profile)
- Fleet REST API: https://fleetdm.com/docs/rest-api/rest-api
- osquery tables: https://osquery.io/schema
