# Observability

This page documents atlas's two observability surfaces — the OpenTelemetry SDK (slice 121) and the external audit-log sink (slice 126) — from the operator's point of view: how to enable, how to verify, how to verify integrity, and where the receive-side dashboards live.

Audience: a self-host operator who has atlas deployed via docker-compose or Helm and needs to wire telemetry and audit retention into their stack.

---

## 1. OpenTelemetry SDK (slice 121)

Atlas emits OTLP traces + metrics + Go runtime telemetry to a maintainer-configured OTel Collector. The SDK is a **no-op when unconfigured** — operators who don't need telemetry pay nothing.

### Enable

Set these env-vars on the atlas process (`deploy/docker/.env` for docker-compose, the Helm chart `values.yaml` for Kubernetes):

| Env var                       | Required | Default       | Purpose                                                                 |
| ----------------------------- | -------- | ------------- | ----------------------------------------------------------------------- |
| `OTEL_EXPORTER_OTLP_ENDPOINT` | Yes      | unset = no-op | OTLP receiver URL (e.g. `http://otel-collector:4317`)                   |
| `OTEL_EXPORTER_OTLP_PROTOCOL` | No       | `grpc`        | `grpc` or `http/protobuf`                                               |
| `OTEL_SERVICE_NAME`           | No       | `security-atlas` | Resource attribute `service.name`                                       |
| `OTEL_RESOURCE_ATTRIBUTES`    | No       | none          | `key=value,key=value` resource attributes                               |
| `OTEL_TRACES_SAMPLER`         | No       | `parentbased_traceidratio` | One of the OTel-standard sampler names                                  |
| `OTEL_TRACES_SAMPLER_ARG`     | No       | `0.1`         | Sampler ratio (10% by default)                                          |
| `ATLAS_METRICS_FALLBACK_ENABLE` | No     | `false`       | Opt-in: serve Prometheus `/metrics` for scrape (otherwise OTLP-only)    |
| `ATLAS_DEPLOYMENT_ENVIRONMENT` | No      | unset         | Sets `deployment.environment.name` resource attribute                   |

### Verify

The atlas process emits one startup log line confirming OTel state:

```
atlas: opentelemetry: enabled endpoint=http://otel-collector:4317 sampler=parentbased_traceidratio sampler_arg=0.1 service_name=security-atlas metrics_fallback=false
```

(or `atlas: opentelemetry: disabled (OTEL_EXPORTER_OTLP_ENDPOINT not set)` in no-op mode).

The docker-compose dev bundle ships an OTel Collector + Tempo + Prometheus + Grafana stack at `deploy/observability/`; bring it up with `just observability-up` and confirm traces appear in Tempo's UI.

See the slice 121 decisions log at `docs/audit-log/121-atlas-otel-sdk-decisions.md` for the full design trail (sampler choice, NATS propagation, security guards, etc.).

---

## 2. External audit-log sink (slice 126)

Atlas's in-app audit-log tables (`decision_audit_log`, `evidence_audit_log`, `exception_audit_log`, `sample_audit_log`, `audit_period_audit_log`, `aggregation_rule_audit_log`, `feature_flag_audit_log`, `me_audit_log`, `walkthrough_audit_log`) are the daily-driver record. But the slice-036 RLS policy allows tenant-write for admins — a malicious in-app admin can read existing rows and craft new rows. The **external audit-log sink** closes that loop by fanning out every in-app audit-log write to a JSONL file outside the app's reach, with a per-record HMAC-SHA256 integrity tag.

### Threat model — what "tamper-evident" means

- **In-app admin tampers with in-app row.** The external JSONL file has a separate copy. The HMAC is bound to a per-deployment secret the admin does not hold; a tampered or forged copy cannot reproduce a valid HMAC.
- **Operator wants to detect tampering.** Replay the JSONL file through the verification procedure below. Any line whose `_hmac` does not match the canonical bytes is flagged.
- **In-app admin tries to read or unlink the JSONL file.** Mount the volume with a different UID owner (the operator-chosen log-shipping user, e.g. `vector`, `fluent`, `syslog`, `1003`) than atlas runs as. atlas writes append-only; another UID owns + reads + ships.

### Enable

