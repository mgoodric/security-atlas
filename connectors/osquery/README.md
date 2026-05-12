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
  --environment prod

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

One `osquery.host_posture.v1` record per host. Schema 1.0.0 declares:

- `host_uuid` (required)
- `hostname` (required)
- `platform`
- `os_version`
- `disk_encryption_enabled`
- `screen_lock_enabled`
- `firewall_enabled`
- `mdm_enrolled`

Schema is `additionalProperties: false` — the connector emits only the
declared fields. The slice's issue spec mentions `edr_running` and
`os_patch_level`; these are not in the frozen 1.0.0 schema and are not
emitted here. An additive minor bump (1.1.0) is a future slice's call.

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
