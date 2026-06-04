# AWS connector

The flagship connector (slice 004) and the canonical worked example the
[connector-authoring guide](../../docs-site/docs/connector-authoring.md)
links to. Emits one evidence kind:

| Kind                                | Profile | Source                                                                          |
| ----------------------------------- | ------- | ------------------------------------------------------------------------------- |
| `aws.s3.bucket_encryption_state.v1` | push    | `s3:ListBuckets` + `s3:GetBucketLocation` + `s3:GetBucketEncryption` per bucket |

The connector lists every visible bucket, resolves its region, and reports
default server-side-encryption posture (`pass` when SSE is configured,
`fail` when absent, `inconclusive` when a per-bucket lookup errors). Each
bucket becomes one `aws.s3.bucket_encryption_state.v1` record. Additional
kinds (EC2 security-group state, etc.) arrive with later AWS slices; slice
004 ships exactly this one.

## Auth — least-privilege read-only IAM via STS AssumeRole

The connector holds **no static access keys**. `run --role-arn` is required
and the connector refuses to start without it; the role is assumed via STS
and the resulting credentials auto-rotate ahead of expiry
(`aws.NewCredentialsCache`). The connector's baseline identity (the principal
allowed to call `sts:AssumeRole`) is supplied by the vendor-native AWS
credential chain — workload identity / IRSA / OIDC where the connector runs —
never a key baked into source or a flag.

Create a dedicated **read-only** IAM role and grant it exactly the actions
below. Every action the connector calls is a read; the role needs nothing
more.

| AWS API call                          | IAM action                          | Access    | Gates                                                                            |
| ------------------------------------- | ----------------------------------- | --------- | -------------------------------------------------------------------------------- |
| `sts:GetCallerIdentity`               | `sts:GetCallerIdentity`             | Read      | Resolving the assumed account id (the `cloud_account` scope dimension)           |
| `s3:ListBuckets`                      | `s3:ListAllMyBuckets`               | Read/List | Enumerating buckets to inspect                                                   |
| `s3:GetBucketLocation`                | `s3:GetBucketLocation`              | Read      | Region resolution per bucket (re-targets the regional S3 client)                 |
| `s3:GetBucketEncryption`              | `s3:GetEncryptionConfiguration`     | Read      | `aws.s3.bucket_encryption_state.v1` (the SSE posture itself)                     |
| `organizations:ListTagsForResource`\* | `organizations:ListTagsForResource` | Read      | Optional — infers the `environment` scope tag from the account `Environment` tag |

\* The Organizations call is **optional**. If the role cannot reach
Organizations (the common case when the connector runs outside the payer
account), the connector falls back to the `--environment` flag. Grant it
only if you want environment inferred from the account tag; omit it
otherwise — least privilege prefers omission.

Example least-privilege policy document (placeholders only — substitute your
own account id and bucket constraints):

```json
{
  "Version": "2012-10-17",
  "Statement": [
    {
      "Sid": "AtlasStsIdentity",
      "Effect": "Allow",
      "Action": "sts:GetCallerIdentity",
      "Resource": "*"
    },
    {
      "Sid": "AtlasS3ReadOnlyEncryptionPosture",
      "Effect": "Allow",
      "Action": [
        "s3:ListAllMyBuckets",
        "s3:GetBucketLocation",
        "s3:GetEncryptionConfiguration"
      ],
      "Resource": "*"
    }
  ]
}
```

The role's trust policy allows the connector's baseline principal to
`sts:AssumeRole`. Use a placeholder ARN such as
`arn:aws:iam::<ACCOUNT_ID>:role/security-atlas-aws-readonly` — never paste a
real account id, role ARN, or access key into config that lands in version
control.

**Banned IAM:** no write, delete, or admin actions of any kind. Do **not**
attach `AdministratorAccess`, `AmazonS3FullAccess`, or any policy that
grants `s3:Put*` / `s3:Delete*` / `iam:*` / `*:*`. Do **not** use the
account root user. The connector invokes only the read actions in the table
above; anything broader is an unnecessary elevation of privilege and is
rejected as a misconfiguration. The connector has no write code path — the
only S3 operations it issues are `List*`/`Get*`.

## Subcommands

