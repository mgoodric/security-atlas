# Manual upload / CSV / S3 / SFTP escape-hatch connector

The universal escape-hatch ingestion connector for `security-atlas`. For
evidence that doesn't fit a structured connector — audit artifacts,
screenshots, third-party reports, certs, CSV exports — three transports
hand the bytes off to the same `manual.upload.v1` evidence kind:

| Mode    | Granularity                 | Auth posture                                                                                                                            |
| ------- | --------------------------- | --------------------------------------------------------------------------------------------------------------------------------------- |
| `local` | one record per CSV **row**  | none — operator owns the file system                                                                                                    |
| `s3`    | one record per object       | standard AWS credential chain (env / profile / IRSA / IMDS); flag-passed access keys are never accepted                                 |
| `sftp`  | one record per file matched | SSH key loaded from `--key-file` (never a flag value); `--known-hosts` mandatory; `InsecureIgnoreHostKey` rejected at config-build time |

Reuses the bundled `manual.upload.v1` schema from slice 014 unchanged.

## Subcommands

```sh
# Announce this connector instance to the platform.
atlas-manual register \
  --endpoint platform.example.com:443 \
  --token "$SECURITY_ATLAS_TOKEN"

# Print the per-mode auth posture.
atlas-manual scopes

# Parse a local CSV; emit one record per row.
atlas-manual local \
  --endpoint platform.example.com:443 \
  --token "$SECURITY_ATLAS_TOKEN" \
  --file ./audit-findings.csv \
  --control-id scf:GOV-04 \
  --scope environment=prod \
  --scope business_unit=corp

# List an S3 prefix; emit one record per object.
atlas-manual s3 \
  --endpoint platform.example.com:443 \
  --token "$SECURITY_ATLAS_TOKEN" \
  --bucket evidence-bucket \
  --prefix audits/2026/ \
  --region us-east-1 \
  --control-id scf:GOV-04 \
  --scope environment=prod

# Pull files matching a glob from SFTP; emit one record per file.
atlas-manual sftp \
  --endpoint platform.example.com:443 \
  --token "$SECURITY_ATLAS_TOKEN" \
  --host sftp.example.com \
  --user atlas \
  --path '/inbox/*.pdf' \
  --key-file ~/.ssh/atlas_ed25519 \
  --known-hosts ~/.ssh/known_hosts \
  --control-id scf:GOV-04 \
  --scope environment=prod
```

## Auth posture per mode

### `local`

No platform-side credentials. The connector reads the CSV from the local
filesystem with the process's current uid. The operator decides who can
exec the binary against which files.

### `s3`

Credentials come from the AWS Go SDK v2 default credential chain — in
preference order: environment variables, shared config / credentials
profile, IRSA (workload-identity in EKS), IMDS (EC2 instance profile).
**The connector never accepts an access key or secret access key via
flag.** This avoids the canonical shell-history / process-listing leak.

The `--prefix` flag is mandatory. The connector refuses to scan an entire
bucket — operators must opt into the prefix scope explicitly.

### `sftp`

Two-part contract: an SSH key loaded from `--key-file`, and a
`--known-hosts` path used to verify the remote host key.

- `--key-file` reads from disk and is never echoed back. The binary will
  not accept the key bytes via a flag value, environment variable, or
  stdin — the explicit on-disk file is the only path. If the key file
  cannot be parsed, the error message names the file path but never
  includes the file contents.
