# OPENENGINE-659 — CSV completion import: decisions log

Follow-up to OPENENGINE-626 (security-awareness training primitive).
Scope: bulk CSV import of training completions + phishing-sim results
against existing assignments, and a scoped design decision (decision
only) for the future LMS connector.

## D1 — Contract doc lives at `docs/spec/`

The issue asked for the CSV contract "where the repo documents connector
contracts". The established connector-contract surface is
`connectors/<name>/README.md`, but that pattern documents connector
_binaries_ — separate processes that push evidence via `IngestEvidence`.
This importer is a platform-side store API (`internal/securityawareness`),
not a connector process, so the contract landed at
`docs/spec/security-awareness-csv-completion-import.md`, following the
`docs/spec/control-bundle.md` precedent for platform-side data contracts.
If a future slice wraps the importer in a CLI or upload endpoint, that
surface's docs should link here rather than fork the contract.

## D2 — Person keying: `source_person_id` preferred, `work_email` fallback

OE-659's "if blocked" note worried about duplicate `work_email` across
people. The schema already forecloses it:
`security_training_people_email_unique` is a tenant-scoped unique index
on `lower(work_email)`, so email keying is deterministic and needed no
contract restriction. `source_person_id` still wins when present because
it is the stable HRIS/SCIM identity; the only genuine ambiguity — the
same `source_person_id` under both `hris` and `scim` — is reported
per-row with a pointer to the optional `person_source` column rather
than guessed.

## D3 — No overwrites; idempotency by equal instant

`Complete()` unconditionally rewrites `completed_at`/`completion_source`.
Reusing it blindly would let a CSV import silently clobber a manual
completion recorded in the UI. The importer therefore refuses rows whose
assignment is already completed at a _different_ instant (per-row error)
and treats an _equal_ instant as `already_complete` (no write). That
yields idempotent re-import of the same file while keeping divergence a
human decision. Phishing rows stay idempotent for free via the existing
`(tenant, assignment, simulation_id)` upsert.

## D4 — One transaction per batch; row failures are data, not errors

"Unmatched rows reported per-row, batch never partially corrupts state"
decomposes into: run the whole import in one transaction; make every
row-level failure a no-rows/pre-validated condition (never a failed SQL
statement, which would poison the transaction); reserve the error return
for infrastructure failures, which roll the whole batch back. `Complete`
and `RecordPhishing` internals were extracted into tx-level helpers
(`completeAssignment`, `upsertPhishing`) so the manual and CSV paths
write identical state.

## D5 — Evidence emission parity

The manual path is Complete → `BuildCompletionEvidence` at the caller.
No ledger wiring exists in this package (no API surface calls it yet),
so parity means: each imported row carries its built evidence record in
the report for the caller to push. Evidence idempotency keys (assignment
id + completion date) make ledger-side dedup safe on re-import.

## D6 — LMS connector shape (decision only; implementation is its own OE)

- **First target: KnowBe4 (KMSAT).** It is the dominant
  security-awareness LMS in the 50–150-person segment security-atlas
  targets, and its Reporting API is a documented, token-authenticated
  REST surface (`/v1/training/enrollments`, `/v1/phishing/security_tests`)
  that returns exactly the fields this import contract needs (user email,
  campaign/module name, completion timestamp, phishing outcomes). A
  KnowBe4 CSV export also maps 1:1 onto the CSV contract, so the
  connector supersedes rather than diverges from the manual bridge.
- **Profile: `pull`** (scheduled poll), per the connector registration
  vocabulary in `Plans/EVIDENCE_SDK.md`. KnowBe4 offers no outbound
  webhooks for training completion, so `subscribe`/`push` profiles have
  nothing to bind to; a poll interval (default hourly, honestly named per
  the anti-pattern list) matches the compliance cadence of training data.
- **Wire shape:** the connector is a separate process holding the
  KnowBe4 API token (constitutional invariant #3 — source-side
  credentials live in the connector). Training completions land through
  the same resolution semantics as this importer with
  `completion_source = 'lms_connector'`; person keying by work email
  against the tenant roster, unresolvable enrollments surfaced in
  connector logs the same way unmatched CSV rows are reported.
- **Not decided here:** whether the platform grows a dedicated
  completions-ingest RPC for connectors or the connector shells out to
  the store API; that contract belongs to the implementation OE. The
  resolution + no-overwrite mechanism is already source-neutral
  (`ingestCompletion` in `csvimport.go` takes the completion source as a
  parameter), so the connector path reuses it with
  `CompletionSourceLMSConnector` rather than re-deriving the semantics.

## Detection-tier classification

- `detection_tier_actual`: none (no defect surfaced during the slice)
- `detection_tier_target`: unit (parser/validation), integration
  (resolution, idempotency, RLS)