| Env var                          | Required when sink active | Default                                      | Purpose                                                                              |
| -------------------------------- | ------------------------- | -------------------------------------------- | ------------------------------------------------------------------------------------ |
| `ATLAS_AUDIT_SINK_PATH`          | Yes (to activate)         | unset = no-op (sink disabled)                | JSONL file path (e.g. `/var/log/security-atlas/audit-log.jsonl`)                     |
| `ATLAS_AUDIT_SINK_HMAC_KEY`      | Yes (when path is set)    | unset = atlas refuses to boot                | HMAC secret (>= 32 bytes; treat as a deployment secret, NOT in the env file plaintext) |
| `ATLAS_AUDIT_SINK_BUFFER_SIZE`   | No                        | `10000`                                      | In-memory channel capacity before overflow lands in `audit_sink_failures` table      |

**Opt-in default**: when `ATLAS_AUDIT_SINK_PATH` is unset, the sink is a no-op. Atlas continues to write the in-app rows; no external fan-out happens.

**Fail-fast on misconfiguration**: if `ATLAS_AUDIT_SINK_PATH` is set but `ATLAS_AUDIT_SINK_HMAC_KEY` is absent or shorter than 32 bytes, atlas exits at boot with an error. Better to refuse to boot than ship a silent integrity gap.

### Container + volume setup (docker-compose example)

```yaml
services:
  atlas:
    environment:
      ATLAS_AUDIT_SINK_PATH: /var/log/security-atlas/audit-log.jsonl
      ATLAS_AUDIT_SINK_HMAC_KEY_FILE: /run/secrets/audit_sink_hmac_key
      # Read the key from a secret file at startup (one-liner shell wrapper around the binary).
      # Direct env-var injection works too if your deployment doesn't carry a secret store.
    volumes:
      - audit-sink:/var/log/security-atlas:rw
    secrets:
      - audit_sink_hmac_key

  vector:
    image: timberio/vector:latest-alpine
    user: "1003:1003"            # OWNED-BY user — atlas writes, vector ships
    volumes:
      - audit-sink:/var/log/security-atlas:ro
    # ... vector config that tails the JSONL + ships to your log backend

volumes:
  audit-sink:

secrets:
  audit_sink_hmac_key:
    file: ./secrets/audit_sink_hmac_key
```

**Why the UID separation matters**: the external sink only achieves the "out-of-app" property if atlas cannot tamper with the file from inside its own process. The volume is owned by UID 1003 (vector / log-shipper); atlas runs as a different UID and can ONLY append (write permission, not delete or read-back-and-modify). The operator's choice of UID is whatever their host's log-shipping pipeline already uses.

### logrotate notes

The sink opens the file with `O_APPEND|O_CREATE|O_WRONLY`. logrotate's default `copy + truncate` mode breaks this — atlas's open file descriptor still references the old inode after truncation, and writes silently land in lost space. Configure logrotate with `create` mode and a SIGHUP-on-rotate hook so atlas reopens cleanly. The simpler alternative: let vector/fluent-bit handle rotation natively (most ship-and-forget log shippers do).

### Configuration verification

The atlas process emits one startup log line confirming sink state:

```
atlas: audit-sink: enabled path=/var/log/security-atlas/audit-log.jsonl buffer_size=10000
```

When disabled (no-op mode), there is no startup line for the sink.

### Receiver-side HMAC verification

Every JSONL line is the canonical `unifiedlog.Entry` shape with one additional field, `_hmac`, that carries the hex-encoded HMAC-SHA256 of the canonical bytes keyed by the deployment secret. To verify a line:

1. Read the line.
2. Parse the line as JSON.
3. Extract the `_hmac` field and capture its value as `received_tag`.
4. Re-serialize the remaining fields back to canonical JSON. Go's `encoding/json` orders struct fields by source order, so the canonical bytes are deterministic across runs (this is the property the verifier depends on).
5. Compute `expected_tag = hmac.HMAC(sha256, key, canonical_bytes).Hex()`.
6. Compare `received_tag` and `expected_tag` with a constant-time comparison (`hmac.Equal` in Go, `hmac.compare_digest` in Python). Mismatch = tamper.

Reference verifier (Python):

