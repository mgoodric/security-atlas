# #2940: manual connector payloads vs `manual.upload` 1.0.0, and `observed_at` on re-runs (decisions log)

Found while building #2824, whose cc6_5 (asset disposal) and cc9_1
(business continuity) SQL checks read `manual.upload.v1`. No migration. No
control bundle changes. `manual.upload.v1` stays a bound kind (#2924,
ADR 0028 decision 9).

- detection_tier_actual: manual_review
- detection_tier_target: unit

## D1: a `manual.upload` 1.1.0 schema declares the per-path fields

Every path's payload was rejected by `manual.upload/1.0.0.json`
(`additionalProperties: false`), confirmed by
`TestConnectorRecords_RejectedBy100`, which runs each builder's real record
through `schemaregistry.NewEmbedded(...).ValidatePayload`, the check ingest
applies to every push.

Two choices were open: a 1.1.0 that declares the fields, or a connector
that drops them. **Choice: 1.1.0.** The fields are the evidence's
provenance. For the local path, `payload_b64` is the content itself: a CSV
row carries the serial numbers, dates and disposal methods cc6_5 describes.
Dropping it would leave a record naming a file and nothing from it.
`bucket`/`etag`/`last_modified` and `host`/`remote_path`/`mtime` are what
an auditor needs to find the source artifact again.

1.1.0 is additive over 1.0.0 (`CheckAdditiveOver`, pinned by
`TestManualUpload110_AdditiveOver100`). Every new field is optional, so CLI
and SDK pushes at 1.0.0 shapes stay valid under both versions (ADR 0028
decision 8). Nothing reads the new fields yet, and no `format` keyword is
used, so #2981's `x-assert-format` opt-in does not apply.

## D2: `observed_at` stays the run hour, not the source timestamp

The ticket asked whether S3 `last_modified` or SFTP `mtime` should become
`observed_at`. **Choice: no.** Ingest refuses an `observed_at` more than
`MaxObservedAtSkew` (24 h) from receipt, in either direction. A source
timestamp would be refused for any file uploaded more than a day before
the connector first saw it. That is the normal case on a first run over an
existing bucket, and for any connector scheduled less often than daily.
The only way round the bound would be to fall back to the run time when
the source time is too old. That gives `observed_at` two meanings,
depending on how stale the file is, and a check could not tell which one
it is reading.

`observed_at` therefore means "the hour the connector first pushed this
version of the item", and the source time sits in the payload
(`last_modified`, `mtime`) for a future check that wants it. That is the
reading cc6_5 and cc9_1 already state ("`observed_at` is when the record
was pushed, which is not always when the file was written"), so their
descriptions stay true. D3 makes "first pushed" hold across re-runs.

## D3: keys name the source item, and `AlreadyExists` means already ingested

The ticket said S3 and SFTP dedupe on re-run. That holds within one hour
only. Ingest's content hash (`canonjson.HashRecord`) covers the full
record, `observed_at` included. A re-run in a later hour rebuilds the same
key with a new `observed_at`, so the hash differs and ingest returns
`ErrIdempotencyMismatch` (gRPC `AlreadyExists`). `doS3` and `doSFTP`
returned that as an error, so any re-run after the first hour aborted on
the first unchanged object. Found by reading `persist` in
`internal/evidence/ingest/ingest.go`, not by a live run.

Changes:

- **Local key:** `LocalRowKey(file_path, row_index, row_json)` hashes the
  row's encoded content in place of the run hour. An unchanged row
  reproduces its key, and an edited row gets a new one. This matches the
  role the ETag plays for S3 and mtime for SFTP.
- **`pushRecord`:** all three loops treat `AlreadyExists` as "this version
  of the item is already in the ledger". They count it and move on. Every
  other error still aborts the run. This reading is sound only because no
  key carries anything about the run. When a key names one version of one
  source item and the hash still differs, the difference can only come
  from the run's own inputs: `observed_at`, or the `--control-id` and
  `--scope` flags (see Known limits). The ledger keeps the first record,
  and its `observed_at` is the one that stands. Covered by `TestPushRecord` (including the SDK's
  `%w` wrapping) and `TestBuildLocalRecord_KeyIgnoresRunTime`.

Two things were considered and not done:

- A platform-side "dedupe ignoring `observed_at`" rule. The receipt hash
  is the EVIDENCE_SDK contract that `atlas evidence verify` recomputes.
  Changing what it covers is out of scope, and the connector does not need
  it.
- Deriving `observed_at` deterministically from the item. D2 covers why
  that fails the skew bound.

## Known limits

- **The local key change re-pushes each row once.** After upgrading, the
  first local run writes every row once under its new key, with that
  run's `observed_at`. An old CSV re-run after the upgrade therefore counts
  as new once. Subsequent runs dedupe.
- **The key does not include control or scope.** Re-running an unchanged
  item with a different `--control-id` or `--scope` reuses the key, so the
  item is reported as already ingested and not re-bound. Before this
  change S3 and SFTP aborted the run in that case. To push to a second
  control, change the item or push it directly.
