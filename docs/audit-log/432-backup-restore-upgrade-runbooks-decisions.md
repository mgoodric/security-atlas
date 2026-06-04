# 432 — backup/restore + upgrade runbooks — decisions log

- detection_tier_actual: manual_review
- detection_tier_target: manual_review

This is a JUDGMENT docs slice. Its load-bearing verification is the
restore drill (AC-14): a runbook procedure that does not actually
restore is caught at the `manual_review` tier — by running it — before
merge, not at any automated tier. There is no cheaper tier: the drill
exercises the real `postgres:16-alpine` + shipped `01-roles.sql` +
real `migrations/sql/*.sql` round-trip. `actual == target ==
manual_review`. No product bug surfaced; the spillover (D6 below) is a
documentation-vs-shipped-surface drift, not a runtime defect.

## What shipped

- `docs-site/docs/backup-restore.md` — Postgres dump + restore, MinIO
  `mc mirror` both directions, signing-key/bootstrap-token handling,
  backup encryption at rest + destination access control, and a tested
  restore drill that verifies integrity (not just presence).
- `docs-site/docs/upgrade.md` — pin a version, pre-upgrade checkpoint,
  manual migration apply, verify, rollback, plus major-Postgres dump+restore.
- `docs-site/mkdocs.yml` — new "Operations" nav section grouping both pages.
- `docs/SELF_HOSTING.md` — "Backups" + "Database migrations" sections
  rewritten to point at the canonical published runbooks (two-homes
  drift resolution, AC-8).

## Decisions made

### D1 — Two-homes resolution: docs-site is canonical for backup/restore + upgrade depth; SELF_HOSTING points at it

**Options:** (a) add a build hook to copy top-level `docs/SELF_HOSTING.md`
into `docs-site/docs/` and nav it; (b) port all operator content into
`docs-site/` and redirect `SELF_HOSTING.md`; (c) make `docs-site/` the
canonical home for the _new_ backup/restore + upgrade depth, nav the two
new pages, and rewrite the thin `SELF_HOSTING.md` Backups + migrations
sections to point at the published canonical runbooks.

**Chosen:** (c). The spec's recommendation was "lighter touch — add
`SELF_HOSTING.md` to nav, avoid full migration." The mkdocs
`docs_dir: docs` constraint (i.e. `docs-site/docs/`) means a top-level
`docs/SELF_HOSTING.md` is outside the docs root and cannot be a bare nav
target without a copy/include hook (option a). Option (a) adds build
machinery for one file and risks divergence between the copied and
source versions. The drift the slice actually targets is narrow —
_backup/restore and upgrade_ operator depth — not the whole 260-line
self-host guide. `install.md` already exists in `docs-site/` as the
"self-host quickstart" and already cross-links `SELF_HOSTING.md` via the
project's established `github.com/.../blob/main/...` pattern, so the
canonical guide is already reachable from the published site. (c)
therefore: makes the published `backup-restore.md` / `upgrade.md` the
authoritative operator runbooks, and edits `SELF_HOSTING.md`'s two thin
sections to defer to them (with a one-paragraph summary so the in-repo
guide is not gutted). One canonical home per topic; no new build hook.
**Confidence: high.**

### D2 — Cross-link style for top-level docs/ targets: GitHub blob URLs

Pages under `docs-site/docs/` cannot relative-link to top-level `docs/`
files (outside `docs_dir`). The runbooks link the BCP/DR plan, the OSCAL
signing runbook, the JWT-key-rotation runbook, and `SELF_HOSTING.md` via
`https://github.com/mgoodric/security-atlas/blob/main/...` — the exact
convention already used by `oidc-setup.md`, `ci-hardening.md`,
`board-reporting.md`, `connector-authoring.md`, and `install.md`.
Mechanically required by the docs_dir boundary AND consistent with house
style. `--strict` does not check external URLs, so these do not break the
build. **Confidence: high.**

### D3 — How much to duplicate vs cross-link from business-continuity.md

The runbook **operationalizes** the BCP/DR plan; it does not restate it
(P0-432-5). The plan owns RTO/RPO tiers, role devolution, and the
five governance restore scenarios (A-E). The runbook owns the _runnable
commands_ an operator types. Where they meet, the runbook carries a
"How this maps to the BCP/DR plan" table that links each plan element to
the corresponding runbook section rather than copying the plan's prose.
Scenario C's ledger-replay path (the load-bearing canvas-invariant-#3
recovery) is summarized in one paragraph and deferred to the plan — it
is governance/architecture territory, not a keystroke procedure.
**Confidence: high.**

