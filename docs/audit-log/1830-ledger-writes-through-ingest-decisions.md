# #1830 — every evidence-ledger write goes through ingest — decisions log

**Ticket:** https://github.com/mgoodric/security-atlas/issues/1830 (from the
#1824 review of #1751). Related: #1699, #1742.

## The defects

| Writer                           | Defect on `main`                                                                                                                                         |
| -------------------------------- | -------------------------------------------------------------------------------------------------------------------------------------------------------- |
| `internal/change/store.go`       | `InsertEvidenceRecord` directly: no audit row, no schema validation, no kind scope, a non-canonical hash `atlas evidence verify` cannot recompute.       |
| `internal/incident/store.go`     | Same, and `observed_at` comes from the `POST /v1/incidents/{id}/close` body with no skew check, so a postmortem could be backdated into a frozen period. |
| `internal/personnelsecurity`     | Same as incident: caller `observed_at`, no skew check, no audit row, `sha256:`-prefixed non-canonical hash, no `credential_id`.                          |
| `internal/accessreview/store.go` | Raw `INSERT INTO evidence_records`: no audit row, no schema validation, empty `scope_canonical`.                                                         |
| `internal/demoseed/writers.go`   | Raw `INSERT INTO evidence_records` of a backdated dataset, with nothing confining it to demo tenants beyond the caller's pre-checks.                     |

- **`detection_tier_actual`: `manual_review`** (the #1824 review).
- **`detection_tier_target`: `unit`** — a repo-wide test that fails on a
  direct ledger write (now `TestNoDirectEvidenceLedgerWrites`). Before it,
  nothing stopped a new store from writing the ledger itself.

## Decisions

### D1 — An in-transaction variant, `ingest.Service.ProcessInTx`. **Confidence: HIGH.**

The four domain writers insert their evidence inside the same transaction as
the domain change (change transition, incident close, checklist completion,
campaign completion), and link the evidence id back in that transaction.
`Process` opens its own transaction on the pool, so calling it would split
the write: a domain rollback after an accepted `Process` leaves orphan
evidence, and the domain rows could not reference the record atomically.

`ProcessInTx` shares `Process`'s pre-write stage (`prepare`) and write stage
(`persist`) unchanged and differs only in where the rows land:

- the record and an **accepted/deduplicated** audit row go into the caller's
  transaction, so they commit or roll back together;
- a **refusal's** audit row goes into its own short transaction (the existing
  `writeAudit` path), so `rejected_observed_at_skew` survives the caller
  rolling back on the returned error. That is the #1751 contract.

`ProcessInTx` refuses a context tenant that differs from the credential's.

### D2 — The INSERT runs under a savepoint. **Confidence: HIGH.**

`persist` re-fetches on a lost idempotency race (UNIQUE violation). Outside a
savepoint Postgres aborts the whole transaction on that error, so the
re-fetch could never run, and under `ProcessInTx` it would also poison the
caller's transaction. `pgx.BeginFunc(tx, …)` makes the INSERT a savepoint in
both paths. This also makes `Process`'s existing race arm reachable.

### D3 — Each domain writer ingests under a kind-scoped system credential. **Confidence: MEDIUM.**

`ingest.SystemCredential(id, tenant, kinds...)` with the ids the raw inserts
already used where they had one (`system:change-register`,
`system:incident-register`, `access_review_campaign`) and
`system:personnel-security` where they had none. `Kinds` is set, so the
credential kind-scope check bounds each store to its own kinds, the same as a
connector key. Ingestion path stays `manual_upload` (`ingest.DomainWriterPath`).

### D4 — `incident.postmortem.v1` registered as a platform schema. **Confidence: MEDIUM.**

The other three kinds were already registered. The incident kind was not, so
ingest would refuse it as unknown. Added
`schemas/incident.postmortem/1.0.0.json` and the `DefaultSeed` row. The SCF
anchor is `IRO-09`, matching `pagerduty.postmortem_summary`: `IRO-13` (root
cause) is absent from the bundled catalog fixture, which
`default_anchor_catalog_test` enforces.

### D5 — Access review keeps its freshness window. **Confidence: HIGH.**

The raw insert set `freshness_class = quarterly` and `valid_until = now + 400d`;
ingest's push path writes `monthly` and no `valid_until`. Dropping to monthly
would mark a quarterly review stale after a month. `ingest.LedgerOptions`
carries both overrides; the zero value keeps the push defaults.

### D6 — Ledger columns that changed for these writers. **Confidence: MEDIUM.**

Going through ingest normalizes what was ad hoc: the hash is the canonical
record hash (verifiable by `atlas evidence verify`), `provenance` and
`source_attribution` take ingest's shape, each record carries a non-empty scope
dimension (ingest requires one: `change_id`, `incident_id`,
`access_review_campaign_id`, or the existing personnel-security pair), and
`control_ref` holds the resolved control UUID when there is one (access review
previously kept the bundle slug in `control_ref` even when `control_id` was
resolved). Existing rows are untouched; nothing reads the old hash format.

### D7 — Fail closed without an ingest service. **Confidence: HIGH.**

`WithIngest(nil)` leaves the store without one, and `ProcessInTx` on a nil
`*Service` returns `ingest.ErrUnavailable`. The incident and personnel-security
handlers map it to 503 with a fixed body; the incident handler maps
`ErrObservedAtSkew` to 400, since `observed_at` is caller input there. The
production server wires `s.ingestService` into both stores. `change` and
`accessreview` are not mounted by any server yet, so they gain the method and
tests only.

### D8 — demoseed stays a raw insert, as a guarded exception. **Confidence: HIGH.**

The demo dataset spreads `observed_at` over twelve months of audit periods;
the 24h skew check exists to refuse exactly that, so routing it through ingest
would either fail or need a skew bypass in ingest, which is the worse hole.
Instead `writeEvidence` calls `assertFreshDemoTenant` first: the tenants row
and the `demo_seed_apply` `super_admin_audit_log` row for its slug must both
have been written by the current transaction (`xmin` equals the transaction
id). A real tenant, or a demo tenant seeded earlier, is refused with
`ErrNotDemoSeedContext` before any row is written.

The mark is checked in `super_admin_audit_log` by slug, not `me_audit_log` by
tenant id, because `writeAuditLogRow` stamps `me_audit_log` with the actor's
tenant when there is one.

### D9 — The guard is a Go test, not a linter rule. **Confidence: MEDIUM.**

`TestNoDirectEvidenceLedgerWrites` (unit tier, `internal/evidence/ingest`)
scans every non-test `.go` file outside `internal/evidence/ingest` and the
generated `internal/db/dbx` for `.InsertEvidenceRecord(`, a raw
`INSERT INTO evidence_records` (also across line breaks) or a COPY into the
table. `ledgerWriteExceptions` names `internal/demoseed/writers.go` with its
reason; a stale exception fails the test too. A custom golangci analyzer
would need a plugin build; a test runs in the existing `Go · build + test`
check with no new tooling.
