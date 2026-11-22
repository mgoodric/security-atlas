# #2825 — cc6_7: default S3 encryption with an allowed algorithm (decisions log)

Parent: #1860 (ADR 0028, tier 1 row 10). Was blocked by #2815 (PR #2858),
which made the AWS connector emit the registered
`aws.s3.bucket_encryption_state.v1` keys and added schema 1.1.0. No
migration.

- detection_tier_actual: manual_review
- detection_tier_target: unit

The bug found: `connectors/aws/README.md` still listed `--control-id`'s
default as `scf:CRY-07`. The flag has defaulted to empty since #2924, which
is what routes the records to cc6_7 at all. Fixed in this PR; a test over
the flag table would have caught it.

## D1 — The fields read are #2815's, and `result` is read with them

The query reads `bucket_name`, `encryption_enabled` and `sse_algorithm`
from the payload and the record's `result` column. These are the keys
`buildRecord` (`connectors/aws/cmd/aws-connector/cmd_run.go`) emits at
1.1.0, which #2815 settled.

#2815 D3, and Matt's note on this ticket, say `encryption_enabled` is
`false` on an inconclusive record too, because 1.0.0 requires it. So
`encryption_enabled = false` alone is not a failure. Per bucket:

| Latest record                                                           | Bucket      |
| ----------------------------------------------------------------------- | ----------- |
| `result` pass, `encryption_enabled` true, `sse_algorithm` allowed       | encrypted   |
| `result` fail, `encryption_enabled` false                               | unencrypted |
| `encryption_enabled` true, `sse_algorithm` a non-empty disallowed value | disallowed  |
| anything else (inconclusive, missing algorithm, inconsistent shapes)    | unread      |

Across buckets, any unencrypted or disallowed bucket fails the control,
all encrypted passes it, and otherwise it is inconclusive. No record is
inconclusive. This is three-valued per the dispatcher's brief: absent is
inconclusive.

The connector gives `fail` only for AWS's
`ServerSideEncryptionConfigurationNotFoundError` or a configuration whose
rules carry no default (`awss3.checkEncryption`), so `fail` plus `false`
is the "no default encryption" case the ticket names. A `fail` record that
names an allowed algorithm is not a shape the connector emits; it is
unread, not pass (case `fail-verdict-naming-an-allowed-algorithm-is-inconclusive`).

## D2 — The allowed list is `AES256` and `aws:kms`, exactly as written

AWS's `ServerSideEncryption` enum (aws-sdk-go-v2 `service/s3` v1.114.6,
`types/enums.go`) has five values: `AES256`, `aws:kms`, `aws:kms:dsse`,
`aws:fsx`, `aws:backup`. The ticket and ADR 0028 name two. I kept the list
at those two, so `aws:kms:dsse` (dual-layer SSE-KMS) fails the control.

This is the call Matt may want to revisit. DSSE-KMS is KMS-based and adds a
second layer, so a bucket using it is not less encrypted than one on
`aws:kms`. I did not widen the list because the list was settled in the
grill and widening it changes what the check claims. The cost is a
visible false finding on a DSSE bucket, which an operator would see and
raise, rather than a silent pass. Adding `'aws:kms:dsse'` to both `IN`
lists and flipping case `dsse-kms-is-not-in-the-allowed-list` is the whole
change if Matt decides otherwise. That change would supersede the bundle
again.

The comparison is exact and case-sensitive. AWS returns the enum
verbatim, so `aes256` is not a value it emits; it fails as disallowed
(case `algorithm-is-case-sensitive`). An empty-string algorithm counts as
missing, so it is unread rather than disallowed. The connector omits empty
strings anyway.

`kms_key_id`, `kms_key_arn` and `bucket_key_enabled` are not read. Whether
the key is customer-managed is a separate check, and the description says
it is not made.

## D3 — Shape follows cc6_6 / cc4_1

- Records with no or empty `bucket_name` are left out. S3 bucket names are
  unique within a partition, so `bucket_name` is the key without
  `account_id`. `account_id` is optional in the schema, so keying on it
  would split a bucket's records.
- Latest per bucket is `rank()` over `observed_at DESC`. No payload
  timestamp exists, so no RFC 3339 guard is needed. The connector
  truncates `observed_at` to the hour, so two runs in one hour tie. A tied
  failing record fails the bucket; it passes only if every tied record
  passes (`tied-latest-with-one-failing-record-fails`).
- Time: only `evaluated_at`, never `now()`. A latest record whose
  `observed_at` is more than an hour after `evaluated_at` is unread, the
  same future cap as cc6_5 / cc9_1 (`observed-at-over-1h-after-instant-is-inconclusive`).
  There is no window of the check's own; the daily freshness window
  bounds the records.

## D4 — Routing: the kind is routed by type

`aws.s3.bucket_encryption_state.v1` is in `internal/evidence/routed`
(#2924), and the connector's `--control-id` defaults to empty. The new
`internal/evidence/ingest/s3_cc6_7_integration_test.go` pushes
connector-shaped 1.1.0 payloads with no `control_id` through
`ingest.Service.Process`, which validates them against the schema, and
evaluates the stock bundle with the live engine. Its disallowed-algorithm
and unread-bucket cases fail against the previous Rego roll-up
(`(pass, 2), want (fail, 2)` and `(pass, 2), want (inconclusive, 2)`)
and pass against this query.

## D5 — Supersedes `soc2_cc6_7_encryption_in_transit`

The query's language and expression change, so the bundle supersedes on
the next boot (#1908). The query id stays
`all_s3_buckets_encrypted_at_rest`. Owners, evidence, evaluations,
exceptions and risk links stay on the old row and have to be moved by hand
(#2820 D1). The PR carries the `supersedes-stock` label (#2891); the
changelog fragment carries the upgrade note.

**Upgrade note:** on the first start after upgrading,
`soc2_cc6_7_encryption_in_transit` is superseded by a new version.
Re-assign its owner and move exceptions and risk links to the new
version; the old version keeps its history. A tenant whose cc6_7 passed
can now fail (a bucket whose default rule is `aws:kms:dsse`, `aws:fsx` or
`aws:backup`) or go inconclusive (a bucket the connector could not read,
which the roll-up let a passing bucket outvote).

## Not changed, noted

The title still says "Data is encrypted in transit", and the bundle id
says `encryption_in_transit`, though the bundle checks only S3 encryption
at rest. The description states the scoping. Renaming the bundle id is
its own supersede, so it is left for a separate decision.
