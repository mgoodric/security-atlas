# 004 — AWS connector (S3 encryption evidence_kind, end-to-end)

**Cluster:** Spine
**Estimate:** 3d
**Type:** AFK

## Narrative

Build the first connector as a tracer bullet through the entire ingestion pipeline. Connector lives at `connectors/aws/`, implements the `Describe`/`AuthMethods`/`HealthCheck`/`ListEvidenceKinds`/`Pull` gRPC methods. Single evidence_kind for this slice: `aws.s3.bucket_encryption_state.v1` — pulls each S3 bucket's encryption configuration, emits one evidence record per bucket per scope cell. Auth via IAM role assumption (least-privilege read-only). Run on a schedule; results land in the ledger via the push endpoint (slice 013). The slice delivers value because for the first time a real cloud signal flows end-to-end and produces a queryable evidence record tied to a control.

## Acceptance criteria

- [ ] AC-1: `aws-connector` binary registers with the platform on startup and appears in `GET /v1/connectors`
- [ ] AC-2: Manual trigger `just connector-run aws s3.bucket_encryption_state.v1` against a test AWS account produces N evidence records (one per bucket)
- [ ] AC-3: Each record carries provenance fields: `source_attribution.actor_type=connector`, `actor_id=aws-connector@<version>`, `source_record_key=arn:aws:s3:::bucket-name`
- [ ] AC-4: Buckets with `BucketEncryption` set return `result=pass`; unencrypted buckets return `result=fail`
- [ ] AC-5: Each record is tagged with scope `cloud_account=aws:<account-id>` and environment inferred from the AWS account tag
- [ ] AC-6: Re-running within the idempotency window does not produce duplicate records (idempotency_key derived from bucket ARN + observed_at hour)

## Constitutional invariants honored

- **Invariant 2 (ingestion/eval separated):** connector writes to ledger via push API; never touches evaluation state
- **Invariant 3 (two SDK profiles):** connector profile (pull) exercised end-to-end

## Canvas references

- `Plans/canvas/04-evidence-engine.md` §4.1 (connector profile), §4.2 (v1 connector roster — AWS as cloud baseline)
- `Plans/EVIDENCE_SDK.md` §3 (connector profile recap)

## Dependencies

- #002, #003, #013, #014

## Anti-criteria (P0)

- Does NOT install or require an agent on AWS-side infrastructure
- Does NOT use AWS access keys — must use IAM role assumption
- Does NOT write evaluation state — only evidence records
- Does NOT skip scope-tagging — every record must carry scope coordinates

## Skill mix (3–5)

- Go + AWS SDK v2
- gRPC connector contract
- IAM role assumption + STS
- Idempotency key derivation
- Postgres ledger client (for push round-trip verification in tests)