```sh
# Build the binary (justfile target).
just connector-build   # produces ./bin/aws-connector

# Announce this connector instance to the platform.
aws-connector register \
  --endpoint platform.example.com:443 \
  --token "$SECURITY_ATLAS_TOKEN"

# Assume the read-only role, inspect bucket encryption, push evidence.
aws-connector run \
  --endpoint platform.example.com:443 \
  --token "$SECURITY_ATLAS_TOKEN" \
  --role-arn arn:aws:iam::<ACCOUNT_ID>:role/security-atlas-aws-readonly \
  --region us-east-1 \
  --environment prod
```

| Flag            | Subcommand | Required | Default                             | Notes                                                                                   |
| --------------- | ---------- | -------- | ----------------------------------- | --------------------------------------------------------------------------------------- |
| `--endpoint`    | both       | yes      | env `SECURITY_ATLAS_ENDPOINT`       | platform gRPC endpoint                                                                  |
| `--token`       | both       | yes      | env `SECURITY_ATLAS_TOKEN`          | security-atlas bearer token                                                             |
| `--insecure`    | both       | no       | `false`                             | disables TLS; loopback endpoints only                                                   |
| `--role-arn`    | `run`      | yes      | —                                   | IAM role ARN to assume (no static keys supported)                                       |
| `--region`      | `run`      | yes      | —                                   | primary AWS region; S3 calls re-target per bucket via `GetBucketLocation`               |
| `--environment` | `run`      | no       | —                                   | environment scope tag; fallback when `Organizations:ListTagsForResource` is unavailable |
| `--kind`        | `run`      | no       | `aws.s3.bucket_encryption_state.v1` | slice 004 supports exactly this kind                                                    |
| `--control-id`  | `run`      | no       | `scf:CRY-04`                        | control id attached to each emitted record                                              |

`register` announces `name=aws-connector`, `supported_kinds=[aws.s3.bucket_encryption_state.v1]`,
and `profiles_supported=[push]` to `ConnectorRegistryService.Register`.

## Scope minimums

Every emitted record sets the minimum scope dimensions slice 004's
connector-pattern convention requires:

| Scope key       | Value shape        | Source                                                                     |
| --------------- | ------------------ | -------------------------------------------------------------------------- |
| `cloud_account` | `aws:<ACCOUNT_ID>` | `sts:GetCallerIdentity` (the assumed account)                              |
| `environment`   | e.g. `prod`        | account `Environment` tag via Organizations, else the `--environment` flag |

`source_attribution.actor_id` follows the cross-connector convention
`connector:<vendor>:<service>@<version>` — here `connector:aws:s3@<version>`,
where `<version>` is the build's module version (or `dev` under `go run`).

## Idempotency

Each record's `idempotency_key = sha256(bucket_arn | hour_truncated_observed_at)`.
`observed_at` is truncated to the UTC hour, so two runs within the same hour
for the same bucket collapse to one ledger row; a run that crosses an hour
boundary writes a fresh record. `source_attribution.session_id` is left
empty on purpose: a per-call UUID would change the record's canonical hash
between dedup retries.

## Anti-criteria (P0)

- Requires a write/admin IAM policy → REJECTED. The documented policy is
  read-only (`sts:GetCallerIdentity`, `s3:ListAllMyBuckets`,
  `s3:GetBucketLocation`, `s3:GetEncryptionConfiguration`); no `Put`,
  `Delete`, `iam:*`, `AdministratorAccess`, or `*:*`.
- Static access keys → REJECTED. `--role-arn` is required; `Assume` refuses
  to fall back to env-var or profile-baked static credentials
  (`aws.AnonymousCredentials` is wired as the base provider).
- Mutates AWS state → REJECTED. The connector has no write code path; the
  only S3 methods invoked are `ListBuckets` / `GetBucketEncryption`
  (+ `GetBucketLocation` for region resolution).
- Emits un-scoped records → REJECTED. `run` fails loudly when it can resolve
  neither the account `Environment` tag nor a `--environment` flag, rather
  than pushing a record with an empty `environment` dimension.

## Tests

```sh
go test ./connectors/aws/...
```

Unit tests fake the STS / Organizations / S3 surfaces (no real AWS) and pin
the encryption verdicts, region resolution, identity/environment fallback,
and the `idem.Key` hour-window behavior. The integration test exercises the
full path — `awsauth.Assume` / `ResolveIdentity` → `awss3.Inspect` → record
builder → SDK `Push` → in-process bufconn platform → push receipt — and
confirms two same-hour pushes for one bucket collapse to the same
`record_id`.
