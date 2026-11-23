# #2950 — cc6_7 retitled to what it checks (decisions log)

The stock bundle `soc2_cc6_7_encryption_in_transit` was titled "Data is
encrypted in transit; storage encryption-at-rest enforced". Since #2825 it
checks one thing: S3 default encryption at rest, over
`aws.s3.bucket_encryption_state.v1`. Nothing it runs looks at transport.

- detection_tier_actual: manual_review
- detection_tier_target: manual_review

## D1 — Retitle, keep the bundle id

New title: "S3 buckets enforce default encryption at rest".

The bundle id stays `soc2_cc6_7_encryption_in_transit`. An id rename is
worse than the misleading name:

- `stock-supersede-check` pairs bundles by `bundle_id`, so a renamed id
  reads as one bundle removed and one added, neither of which it treats
  as a supersede. It would pass silently.
- Bootstrap re-uploads the bundles that exist. Nothing retires a stock
  control whose bundle id disappears, so the old control would stay active
  beside the new one, holding the owners, evidence, exceptions and links.

An id rename needs a retire path for missing stock bundles and a tripwire
that flags id renames first. That is its own ticket, not this one. The
description (an in-place key) now says why the id still reads "in transit".

## D2 — The title change supersedes; fold it into #2825's

`title` is not in `inPlaceManifestKeys` (`description`, `external_refs`,
`anchor_fit_note`; `internal/control/supersede.go`), so this upload mints a
new control version. The PR carries `supersedes-stock`, and this log names
the bundle id, as `stock-supersede-check` requires.

#2825 already supersedes this bundle (its query changed), and
`changelog.d/2825.changed.md` is not yet released. An install upgrading
from the last release therefore supersedes cc6_7 once, picking up the
query and the title together. Only an install that booted from `main`
between #2825 and this change supersedes it a second time. The #2405 log
made the same call for `control_family`.

## D3 — Hash list stays append-only

`bash scripts/gen-stock-control-hashes.sh` adds the new control.yaml hash
and keeps every existing entry (#2963): 294 entries, and
`scripts/check-stock-hashes-superset.sh` against `main` reports all 293 of
the base's entries held.

## D4 — In-transit coverage is a separate ticket

CC6.7's in-transit prong (TLS-only endpoints) has no evidence source: the
AWS connector emits only `aws.s3.bucket_encryption_state.v1` and
`aws.s3.encryption_status.v1`. #2993 tracks a collector for the S3 bucket
policy's `aws:SecureTransport=false` deny, a new evidence kind and a control.
