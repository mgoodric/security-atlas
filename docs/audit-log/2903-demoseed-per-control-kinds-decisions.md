# 2903 — Demo-seed evidence kinds per control, and a real-backend render check: decisions log

Issue: mgoodric/security-atlas#2903. Follows #2863 / PR #2899.

## Detection-tier classification

- **detection_tier_actual:** manual_review. Found in review of PR #2899:
  its third acceptance check (the seeded S3 rows render) had no spec that
  could answer it, and the kind assignment was round-robin.
- **detection_tier_target:** unit for the kind assignment
  (`TestBuildFixtures_EvidenceCarriesItsControlsKind`), playwright for the
  render (`web/e2e/demoseed-evidence-2903.spec.ts`).

## Decisions

### D1 — a demo control's kind comes from a title map, not from a bundle query

The ticket asks for each row's kind to come from "its control's bundle".
Demo controls have no such kind: `writeControls` gives each one a
synthetic `demo-bundle-<id>` with no `evidence_queries`, so the engine
rolls their rows up per record (`evalQueries`' no-query branch). There is
no declared `evidence_kind` to read.

So the seed now states one: `controlEvidenceKinds` maps each of the 50
control titles to the kind its subject produces (S3 bucket encryption for
encryption-at-rest and the cloud-configuration controls, the Okta MFA
policy for the MFA control, host posture for the endpoint controls,
manual attestation or upload for the process controls). Each row takes
its control's kind. `TestControlEvidenceKinds_CoverEveryTitle` fails when
a title is added without a kind, or a kind outside `evidenceKindsPool` is
named.

Not done: writing `evidence_queries` on demo controls. That would make the
demo verdicts come from query logic instead of the rows' stored results,
and it is a larger change than the ticket asks for.

### D2 — the S3 algorithm alternates on the row's place among its kind

`buildEvidencePayload` alternated `sse_algorithm` on
`(idx/len(evidenceKindsPool))%2`, which assumed the round-robin. The seed
now passes the row's ordinal among rows of its kind, and S3 alternates on
`idx%2`. The seeded pass rows still show both AES256 and aws:kms
(`TestBuildEvidencePayload_S3AgreesWithResult`).

### D3 — every row of a control has one result, so S3 has no failing control

Rows go to controls `i%50` and results are `i%10`, so each control's rows
share one result (true before #2903 too). The four S3 controls fall on
pass, pass, pass and inconclusive. The demo therefore shows no failing
S3 control. Mapping an unrelated control (one that lands on `fail`) to
S3 to get one would make the mapping wrong, so this is left as is.

### D4 — the render check seeds through `atlas-cli demo seed` in the spec

The spec runs `go run ./cmd/atlas-cli demo seed --tenant-slug
e2e-demoseed-2903` in `beforeAll`, with `ATLAS_ENABLE_DEMO_SEED` and
`ATLAS_ALLOW_MULTI_TENANT` set on that child process only. The atlas
server CI runs keeps both off, so no other spec sees the HTTP demo routes
enabled. The seed is idempotent, so a re-run finds the tenant and changes
nothing. The spec reads from Postgres which controls hold S3 rows and
their results, then asserts `/evidence?control_id=…` lists exactly those
rows with that kind and result. The file runs serially, so two workers
never race to seed the same slug.

Alternative rejected: a SQL fixture under `fixtures/e2e/`. That would test
a hand-written copy of the seed's rows, not the seed.
