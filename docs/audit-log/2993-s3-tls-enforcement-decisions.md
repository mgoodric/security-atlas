# #2993 — CC6.7 in transit: S3 bucket policy denies aws:SecureTransport false (decisions log)

Split out of [#2950](https://github.com/mgoodric/security-atlas/issues/2950),
which retitled `soc2_cc6_7_encryption_in_transit` to what it checks, S3
encryption at rest. Before this, nothing collected in-transit evidence.

- detection_tier_actual: unit
- detection_tier_target: unit

One defect was caught before it shipped: the first draft reused
`idem.Key(bucket_arn, hour)` for the new kind. The ledger's unique index is
`(tenant_id, idempotency_key)` across kinds
(`migrations/sql/20260511000004_evidence_ledger.sql`), so a TLS record and
the same hour's encryption record for one bucket would have deduplicated
into one row. It was found by reading the index before writing the record
builder. `TestBuildTLSRecord_IdempotencyKeyDiffersFromEncryption` now pins
it.

## Not verified live

No AWS account was used. The AWS facts come from the documentation, read
2026-11-22, and the SDK source (`service/s3` v1.114.6):

| Fact                                                                                                               | Source                                                                          |
| ------------------------------------------------------------------------------------------------------------------ | ------------------------------------------------------------------------------- |
| `GetBucketPolicyOutput.Policy` is `*string`, "the bucket policy as a JSON document"                                | SDK `api_op_GetBucketPolicy.go`                                                 |
| No policy returns error code `NoSuchBucketPolicy` ("The specified bucket does not have a bucket policy")           | SDK `types/types.go` error-code list (the API reference page does not list it)  |
| Missing permission is 403; a caller outside the bucket owner's account is 405                                      | S3 API reference, GetBucketPolicy                                               |
| The TLS statement: Deny, Principal `*`, Action `s3:*`, bucket and `/*` resources, `Bool aws:SecureTransport false` | S3 user guide, bucket policy examples, "Restrict access to only HTTPS requests" |
| `"Principal": "*"` and `{"AWS": "*"}` are equivalent                                                               | IAM user guide, Principal element                                               |
| Bool with the key absent does not match                                                                            | IAM user guide, condition operators                                             |

Not answered by any page read: whether `Bool` values match case-insensitively
or accept a JSON `false`. D3 is written so that neither question can turn a
fail into a pass.

## D1. A new stock bundle, not a second query in cc6_7

Options: (a) a second query in `soc2_cc6_7_encryption_in_transit`, or (b) a
new bundle `soc2_cc6_7_s3_tls_enforced`.

**Chosen: (b).** #2950 retitled cc6_7 to "S3 buckets enforce default
encryption at rest". An in-transit query would contradict that title, and
it would supersede the bundle: every adopter re-assigns its owner and moves
exceptions and risk links for a check that did not change. A separate
bundle gives the in-transit check its own owner, exception and evidence
history. Its SCF anchor is `CRY-05`, the anchor the bundled SOC 2 crosswalk
maps to CC6.7 with the rationale "Encryption in transit is the canonical
control for protected transmission" (`data/crosswalks/soc2-tsc-2017.yaml`),
and the one the HIPAA crosswalk names "Encrypting Data In Transit". cc6_7
(at rest) also sits on CRY-05; re-anchoring it is out of scope here.

cc6_7's description still says "no platform check collects in-transit
evidence yet (#2993)". Correcting that line would change its manifest hash
and supersede it, so it is left for the next PR that edits cc6_7 for another
reason.

## D2. The connector parses the policy; the SQL reads two booleans

ADR 0028 puts payload checks in SQL and rejects per-control verdicts in a
connector. Parsing IAM JSON in SQL (a Statement that may be an object or a
list, elements that may be a string or a list) would put a policy parser in
a sandboxed query. The connector reports observed facts instead:
`policy_present` and `denies_insecure_transport`. These are facts about the
bucket, not a control verdict, and another control could read them. The SQL
then applies the same tri-state as cc6_7 (#2815): a bucket fails only when
the connector's verdict is fail and `denies_insecure_transport` is false,
passes only on pass and true, and anything else is inconclusive.

Both booleans are omitted, not written false, when the policy could not be
read, the convention #2912 set for osquery. A missing value therefore means
"not observed".

## D3. What counts as a TLS-enforcing statement

Strict, and a statement it does not recognise counts as not denying. A
misread therefore fails a compliant bucket, and never passes a
non-compliant one. A statement counts when it:

- has Effect `Deny`, and none of `NotPrincipal`, `NotAction` or `NotResource`;
- has Principal `"*"`, `{"AWS": "*"}` or `{"AWS": ["*"]}`;
- lists `s3:*` or `*` among its actions;
- has exactly one condition operator, `Bool`, with exactly one key,
  `aws:SecureTransport`, whose values include the string `"false"`.

The counting statements' resources together must name both
`arn:aws:s3:::<bucket>` and `arn:aws:s3:::<bucket>/*`, each exactly or as
`*`. Two statements, one per resource, therefore pass.

Rejected on purpose:

- **A second condition** (for example a VPC endpoint exception) narrows the
  deny, so plain HTTP is still allowed from somewhere. That is a fail, and
  the operator records an exception if it is intended.
- **Wildcard ARN patterns** (`arn:aws:s3:::logs*`) would need IAM's glob
  semantics. Read as not covering the bucket.
- **`BoolIfExists`** is not named in the condition-operator table, so it is
  not counted.

## D4. Inconclusive, not fail, for an unreadable policy

The ticket asks for the #2815 tri-state. An AccessDenied (403), a 405 for a
caller outside the owning account, a region lookup error and a policy that
is not valid JSON are all inconclusive, with the reason in `reason`. Only
`NoSuchBucketPolicy` is the observed absence of a policy, and that fails.

## D5. One kind per run, chosen with `--kind`

`run --kind` already existed with a single allowed value. It now accepts
either kind, and `register` announces both. One run per kind keeps the IAM
surface per run explicit, and leaves the encryption run's flags, output and
records unchanged. The TLS idempotency key is
`sha256(kind | bucket_arn | hour)` (see the defect above). The encryption
key is unchanged, so encryption records keep deduplicating across the
upgrade.

## Falsifiers run

| Mutation                                 | Caught by                                                                |
| ---------------------------------------- | ------------------------------------------------------------------------ |
| `enforced-passes` expected `fail`        | `TestStockSQLBundleTests_PassAgainstPostgres`: "expected fail, got pass" |
| TLS record keyed on the bucket ARN alone | `TestBuildTLSRecord_IdempotencyKeyDiffersFromEncryption`                 |