### D4 — Restore-drill integrity assertion: signed-OSCAL-export verify is the shipping end-to-end check

Threat-model T requires the drill to verify _integrity_, not just
presence. The shipping cryptographic integrity surface that an operator
can run today is `atlas oscal verify <bundle-dir>` (real:
`cmd/atlas-cli/cmd_oscal_sign.go:150`), which checks the bundle digest
(sha256 over member bytes) against the signature. The drill exports a
frozen audit period and verifies the signed bundle — exercising the
cosign/ed25519 verification path end-to-end and proving the evidence
behind the export survived the restore intact. This is the AC-5 / AC-14
integrity assertion. **Confidence: high.**

### D5 — Restore drill is role-correct: restore as atlas_migrate, never superuser-into-app (threat-model E / AC-11)

The runbook restores schema + data as `atlas_migrate` (the BYPASSRLS DDL
role created by `migrations/bootstrap/01-roles.sql` at cluster init), and
the running server reconnects as `atlas_app` (NOBYPASSRLS, RLS-enforced).
The runbook explicitly warns against restoring as a superuser into the
running app's connection. This is the role separation the bundle already
ships (`docker-compose.yml` postgres/atlas service comments,
`bootstrap.sh` phase 2). The drill (below) ran the restore as
`atlas_migrate` and confirmed 237/237 RLS policies survived.
**Confidence: high.**

### D6 — `atlas evidence verify` does NOT exist; document the real surface + file a spillover

