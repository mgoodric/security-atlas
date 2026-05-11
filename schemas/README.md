# schemas/

JSON Schemas (draft 2020-12) for every registered `evidence_kind`. Each kind has a stable identifier (e.g., `aws.s3.bucket_encryption_state.v1`), a versioned JSON Schema, an owner attribution, and default SCF anchor mappings.

The schema registry service (slice 014) ingests schemas from this directory at platform startup and serves them at `/v1/schemas/<kind>/<semver>`.

Empty in slice 001. First schemas land with slice 014 + slice 004 (AWS S3 encryption).
