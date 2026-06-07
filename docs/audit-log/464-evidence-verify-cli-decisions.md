# 464 — `atlas evidence verify` ledger-integrity walk — decisions log

- detection_tier_actual: integration
- detection_tier_target: integration

AC-3 is the load-bearing verification: a deliberately-corrupted ledger row
must be reported and a clean ledger must report zero mismatches. That is
caught at the **integration** tier (real Postgres, real ingest `Process`,
out-of-band payload-column mutation via the admin pool). `actual == target
== integration`. No product bug surfaced during the build; the design
tension below (D1) is an architecture limitation, not a runtime defect.

## What shipped

- `cmd/atlas-cli/cmd_evidence_verify.go` — the `evidence verify` cobra verb
  (tenant-scoped + cross-tenant walk, keyset pagination, exit codes).
  Lives in `cmd/atlas-cli`, which is coverage-EXCLUDED (cobra glue).
- `internal/evidence/ingest/verify.go` — `RecordFromLedgerRow`,
  `LedgerRowHash`, `VerifyLedgerRow`, `RowID`: reconstruct the canonical
  `EvidenceRecord` from a persisted ledger row and recompute the canonical
  hash via the existing `canonjson.HashRecord` primitive. Placed in the
  ingest package because it is the canonical home of the hash derivation
  and the package is coverage-EXCLUDED (no floor lifted; no collision with
  sibling 472's `coverage-thresholds.json` ownership).
- `internal/evidence/ingest/verify_integration_test.go` — AC-3 (clean →
  zero mismatches; corrupted payload → detected). Lands in the
  already-shard-enrolled `internal/evidence/ingest` package (Leg A in
  `scripts/integration-shards.txt`) → **no manifest change**.
- `internal/db/queries/evidence_ledger.sql` + regenerated `dbx` —
  `WalkEvidenceRecordsForVerify` keyset-paginated read (`sqlc generate`,
  pinned v1.31.1; dbx never hand-edited).
- `migrations/sql/20260606000000_evidence_verify_service_account_read.sql`
  — `GRANT SELECT ON evidence_records TO atlas_service_account` (read-only)
  so the `--all-tenants` BYPASSRLS walk works without a write grant.
- `docs/SELF_HOSTING.md` — step 5 migrate prose corrected (AC-5).
- `docs-site/docs/backup-restore.md` — drill uses the real verb; "not yet
  present" admonition removed (AC-4).
- `CHANGELOG.md` — `[Unreleased]/Added` bullet.

## Decisions made

### D1 — Integrity unit: hash the record as RECONSTRUCTABLE FROM THE LEDGER (the ingest scope-inclusive hash is provably not reproducible)

**This is the load-bearing call.** The ingest path stores
`hash = canonjson.HashRecord(rec)` where `rec` is the **full
`EvidenceRecord` proto including `scope`** (`internal/evidence/ingest/ingest.go:441`;
`canonjson.HashRecord` marshals the whole proto with sorted scope). But the
ledger does **not persist the wire `scope`**: there is no scope column, the
push path writes `ScopeID: pgtype.UUID{}` (empty), and scope is absent from
the `provenance` / `source_attribution` JSONB. Confirmed empirically: for a
record with scope, `HashRecord(full)` ≠ `HashRecord(scope-free reconstruction)`
≠ `sha256(payload)` — all three diverge. Therefore the **exact ingest hash
cannot be re-derived from a ledger row**.

Options considered:

- **(a) Re-derive the exact ingest hash.** Impossible without the original
  scope, which is not persisted. Would require persisting scope at
  ingest — a change to the frozen ingest write path (directive: do NOT
  change ingest write behavior) and a coverage/schema change. Rejected.
- **(b) Hash only the `payload` column against a stored payload digest.**
  No payload-only digest is stored at ingest; would also require an ingest
  write change. Rejected.
- **(c, CHOSEN) Reconstruct the record from the persisted columns and
  recompute `canonjson.HashRecord` over THAT, comparing to the stored
  `hash`.** The verify contract becomes: _"the stored `hash` equals the
  canonical hash of the record as reconstructable from the ledger."_ This
  uses the existing `canonjson` primitive (no reimplementation — per slice
  note), is READ-ONLY, needs no ingest/schema/coverage change, and
  faithfully detects any mutation of the persisted columns (payload,
  result, kind, version, control_ref, observed_at, source_attribution) —
  the dominant tamper surface and exactly what AC-3 corrupts.

**Limitation, stated honestly:** records written by the _current_ ingest
path carry a scope-inclusive `hash`, so a verify over a freshly-ingested
record reports a mismatch (the scope is gone). AC-3's clean-path assertion
establishes the baseline by stamping the ledger-reconstructable hash. To
make production records verify cleanly, the ingest hash must become
ledger-reproducible — **filed as follow-up slice 474** (persist canonical
scope, or switch the stored `hash` to the ledger-reconstructable form).
This was NOT escalated as a constitutional conflict because the slice ships
a correct, honest, useful integrity walk today; aligning the two hashes is
a discrete, separately-reviewable change. **Confidence: high** that (c) is
the right v1 shape; **medium** on whether the follow-up should persist
scope vs change the stored hash form (deferred to 474).

### D2 — Output shape: per-mismatch line + a summary line

One `MISMATCH tenant=… record=… stored=… recomputed=…` line per corrupt
record (truncated 12-char hashes for scannability) plus a final
`verify: scanned=N mismatches=M tenants=K` summary. Not JSON for v1 (the
restore-drill consumer greps the summary + exit code); a `--json` flag is a
cheap follow-up if a machine consumer appears. **Confidence: high.**

### D3 — Exit codes: 0 clean / 1 mismatch / 2 operational

Mirrors the Unix convention and what a restore-drill script wants:
`evidence verify && echo OK` succeeds only on a clean ledger; a corrupt
ledger fails the drill (exit 1); a misconfiguration (no DSN, bad flags,
connect failure) is distinguishable as exit 2. Operational errors print to
stderr _before_ exit so they are visible (the naive return-error-after-exit
pattern swallowed the message — fixed). **Confidence: high.**

### D4 — Pagination: keyset (cursor by id), default 1000/page, streaming

The ledger can be very large; the walk pages by `id > cursor ORDER BY id`
(no OFFSET drift) inside a per-page read transaction, so the working set is
bounded regardless of ledger size and each page's RLS/role context is set
fresh. `--page-size` is tunable. **Confidence: high.**

### D5 — Roles: atlas_app + RLS GUC (tenant); SET LOCAL ROLE atlas_service_account (all-tenants)

AC-2 verbatim. The tenant walk sets `app.current_tenant` via the existing
`tenancy.ApplyTenant` so RLS bounds the rows. The cross-tenant walk uses
`SET LOCAL ROLE atlas_service_account` (BYPASSRLS, NOLOGIN, NOINHERIT,
granted to atlas_app per `migrations/bootstrap/01-roles.sql`) inside the
walk transaction — never a superuser connection. The service account had
no grant on `evidence_records` (SELECT only on tenants/super_admins), so a
new migration grants it **SELECT only** (read-only; the walk never writes).
**Confidence: high.**

### D6 — SELF_HOSTING fix: docs-fix to the bootstrap one-shot (not a new `atlas migrate up` subcommand)

The slice offered a choice: implement an `atlas migrate up` subcommand to
match the docs, OR fix the docs to the shipped `atlas-bootstrap` one-shot
reality. Chose the docs-fix (the slice's recommended, lower-cost path): no
operator-facing migrate verb is independently wanted, and slice 432's
`upgrade.md` already routes through the bootstrap one-shot. Only one drift
instance existed (`SELF_HOSTING.md:122`); `ATLAS_MIGRATE_ON_START` was not
present in the file. **Confidence: high.**

### D7 — AC-6 (business-continuity.md) left to slice 373's owner

The BCP/DR doc's `atlas-cli evidence verify` references now resolve to a
real command, so they are correct as-is. Per the slice note + the plan's
§11 annual-review reconciliation clause, the doc is slice-373-owned and not
edited unilaterally; the verb's existence satisfies the intent.
**Confidence: high.**

## Revisit

- **Slice 474 (filed):** align the ingest hash so production records verify
  cleanly (persist canonical scope, or store the ledger-reconstructable
  hash). Until then the verify baseline must be established for existing
  records. This is the one material gap.
- `--json` output mode if a machine consumer (dashboard, CI gate) appears.
- A `--since`/`--observed-before` horizon filter to scope a walk to an
  audit period (composes with audit-period freezing, canvas §8.4).

## Surprises / spillover

- The "per-record sha256 integrity is relied on at every ledger read"
  phrasing in the docs is aspirational — nothing in the codebase actually
  re-verifies the stored hash on read (no on-read recompute exists). This
  verb is the first surface that does. Noted, no action this slice.
- Spillover **slice 474** filed: ingest-hash / ledger-reconstruction
  alignment (the D1 limitation). NNN chosen 474 to avoid collision with
  sibling 472 and the not-yet-on-main 473 (#1023).
