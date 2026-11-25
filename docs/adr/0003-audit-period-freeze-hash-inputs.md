# ADR 0003 — Audit period freeze hash: content-only inputs

**Status:** Accepted · Honored (verified 2026-05-15 by slice 071 audit — slice 028 ships `frozen_hash` over content-only inputs; `frozen_at` lives alongside the hash, not inside it; canonical-JSON serialization with sorted keys is in place)

**Date:** 2026-05-13

**Resolves:** slice 028 AC-7 ("freezing the same content twice produces the same hash") against the design ambiguity in canvas §8.4 ("frozen state is hashed and signed; tampering is detectable").

**Implements through:** [`docs/issues/028-audit-period-freezing.md`](../issues/028-audit-period-freezing.md)

---

## Context

Slice 028 freezes an `AuditPeriod` and persists a tamper-evident hash of the frozen state in `audit_periods.frozen_hash`. Canvas §8.4 commits to "frozen state is hashed and signed; tampering is detectable" but does not specify which fields feed the hash.

Two natural readings of AC-7 ("freezing the same content twice produces the same hash"):

1. **Wall-clock reading.** "Same content" means same evidence universe + same control set. Re-freezing the same row (after directly rewriting `status='open'`, which only a privileged operator or test harness can do) at a different wall-clock instant should still yield identical hash bytes if no underlying ledger record changed.
2. **Event reading.** "Same content" means the same freeze event — same wall-clock moment too — and AC-7 is just asserting determinism for a single freeze call repeated against an unchanged inputs.

These readings have a single concrete consequence: **does `frozen_at` belong in the hash input set or not?**

If `frozen_at` is in the input set, reading #1 fails: two freezes seconds apart of the same content produce different hashes.

If `frozen_at` is out of the input set, reading #1 holds: re-running the hash function on a row whose ledger universe has not changed produces identical bytes, regardless of which `frozen_at` is recorded alongside.

A third reality: a future signing step (cosign on OSCAL export bundles per canvas tech stack §9 — "cosign signing of audit-export bundles") will sign over the tuple `(frozen_at, frozen_hash)` at export time. The signature commits to the wall-clock moment of the freeze; the hash commits to the _content_ of what was frozen. Putting `frozen_at` inside the hash conflates two separable claims.

## Decision

The freeze hash is computed over **content-only inputs**, in this canonical order:

```
sha256(canonical_json({
  "audit_period_id":       <UUID>,
  "period_start":          <ISO-8601 date>,
  "period_end":            <ISO-8601 date>,
  "framework_version_id":  <UUID>,
  "evidence_record_ids":   <sorted UUID array, where observed_at <= frozen_at
                            AND ingested_at <= frozen_at>,
  "control_ids":           <sorted UUID array, in period scope>
}))
```

Canonical JSON: keys in the literal order above (NOT alphabetical), values UTF-8, no whitespace, arrays sorted by string comparison of the UUID forms.

`frozen_at` is **NOT** an input to the hash. It is persisted alongside the hash on the same row and is part of the audit-log entry, so the wall-clock moment is recoverable from any frozen period — it just isn't mixed into the content commitment.

`frozen_by` is **NOT** an input either, by the same argument: who froze it is metadata about the freeze event, not content of what was frozen.

## Consequences

- AC-7 holds under the natural reading: rolling a row back to `status='open'` and re-freezing produces identical hash bytes if and only if the underlying evidence universe and the period's scope have not changed. Tampering with the ledger after freeze is therefore detectable by recomputing the hash and comparing.

- A future cosign signing step over `(frozen_at, frozen_hash, signer_identity)` can commit to both content and event-time without redundancy.

- The hash does NOT prove "this hash was produced at time T." If that claim becomes important (e.g., transparency log entry per future tier-3 evidence integrity), the signing step provides it.

- The canonical-JSON shape is intentionally narrow — six keys, three primitive types (UUID, date, array of UUID) — so a Python or TypeScript reimplementation in a future verifier tool has zero ambiguity.

- We do NOT include `tenant_id` in the hash because the period id is already tenant-unique (`(tenant_id, id)` composite uniqueness) and the verifier always operates inside one tenant's RLS context.