```python
import hmac
import hashlib
import json
import sys
from pathlib import Path

KEY = Path("/run/secrets/audit_sink_hmac_key").read_bytes()

def canonical_bytes(entry: dict) -> bytes:
    # encoding/json sorts struct fields by SOURCE ORDER; reproduce the order
    # below by listing the keys explicitly. (The wire form NEVER carries
    # additional fields — see unifiedlog.Entry.)
    order = ["occurred_at", "actor_id", "tenant_id", "kind", "target_type",
             "target_id", "action", "row_id", "payload_json"]
    ordered = {k: entry[k] for k in order if k in entry}
    return json.dumps(ordered, separators=(",", ":")).encode()

ok = bad = 0
for line in sys.stdin:
    raw = json.loads(line)
    received = raw.pop("_hmac", None)
    if received is None:
        bad += 1
        continue
    expected = hmac.new(KEY, canonical_bytes(raw), hashlib.sha256).hexdigest()
    if hmac.compare_digest(received, expected):
        ok += 1
    else:
        bad += 1
        print(f"TAMPER: {line.strip()}")

print(f"verified: {ok} ok / {bad} tampered-or-malformed")
```

**Important**: Go's `encoding/json` produces JSON without insignificant whitespace by default, and orders struct fields by source order. Python's `json.dumps` with `separators=(",", ":")` matches the byte form — but only if you reconstruct the field order exactly. If you change the unifiedlog.Entry struct shape (add a field), the verifier above breaks; update both sides together. (The canonical struct lives at `internal/audit/unifiedlog/unifiedlog.go::Entry`.)

### Backpressure: the audit_sink_failures table

When the in-memory channel fills (10000 records by default) and a producer's `Emit` call cannot enqueue, the sink writes one row to the `audit_sink_failures` table instead — never silently drops. The table has the slice-036 four-policy append-only RLS pattern: any tenant member can `SELECT` + `INSERT` their tenant's rows, but no policy permits `UPDATE` or `DELETE`. Operators investigating a gap query this table:

```sql
SELECT occurred_at, failure_reason, entry_kind, entry_actor, entry_action, error_text
FROM audit_sink_failures
WHERE tenant_id = '<tenant>'
ORDER BY occurred_at DESC
LIMIT 100;
```

`failure_reason` is one of:

- `buffer_overflow` — the channel was full; the entry never reached the file. Cross-reference the entry's `(actor, target_id, action)` tuple against the in-app audit-log table to find the row that exists in-app but not in the sink file. Resync by re-emitting from the in-app row if needed.
- `write_error` — the channel accepted the entry but the file-side write failed (disk full, broken pipe after rotation race, permission denied). `error_text` captures the underlying OS error string.

Per the slice-126 anti-criterion P0-A1, every dropped entry MUST land in this table. If you see Emitted + Dropped not equal to total events, file an issue — that's a bug in the sink itself.

### Counter snapshot for ops dashboards

The sink exposes lifetime counters via the (private; programmatic) `Sink.StatsSnapshot() (emitted, dropped, writeErrors, failureRows int64)` API. Surface them through your OTel metrics pipeline by extending the sink to register OTel meters (out-of-scope for slice 126; a future slice can add this once the operator demand surfaces — the JSONL + table-row signal is sufficient for v1).

### Composing with OTel routing

Operators who want OTel-native audit-log routing instead of (or in addition to) JSONL-to-disk run `vector` or `fluent-bit` as the log shipper, configured with both:

- A `file` source watching the JSONL file
- An `opentelemetry` sink targeting the OTel Collector

The JSONL line shape includes the `tenant_id`, `kind`, `actor_id`, `target_id`, and `action` fields directly — Collector pipelines can attribute on them without parsing payload_json. The maintainer's filed lean (option c) is achievable today via this composition without atlas itself depending on the OTel logs SDK (which is pre-1.0 alpha as of 2026-05-18); the slice 126 decisions log records the rationale for not bundling the OTel logs dependency.

---

## Cross-references

- Slice 121 (OTel SDK) — `docs/issues/121-atlas-otel-sdk.md`, `docs/audit-log/121-atlas-otel-sdk-decisions.md`, `internal/observability/otel/`
- Slice 124 (unified audit-log aggregator) — `docs/issues/124-unified-audit-log-aggregation-api.md`, `internal/audit/unifiedlog/`
- Slice 126 (this slice) — `docs/issues/126-external-audit-log-sink.md`, `docs/audit-log/126-external-audit-log-sink-decisions.md`, `internal/audit/sink/`, `migrations/sql/20260518000000_audit_sink_failures.sql`