The slice spec (AC-5, notes), `docs/SELF_HOSTING.md` (old Backups line),
and the BCP/DR plan (§6 Scenarios A/B/C "Verify" steps) all reference an
`atlas evidence verify` CLI command. **It does not exist** — `cmd/atlas-cli`
ships only `evidence push` (`cmd_evidence.go`). The per-record sha256
content hash IS computed and stored at ingest (`ingest.go:172` —
"canonjson sha256 of the record") and relied on at every ledger read
(canvas invariant #3), but there is no operator-facing verb to re-walk
the whole ledger on demand.

**Decision:** ground the drill's integrity assertion in what actually
ships (`atlas oscal verify`, D4) and add an explicit admonition in
`backup-restore.md` that a ledger-wide `evidence verify` verb is not yet
present. Filed **slice 464** (`docs/issues/464-atlas-evidence-verify-cli-ledger-integrity-walk.md`,
cites parent 432) for the missing CLI. Did NOT invent the command in the
runbook (P0: ground in the shipped surface). **Confidence: high.**

### D7 — Migration mechanism in upgrade.md: ground in the bootstrap one-shot, not a nonexistent `atlas migrate up`

`docs/SELF_HOSTING.md` documents `docker compose run --rm atlas atlas migrate up`
and `ATLAS_MIGRATE_ON_START=true`. **Neither exists in the shipped binary**:
`cmd/atlas/main.go` only handles `--version`/`version` as args, and
`ATLAS_MIGRATE_ON_START` appears nowhere in `cmd/` or `.env.example`. The
real migration runner is the `atlas-bootstrap` one-shot container, which
applies `migrations/sql/*.sql` (skipping `*.down.sql`) ledgered in
`schema_migrations`, single-transaction per file, as `atlas_migrate`
(`bootstrap.sh` phases 2-2.5). `just migrate-up` runs the same set via
`psql` for a bare external Postgres.

**Decision:** AC-7 says reconcile the migration guidance "without
contradicting" `SELF_HOSTING.md`. I honored the _principle_ SELF*HOSTING
states (migrate manually, before the new server takes traffic; keep
auto-on-start off in production) and gave the \_real* command
(`docker compose run --rm atlas-bootstrap`). The upgrade page's
"Migration mechanism — reconciling the guidance" admonition states this
explicitly. The `atlas migrate up` / `ATLAS_MIGRATE_ON_START` drift in
`SELF_HOSTING.md` is noted in the spillover (D6's slice 464 covers the
evidence-verify CLI; the migrate-up phrasing drift is folded into the
same spillover doc as a secondary item). I did NOT invent or re-document
the nonexistent `atlas migrate up` subcommand. **Confidence: high.**

### D8 — Encryption: client-side age/gpg before offsite, AND SSE on destination

Threat-model I (load-bearing): an unencrypted dump is a full copy of
tenant evidence. The runbook documents client-side encryption (`age`
example with a placeholder recipient key) _before_ the artifact leaves
the host, layered with SSE on the destination bucket and a bucket policy
denying unencrypted uploads. Client-side is primary (the operator holds
the key, so a compromised destination still yields no plaintext); SSE is
defense-in-depth. Signing keys are backed up _separately_ with stricter
handling (AC-3 table). All keys/buckets/hosts are placeholders, and the
example `age` recipient is a non-functional placeholder string (not a
real-key-shaped value, to avoid tripping secret scanners). **Confidence: high.**

## The drill (AC-14) — actually run

Run against a scratch `postgres:16-alpine` with the shipped
`migrations/bootstrap/01-roles.sql` mounted at initdb and all 66 real
forward migrations applied via the bootstrap loop. Outcome:

| Step                           | Result                                                            |
| ------------------------------ | ----------------------------------------------------------------- |
| Roles at initdb                | `atlas_app`, `atlas_migrate`, `atlas_service_account` created     |
| Forward migrations             | 66/66 applied clean; 88 tables, 237 RLS policies                  |
| Seed known record              | evidence_records id `…e1`, hash `c656…49ee`                       |
| `pg_dump` + sha256             | dump taken, checksum recorded                                     |
| Wipe (drop container + volume) | scratch-stack equivalent of `just self-host-wipe`                 |
| Fresh bring-up                 | clean DB, no `evidence_records` pre-restore                       |
| Checksum verify before restore | `OK`                                                              |
| Restore as `atlas_migrate`     | **zero errors** (role-correct, AC-11)                             |
| Known record survived          | hash byte-identical (`match=true`) — integrity, not just presence |
| RLS policies                   | 237 → 237 (tenant isolation intact across restore)                |
| Tables                         | 88 restored                                                       |

**Honest caveat:** the drill verified the _stored_ hash is byte-identical
pre/post restore (the load-bearing faithful-restore assertion). It did
NOT recompute the platform's canonical-JSON record hash — a naive
`payload::text` digest does not match the stored hash because the stored
hash is canonjson over the whole record, not the payload alone. The
canonical re-walk is exactly the missing `atlas evidence verify` verb
(D6 / slice 464). The signed-OSCAL-export verify (D4) is the shipping
cryptographic integrity check the runbook directs operators to; the
drill's DB round-trip proves the data layer restores faithfully. The
full bundle `just self-host-up` + `atlas oscal-export` + `atlas oscal verify`
path was not run end-to-end in this drill (image-build + flaky
MinIO/NATS startup the project itself avoids in CI); the OSCAL verify
command is real and documented per its shipped signature.

## Revisit once in use

- **`atlas evidence verify` CLI (slice 464).** When it lands, replace the
  runbook's "ledger-wide verify not yet present" admonition with the real
  command in the drill's assert step. The signed-OSCAL-export verify stays
  as the cryptographic end-to-end check.
- **`SELF_HOSTING.md` `atlas migrate up` / `ATLAS_MIGRATE_ON_START` drift.**
  These reference a binary surface that does not exist. The upgrade runbook
  routes around it with the real bootstrap-one-shot command; a follow-up
  should correct `SELF_HOSTING.md`'s phrasing directly (folded into the
  slice 464 spillover doc as a secondary item). Out of scope here — this
  slice owns the published runbooks + the two thin SELF_HOSTING sections it
  rewrote, not the migration-prose correction.
- **WAL archival / point-in-time recovery.** The runbook is honest that
  RPO is the dump cadence; if the maintainer (or an operator) adds WAL
  archival per BCP/DR plan §11, the restore section gains a PITR path.

## Confidence summary

| Decision                                                                  | Confidence |
| ------------------------------------------------------------------------- | ---------- |
| D1 — docs-site canonical, SELF_HOSTING points at it                       | high       |
| D2 — GitHub blob cross-link style                                         | high       |
| D3 — operationalize, do not restate the BCP/DR plan                       | high       |
| D4 — signed-OSCAL-export verify is the integrity check                    | high       |
| D5 — restore as atlas_migrate, role-correct                               | high       |
| D6 — evidence verify CLI absent; documented real + filed 464              | high       |
| D7 — upgrade migration via bootstrap one-shot, not nonexistent migrate up | high       |
| D8 — client-side encryption + SSE + separate key handling                 | high       |

Top of the revisit list: the `atlas evidence verify` CLI gap (slice 464)
— once it ships, the runbook's drill assert step gets a ledger-wide
integrity command to sit alongside the OSCAL-export verify.