## Amendment: the ingested_at bound (#1699)

The evidence set was first defined as `observed_at <= frozen_at` only. That admits a record written to the ledger after the freeze whose `observed_at` is backdated before it (a late connector push, a backdated upload, a clock-skewed source). Such a record was not in the ledger when the period froze, yet a recompute of the hash at `frozen_at` included it, so the recompute no longer matched the stored `frozen_hash`. The set is now bounded on both columns: `observed_at <= frozen_at AND ingested_at <= frozen_at` (`ListEvidenceIDsForPeriodHash`). Every other freeze-horizon read (period control state, population count and draw, the frozen evidence summary) applies the same pair.

Upgrade: no stored hash changes, and none needs to.

- `Freeze` (`internal/audit/period/period.go`) is the only caller of `ListEvidenceIDsForPeriodHash`. The product never recomputes or verifies a stored `frozen_hash`, and never rewrites one, so no existing frozen period can fail an in-product check.
- For an external verifier, a recompute at `frozen_at` under the new definition reproduces the set the hash was taken over, with two exceptions. Both affect only periods frozen before the fixes named below.
  - **Clock skew.** `ingested_at` is the database clock and `frozen_at` was then the app clock. A row visible to the freeze whose `ingested_at` is later than `frozen_at`, because the DB clock ran ahead of the app clock, was hashed, but a recompute leaves it out. Closed by stamping `frozen_at` from the database clock (#1742, and #3038 below).
  - **Transaction start.** `ingested_at` is the ingest transaction's start (`now()`), not its commit. A row whose transaction began before `frozen_at` but committed after the freeze read the ledger has `ingested_at < frozen_at` and was not hashed, but a recompute includes it. Closed by the tenant evidence-freeze lock: for historical import by #1742, for live ingest by #3038 below.
- Under the old definition, any late, backdated row that arrived after the freeze made the stored hash unreproducible. The amendment makes verification reproducible where it was not, so it is a correction, not a break.

A reimplemented verifier MUST apply both bounds.

## Amendment: freeze revisions (#1742)

A historical evidence import may add records to a period that is already frozen, but only when the import names the period explicitly. The period is then re-frozen as a new **freeze revision**; `frozen_at` and `frozen_hash` on `audit_periods` are never rewritten and remain revision 1.

- Revision N (N >= 2) is an append-only row in `audit_period_freeze_revisions` that records its hash, the previous revision's hash, the cut time, the actor, the import batch, and the evidence record ids it adds.
- The evidence set at revision N is the revision-1 set (both bounds above, at the original `frozen_at`) plus the ids listed by revisions 2..N. The six-key canonical-JSON shape, the control set, and the sort are unchanged. Only the evidence id array gains those ids.
- Revision 1's hash is unaffected by any later revision. A verifier recomputes revision N by applying the bounds at the original `frozen_at` and then adding the listed ids for revisions 2..N.
- `frozen_at` (the horizon) does not move, so a record ingested after the original freeze joins a later revision only by being listed in it.
- Only a record observed at or before the original `frozen_at` may be listed (invariant #10). The importer enforces it, and every frozen read admits a listed id only with `observed_at <= frozen_at`.
- Each revision carries a required free-text reason from the admin who cut it, recorded on the revision row and in the period's audit log (Matt, 2026-11-24).
- A freeze and a historical import in the same tenant serialise on one transaction-level advisory lock (`advisorylock.TenantEvidenceFreeze`): Freeze takes it before it reads the period or hashes, the import as its first statement. The HTTP freeze stamps `frozen_at` from the database clock (`clock_timestamp()`) once it holds the lock, so `frozen_at` and `ingested_at` share one clock and every row committed before the lock was granted is inside the horizon. The import then refuses (and retries) if any tenant period has `frozen_at` at or after its own transaction start, because its rows carry `ingested_at` = that start and would otherwise join a freeze that never hashed them.

The rule, as decided 2026-11-24: a frozen population changes only through a recorded, hashed, reasoned revision; the original freeze always replays; and only records with `observed_at <= ` the original `frozen_at` may join a revision.

A reimplemented verifier MUST take the revision number as an input and add the listed ids for revisions up to it.

## Amendment: one clock, and live ingest under the freeze lock (#3038)

The freeze horizon and `ingested_at` now come from one clock, the database's, and every ledger writer, not only the historical import, is ordered against a freeze.

- **Freeze.** The HTTP freeze stamps `frozen_at` from `clock_timestamp()` once it holds `advisorylock.TenantEvidenceFreeze` in exclusive mode (as #1742 made it). An explicit `at` is still used as given; only tests and replays of a past instant pass one.
- **Live ingest.** Every ledger insert (`ingest.Service.persist`: push, the stream-buffer consumer, and the domain writers through `ProcessInTx`; the demo seed alone writes fixture rows directly, with backdated `ingested_at`, into a tenant it builds itself) takes the same lock in shared mode just before its INSERT and holds it to commit. Ingests do not block each other. A freeze waits for every ingest holding the lock to commit, so it hashes with their rows in view. After the lock is granted, the ingest refuses if a tenant period has `frozen_at` at or after its transaction start (`ErrConcurrentFreeze`), because its row would carry an `ingested_at` inside a horizon that never hashed it. `Process` retries in a new transaction, whose `ingested_at` is after that freeze, up to three attempts. `ProcessInTx` returns the error, and the domain writer's whole transaction is the unit to retry: its handler answers `409` with code `concurrent_freeze`, and `evidence_audit_log` records `rejected_concurrent_freeze` (#3060).
- **Isolation level.** The check after the lock is sound only under READ COMMITTED, where that statement takes a fresh snapshot after the lock is granted. Under REPEATABLE READ or SERIALIZABLE the snapshot is the transaction's first statement's, which can predate a freeze that committed before the lock, so the check would read no freeze. The check reads `transaction_isolation` in the same statement and fails the write on any other level (`ingest.ErrIsolationLevel`). The refusal is audited as `rejected_internal_error` and answers 500, because it is a caller or configuration bug, not a client retry. Self-host operators: a server, database or role `default_transaction_isolation` above READ COMMITTED makes every ledger write fail this way (#3060).
- **Ingests stall while a freeze waits.** Postgres grants advisory locks in queue order: once a freeze is waiting for the exclusive lock, a new ingest's shared request queues behind it, even though it is compatible with the shared holders. So from the moment a freeze starts waiting until it commits, every new ledger write in the tenant blocks. That covers the wait for open ingests to commit and the freeze's own hash and UPDATE. The stall is bounded by the longest open ingest transaction plus the freeze itself. A live ingest's hold is short, since the shared lock is taken just before the INSERT. A historical import is the long holder: it takes the lock in exclusive mode as its first statement and keeps it for its whole batch, up to 10,000 records. Live ingests in the tenant block for that long, and a freeze queued behind the import extends the stall by its own run. Freeze sets no `lock_timeout` (#3060 decisions log).
- **Visibility rule.** With both, a row is in a freeze's hash if and only if `observed_at <= frozen_at AND ingested_at <= frozen_at`. A row committed before the freeze took the lock has `ingested_at` at or before the commit, so it is before `frozen_at`, and the freeze sees it. A row whose transaction was open when the freeze took the lock either held the shared lock, so the freeze waited for it, or refuses because `frozen_at` is after its start. A row whose transaction began after `frozen_at` is outside the horizon.
- **Live evaluation.** `eval.Engine` reads `evaluated_at` from `clock_timestamp()` inside the evaluation's transaction, before its ledger read, and applies `ingested_at <= evaluated_at` to that read, as a replay does. A row ingested and committed before T is counted by the live result at T and by a replay of T. The engine does not take the freeze lock, so the transaction-start window stays open for live evaluation alone. A row whose transaction began before T but committed after the read is missed live at T and counted by a replay of T. A later evaluation counts it. The backstop is the `eval.Scheduler` sweep, which re-evaluates every active control hourly by default (`ATLAS_EVAL_RECOMPUTE_INTERVAL`). A row pushed through the JetStream `EVIDENCE_INGEST` stream is re-evaluated sooner by `IngestSubscriber`. Domain-writer, MCP-applied and direct-publisher rows publish nothing, so for them the sweep is the only re-evaluation (#3060).

Upgrade: no stored hash changes. A period frozen before this change keeps the hash it has; the two exceptions above describe when an external recompute of such a period can differ. A period frozen after this change has neither.

## Amendment: the control set is recorded at freeze (#3066)

`control_ids` was read from the tenant's live catalog on every hash, at freeze and at replay (`ListControlIDsForPeriodHash`), with no horizon. Creating or deleting any control after a freeze changed the replay of revision 1 and of every later revision, so the rule above, that the original freeze always replays, did not hold.

- **Recorded, not bounded.** A `created_at <= frozen_at` horizon cannot express deletion: controls are hard-deleted and carry no `deleted_at`. So Freeze records the exact list it hashed in `audit_period_frozen_controls` (one row per period: `control_ids`, `source`, `recorded_at`), in the freeze's own transaction. `ReplayHash`, and every revision hash including the one `AmendInTx` cuts, read that row and never the live catalog. A frozen period with no row fails closed with `period.ErrNoControlSnapshot`; it is never replayed against the current catalog instead.
- **Hash definition unchanged.** The six-key canonical JSON and its sort are byte-identical. Only where `control_ids` comes from changes, so no stored `frozen_hash` or revision hash changes. The control set is the same for every revision of a period: a revision adds evidence ids only.
- **Write-once.** `atlas_app` holds SELECT and INSERT only, and RLS has no UPDATE or DELETE policy. The trigger `audit_period_frozen_controls_immutable_trg` refuses UPDATE for every role, and DELETE while the period exists. Deleting the period cascades to its row. The owner-only reopen (`docs/runbooks/audit-period-reopen.md`) removes the row with the trigger disabled, so the next freeze records its own.
- **Writers that bypass Freeze.** The demo seed and the SQL fixtures (`fixtures/e2e/audit-workspace.sql`, `fixtures/e2e/audits-list.sql`, `fixtures/walkthroughs/oscal-export.sql`) insert frozen periods directly. Each writes the row too, with `source = 'seed'`. Their `frozen_hash` values are placeholders, so these periods were never replayable; the row only keeps a historical import that amends one from failing closed.

Upgrade: migration `20261311000000_audit_period_frozen_controls` backfills one row per period already frozen, from the tenant's catalog as it stands at migration time, with `source = 'migration_backfill'`. For such a period verification is exactly as strong as before the upgrade:

- if the catalog has not changed since the freeze, replay matches, and from then on no catalog change can break it;
- if it changed before the upgrade, replay already mismatched and still does. The backfill does not hide the drift, and `source` says the recorded set is not freeze-time.

A reimplemented verifier MUST take `control_ids` from the recorded row, and SHOULD report a `migration_backfill` row's match as "catalog unchanged between freeze and upgrade", not as a freeze-time commitment.

## Alternatives considered

1. **Include `frozen_at`.** Rejected: breaks AC-7 under the wall-clock reading, which is the practitioner-natural reading ("audit said the dashboard hasn't changed since freeze — prove it"). Wall-clock differences between two equivalent freezes are bookkeeping, not content.

2. **Include the full payload (or payload hash) of each evidence record.** Rejected: the evidence*records table already stores a per-record `hash` (slice 002 + slice 003). The freeze hash binds the \_set* of records (by id); the records' hashes bind the _content_ of each record. Composing the two at verification time gives full chain detection without making the freeze hash quadratic in evidence volume.

3. **Use a Merkle tree over evidence record hashes.** Rejected for v1: the linear sha256-over-sorted-ids is sufficient for the immediate threat (set-membership tampering), and Merkle adds verifier complexity. Tier-3 transparency-log work can graduate to Merkle when warranted.

4. **Hash the literal SQL result rows.** Rejected: row order, NULL serialization, and timestamp precision all leak engine details into the hash. Canonical-JSON over an explicit input contract is portable.