- `--known-hosts` is mandatory. Loading the file produces an
  `ssh.HostKeyCallback` backed by `golang.org/x/crypto/ssh/knownhosts`.
  A construction-time guard rejects `ssh.InsecureIgnoreHostKey` (detected
  by inspecting the closure's runtime function name).

## Idempotency keys

All three modes derive a `sha256(...)` hex key prefixed with `manual.upload`
so keys never collide across transports:

| Mode    | Key inputs                                                        |
| ------- | ----------------------------------------------------------------- |
| `local` | `"manual.upload\|" + file_path + "\|" + row_index + "\|" + hour`  |
| `s3`    | `"manual.upload\|" + bucket    + "\|" + key       + "\|" + etag`  |
| `sftp`  | `"manual.upload\|" + host      + "\|" + path      + "\|" + mtime` |

The S3 etag and SFTP mtime are the freshness discriminators: when the
remote artifact changes, a new key is derived and the ledger emits a
fresh record.

## CSV parser caps (DoS guards)

The CSV parser enforces explicit caps so an attacker-supplied file cannot
exhaust memory before the connector emits its first record:

| Flag                | Default         | Behavior on cap-exceeded     |
| ------------------- | --------------- | ---------------------------- |
| `--max-rows`        | 100000          | `manualcsv.ErrTooManyRows`   |
| `--max-field-bytes` | 1048576 (1 MiB) | `manualcsv.ErrFieldTooLarge` |

The parser is `encoding/csv`-backed with `ReuseRecord = false`; each
emitted `Row` carries defensive copies of its `Fields`.

## Schema mapping (manual.upload.v1)

Every emitted record carries the schema's three required fields:

| Schema field   | Local mode                   | S3 mode                          | SFTP mode                   |
| -------------- | ---------------------------- | -------------------------------- | --------------------------- |
| `uploaded_by`  | `connector:manual:local@<v>` | `connector:manual:s3@<v>`        | `connector:manual:sftp@<v>` |
| `filename`     | basename of the CSV file     | full S3 object key               | basename of the SFTP path   |
| `content_type` | `text/csv`                   | `application/octet-stream`       | inferred from extension     |
| `size_bytes`   | JSON-encoded row size        | object size from `ListObjectsV2` | file size from `Stat`       |
| `description`  | `row <i> of <filename>`      | `s3://<bucket>/<key>`            | `sftp://<host><path>`       |

The `actor_type` is always `connector`, matching the convention shared
with `connectors/aws/` and `connectors/github/`.

## Large payload policy (v1)

Inline payload is capped at 1 MiB per row / object / file. Records that
exceed the cap are **skipped with a warning** rather than half-wiring
slice 036's `POST /v1/artifacts:upload` redirect. The schema's
`size_bytes` field still records the true size. A follow-up slice
will wire the artifact-store redirect end-to-end.

## Anti-criteria (P0) — all enforced at code or test level

| Anti-criterion                    | Where enforced                                                                       |
| --------------------------------- | ------------------------------------------------------------------------------------ |
| AWS credentials in logs           | `cmd_s3.go` never echoes the AWS config; integration test scans output               |
| SSH key material in logs          | `manualsftp.LoadPrivateKey` error never contains key bytes; integration test asserts |
| CSV parser without row/field caps | `manualcsv.Limits` zero values rejected; tests pin cap behavior                      |
| SFTP `InsecureIgnoreHostKey`      | `manualsftp.BuildSSHConfig` rejects via closure-name inspection                      |
| Push without idempotency_key      | `idem.{LocalRowKey,S3ObjectKey,SFTPFileKey}` mandatory on every emit                 |

## Tests

```sh
go test ./connectors/manual/...
```

| Package               | What's covered                                                                                              |
| --------------------- | ----------------------------------------------------------------------------------------------------------- |
| `internal/idem`       | Deterministic keys, hour truncation, mode-prefix separation, table tests                                    |
| `internal/manualcsv`  | Cap-rows / cap-field reject paths, happy-path multi-row, header-only edge                                   |
| `internal/manuals3`   | `httptest`-fake LIST round trip, ETag unquoting, error propagation                                          |
| `internal/manualsftp` | Host-key callback loading, `InsecureIgnoreHostKey` rejection, key parse, key-bytes redaction                |
| `cmd/atlas-manual`    | PreRunE flag enforcement for every mode, scopes-subcommand output, actor_id shape, credential-sentinel scan |
