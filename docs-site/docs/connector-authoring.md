# Connector authoring guide

<!-- prettier-ignore-start -->
!!! info "What you'll learn"

    - The two connector profiles (pull and push) and when to pick which
    - The minimum shape of a connector — register, run, push
    - How to register an `evidence_kind`, sign records, and handle
      idempotency
    - Conventions from the four reference connectors (AWS, GitHub, Okta,
      1Password) that any new connector should follow
<!-- prettier-ignore-end -->

The Evidence SDK is the contract surface for getting evidence into the
platform. The full SDK reference lives at
[`Plans/EVIDENCE_SDK.md`](https://github.com/mgoodric/security-atlas/blob/main/Plans/EVIDENCE_SDK.md) —
this page is the operator-facing how-to for building a new connector.

## The two profiles

| Profile                          | Direction         | Who initiates                                       | Use when                                                                                       |
| -------------------------------- | ----------------- | --------------------------------------------------- | ---------------------------------------------------------------------------------------------- |
| **Connector** (pull / subscribe) | Platform → Source | security-atlas reaches out and queries / subscribes | Source has a stable API and you can give the platform credentials to reach it.                 |
| **Pusher** (push)                | Source → Platform | Source initiates and pushes to security-atlas       | Source is behind a firewall, ephemeral (CI), event-emitting (webhook), or owns its scheduling. |

Many real connectors implement **both**. The GitHub connector pulls
org/repo state on a schedule **and** receives push events from
GitHub's webhook subscription. Both flow into the same ledger via the
same `IngestEvidence` call. Pick **at least** the profile that fits
your source's reachability today; add the other when you need it.

## Reference connectors

The platform ships four v1 reference connectors. Pattern your new
connector after the closest match:

| Source    | Connector path                                                                                       | Auth                          | Profile     |
| --------- | ---------------------------------------------------------------------------------------------------- | ----------------------------- | ----------- |
| AWS       | [`connectors/aws/`](https://github.com/mgoodric/security-atlas/tree/main/connectors/aws)             | IAM role (STS AssumeRole)     | Pull (push) |
| GitHub    | [`connectors/github/`](https://github.com/mgoodric/security-atlas/tree/main/connectors/github)       | GitHub App installation token | Pull        |
| Okta      | [`connectors/okta/`](https://github.com/mgoodric/security-atlas/tree/main/connectors/okta)           | API token (Okta admin-scoped) | Pull        |
| 1Password | [`connectors/1password/`](https://github.com/mgoodric/security-atlas/tree/main/connectors/1password) | Service-account token         | Pull        |

The simplest by far is `connectors/aws/cmd/aws-connector/` — read it
first. Two subcommands (`register`, `run`), one evidence kind
(`aws.s3.bucket_encryption_state.v1`), a clean separation between the
auth concern (`internal/awsauth/`), the source-system concern
(`internal/awss3/`), and the idempotency concern (`internal/idem/`).

## Step 1 — define your evidence kind

Every record you push declares an `evidence_kind`. The kind is a
schema identifier, semver-pinned. Register it in
`schemas/<vendor>.<service>.<state>.v<n>.json`:

```json
{
  "$schema": "https://json-schema.org/draft/2020-12/schema",
  "$id": "https://schemas.security-atlas.dev/aws.s3.bucket_encryption_state.v1.json",
  "type": "object",
  "required": ["bucket_arn", "bucket_region", "result", "algorithm"],
  "properties": {
    "bucket_arn": { "type": "string", "pattern": "^arn:aws:s3:::.+$" },
    "bucket_region": { "type": "string" },
    "result": { "enum": ["pass", "fail", "inconclusive"] },
    "algorithm": { "type": "string", "enum": ["AES256", "aws:kms", ""] },
    "kms_key_id": { "type": "string" },
    "bucket_key_enabled": { "type": "boolean" },
    "reason": { "type": "string" }
  }
}
```

Naming convention: `<vendor>.<service>.<state>.v<n>` —
`aws.s3.bucket_encryption_state.v1`, `github.repo.branch_protection_state.v1`,
`okta.org.mfa_enforcement_state.v1`. The verb-noun pattern signals
that a record describes **state**, not an event.

Submit the schema via PR to the canonical schema registry (or, for
private connectors, register it via the schema-registry API as a
tenant-private kind — see slice 014).

## Step 2 — connector binary skeleton

The reference connector has the minimal shape:

```
connectors/<vendor>/
├── cmd/<vendor>-connector/
│   ├── main.go            # entrypoint
│   ├── root.go            # common flags (--endpoint, --token, --insecure)
│   ├── cmd_register.go    # "register" subcommand
│   └── cmd_run.go         # "run" subcommand
├── internal/
│   ├── <vendor>auth/      # vendor-native auth (IAM, OAuth, API key)
│   ├── <service>/         # source-system queries
│   └── idem/              # per-run idempotency-key generation
└── README.md
```

The `register` subcommand announces the connector instance to the
platform — name, version, supported kinds, profiles supported. The
`run` subcommand assumes credentials, queries the source, and emits
records. Separating them lets `register` be a one-shot setup; `run` is
the periodic / on-demand worker.

## Step 3 — register

The connector identifies itself once via the
`ConnectorRegistryService.Register` gRPC call. From the AWS connector:

```go
resp, err := client.Register(ctx, &connectorsv1.RegisterRequest{
    Name:              ConnectorName,          // "aws-connector"
    Version:           connectorVersion(),     // from runtime/debug.ReadBuildInfo
    InstanceId:        uuid.NewString(),       // per-process; new each invocation
    SupportedKinds:    []string{SupportedKind}, // ["aws.s3.bucket_encryption_state.v1"]
    ProfilesSupported: []string{"push"},        // or "pull" / both
})
```

The platform records the registration in the connector inventory; the
returned `Handle.Id` is durable, the `InstanceId` is per-process and
typically re-rolled on each run.

## Step 4 — query the source

Each connector subclasses this layout:

```
[ auth: produce a vendor-native session ]
        │
        ▼
[ source query: enumerate the relevant objects ]
        │
        ▼
[ map: source-record → EvidenceRecord ]
        │
        ▼
[ push: PushEvidenceRecord or batch via gRPC stream ]
```

Conventions from slice 004 (the AWS connector — the first):

- **`actor_id` format**: `connector:<vendor>:<service>@<version>`
  (e.g. `connector:aws:s3@v0.1.4`). This is what shows up in
  `provenance.actor` and is the string the audit log surfaces. The
  vendor and the service are required; the version tag answers "what
  version of this connector emitted this row?"
- **Stable optional fields**: when the source can't provide a value,
  send the empty string or `null`, **not** a placeholder like
  `"unknown"`. Schema validation will accept; downstream evaluation
  treats `""` and `null` consistently.
- **`observed_at` granularity**: seconds, in UTC. Most source APIs
  return millisecond or microsecond timestamps; truncate to second
  granularity at the connector edge — finer granularity is noise.
- **Register-per-run**: it's cheap. Don't try to cache the handle ID
  across runs; just `Register` at the top of every `run` invocation.
- **Scope minimums**: every record carries `scope_id` (or a scope
  predicate the platform can resolve). The connector infers the
  cell from source attributes (account ID + region for AWS,
  org + repo for GitHub). If the connector cannot resolve a cell,
  the record is **rejected** at ingest (`rejected_scope_violation`).
- **Vendor-native auth**: use STS / IRSA / OIDC federation /
  short-lived tokens. **Do not** ship long-lived static credentials
  in the connector config; the platform rejects them at the
  registration validation step.

## Step 5 — push records

Single record:

```go
import sdk "github.com/mgoodric/security-atlas/pkg/sdk-go"

receipt, err := sdk.Push(ctx, sdk.Record{
    Kind:        "aws.s3.bucket_encryption_state.v1",
    ControlID:   controlID,
    ScopeID:     scopeID,
    ObservedAt:  observedAt.UTC().Truncate(time.Second),
    Result:      sdk.ResultPass,  // or ResultFail / ResultNA / ResultInconclusive
    Payload:     payloadJSON,
    Provenance: sdk.Provenance{
        Actor:           actorID(),                 // "connector:aws:s3@v0.1.4"
        SourceSystem:    "aws",
        SourceRecordKey: bucketARN,
        QueryHash:       queryHash,
        RunnerID:        runnerID,
    },
    IdempotencyKey: idem.For(bucketARN, observedAt),
})
```

Batch (≤100 records per call):

```go
results, err := sdk.PushBatch(ctx, []sdk.Record{...})
```

High-throughput (streaming gRPC):

```go
stream, err := sdk.PushStream(ctx)
for _, rec := range records {
    if err := stream.Send(rec); err != nil { return err }
}
receipts, err := stream.CloseAndRecv()
```

## Step 6 — idempotency

Every push carries an `idempotency_key`. The platform uses it for
dedup: re-sending the same `(idempotency_key, payload_hash)` returns
the same receipt without writing again. The convention from slice 004:

```go
// idem.For derives a stable key from the source-record's natural
// identity + the observation timestamp truncated to the connector's
// re-run cadence. For S3 buckets running on a 1h schedule:
//
//   key = sha256(bucket_arn || ":" || observed_at.truncate(1h))[:16]
```

Choose the truncation granularity to match the connector's re-run
cadence — too fine and you re-write the same evidence every minute;
too coarse and a genuine state change inside the window is lost. A
common choice is the connector's run interval rounded up.

## Step 7 — handle rejections

The platform's `IngestEvidence` returns a receipt with a `decision`
field that maps 1:1 to `evidence_audit_log.decision` (see [Audit
logs](audit-logs.md)). A connector should:

- Log the rejection with the `reason_code` at WARN.
- Continue processing — one rejection should not abort the batch.
- Emit a connector-side metric for rejection rate by `decision`.

Rejections are not failures of the connector — they are signal. A
`rejected_validation` says your schema mapping is wrong; a
`rejected_scope_violation` says your scope inference is wrong; a
`rejected_idempotency_mismatch` says your idempotency key collides
across genuinely-distinct content.

## Step 8 — testing

Each reference connector ships:

- **Unit tests** per `internal/<package>` (e.g. `awss3_test.go`)
  exercising the source-record → EvidenceRecord mapping against a
  fake source API.
- **Integration tests** (`integration_test.go`) running against a
  local stub-platform — the `web/scripts/stub-platform-server.ts`
  pattern is the reference stub.
- **Build-tag isolation**: `//go:build integration` keeps the
  integration suite from running in `go test ./...` unless explicitly
  requested.

```sh
just test-go                       # unit
just test-integration              # integration (Postgres + NATS)
go test ./connectors/<vendor>/...  # connector-only subset
```

## Step 9 — publish

Connectors live in the monorepo under `connectors/`. To publish a new
one:

1. Open a PR adding the connector subtree + schemas + tests.
2. The CI pipeline (`just connector-build`) builds the connector
   binary as part of the normal test suite.
3. Squash-merge follows the same Conventional-Commits convention as
   the rest of the repo (`feat(connector): add <vendor>:<service>`).

Out-of-tree connectors (operator-private) follow the same SDK contract
but ship as the operator's own binary. The platform's
`ConnectorRegistry` accepts any registered connector that authenticates
against a valid platform credential.

## A complete worked example

The end-to-end is the AWS S3 connector. Read in this order:

1. [`connectors/aws/README.md`](https://github.com/mgoodric/security-atlas/blob/main/connectors/aws/) — orientation
2. [`connectors/aws/cmd/aws-connector/main.go`](https://github.com/mgoodric/security-atlas/blob/main/connectors/aws/cmd/aws-connector/main.go) — entrypoint
3. [`connectors/aws/cmd/aws-connector/cmd_register.go`](https://github.com/mgoodric/security-atlas/blob/main/connectors/aws/cmd/aws-connector/cmd_register.go) — register flow
4. [`connectors/aws/cmd/aws-connector/cmd_run.go`](https://github.com/mgoodric/security-atlas/blob/main/connectors/aws/cmd/aws-connector/cmd_run.go) — query + push flow
5. [`connectors/aws/internal/awss3/awss3.go`](https://github.com/mgoodric/security-atlas/blob/main/connectors/aws/internal/awss3/awss3.go) — source query
6. [`connectors/aws/internal/idem/idem.go`](https://github.com/mgoodric/security-atlas/blob/main/connectors/aws/internal/idem/idem.go) — idempotency key derivation

Total: ~600 LOC in Go. A new connector following this shape is
typically a 1–2 day slice.

## What this guide deliberately is NOT

- **A protocol reference** — the full proto contract is at
  [`proto/connectors/v1/`](https://github.com/mgoodric/security-atlas/tree/main/proto/connectors/v1)
  and the SDK reference at
  [`Plans/EVIDENCE_SDK.md`](https://github.com/mgoodric/security-atlas/blob/main/Plans/EVIDENCE_SDK.md).
- **An exhaustive list of evidence kinds** — those live in the schema
  registry; the canonical roster is the schema files in
  `schemas/`.
- **A guide to in-app webhook receivers** — push-from-source is the
  pusher profile of the SDK; the operator's webhook receiver is a
  push **client**, not a separate API.

## Next steps

- [Evidence →](primitives/evidence.md) — what your connector produces
- [Audit logs →](audit-logs.md) — where your connector's decisions are
  recorded
- [Evidence SDK reference →](https://github.com/mgoodric/security-atlas/blob/main/Plans/EVIDENCE_SDK.md) — the full SDK contract

---

## Was this helpful?

Tell us in [GitHub
Discussions](https://github.com/mgoodric/security-atlas/discussions).
