# 633 — Fix slice 474 ingest/verify canonical-scope hash round-trip divergence (P0)

**Cluster:** evidence-pipeline (integrity)
**Estimate:** M (1-2d — integrity code, needs care + a real round-trip proof)
**Type:** JUDGMENT (which side is canonical — the ingest wire-scope hash or the
rehydrated canonical-scope hash — is a design call with invariant-#2 stakes)
**Status:** `ready`
**Parent:** #474 (`ea3541fb`, PR #1142). Surfaced 2026-06-08 during batch 225
(orchestrator merge-queue) when slices 615 + 620 became the first Go PRs to run
`Go · integration (shard A)` to completion since 474 merged.

## Severity / why P0

`TestEvidenceVerify_ProductionRecordValidates_474` fails **deterministically** on
`main`: a record pushed through the real ingest `Process` does **not** verify
against its own stored ingest hash —

```
verify_integration_test.go:63: production record did NOT verify against its
ingest hash: stored=6f84c9ff0f66548a660af14c5889b931bc1d81e4c600e8d713797dd528414caa
            recomputed=dfd1745b12e5d6003f23eb78d832b1526f79c41a0ec5a225e73ba623db989b25
```

Same `stored`/`recomputed` pair on every run → not a flake. This is the core
**tamper-evidence guarantee (constitutional invariant #2)** failing: the ledger
verify walk cannot validate a production record it just ingested. It blocks the
required `Go · integration (shard A)` + roll-up `Go · integration (Postgres RLS)`
checks for **every** Go PR, so the entire merge queue is wedged until it lands.

## Root cause (diagnosed)

Slice 474 (`ea3541fb`) tried to make the verify walk reproduce the ingest
scope-inclusive hash by persisting the canonical wire scope at ingest:

- **Ingest** (`internal/evidence/ingest/ingest.go`): hashes the FULL evidence
  proto (including the wire `scope`), then persists
  `scope_canonical = canonjson.MarshalCanonicalScope(rec.GetScope())` into a new
  additive JSONB column (migration `20260608040000_evidence_scope_canonical.sql`).
- **Verify** (`internal/evidence/ingest/verify.go` → `RecordFromLedgerRow` /
  `VerifyLedgerRow`): rehydrates `rec.Scope = canonjson.UnmarshalCanonicalScope(row.ScopeCanonical)`
  and re-hashes the reconstructed proto.

The round trip is **not hash-preserving**: the proto re-serialized from the
rehydrated canonical scope does not produce the byte sequence the ingest hash was
computed over. Candidate causes to confirm during the fix:

1. `MarshalCanonicalScope` reorders / normalizes (sorts keys, dedupes values,
   drops empties) so `Unmarshal(Marshal(scope))` ≠ original `scope` proto, and
   the hash covers the **original** wire order.
2. Proto serialization of the rehydrated `Scope` message differs (field
   presence, repeated-element order, default elision) from the original.
3. The hash envelope includes a field that the canonical round trip cannot
   reconstruct (e.g. a wire-only field not carried in `scope_canonical`).

## How it stayed hidden (process gap — call out in the fix's decisions log)

- 474's own PR #1142 was **merged with `Go · integration (shard A)` and
  `(Postgres RLS)` RED** (10m21s real FAIL, not cancelled) — the load-bearing
  test for the slice was failing at merge time.
- Every main commit since either **skipped** the shard legs (path-filter on
  docs/status-only commits) or had its main run **cancelled** by the next merge
  (concurrency group). So no completed shard-A run on `main` ever re-caught it.
- This is the `CI path-filter as gap-multiplier` pattern: the first PR to run the
  filtered path to completion surfaces the accumulated breakage.

## Acceptance criteria

- **AC-1** `TestEvidenceVerify_ProductionRecordValidates_474` passes: a record
  through real `Process` verifies clean against its stored ingest hash, with NO
  baseline-stamp workaround.
- **AC-2** Tamper-evidence preserved: corrupting `payload` OR `scope_canonical`
  out-of-band is still detected (the existing AC-2 assertions stay green).
- **AC-3** The fix makes ingest and verify agree by construction — pick ONE
  canonical hash basis and document it: either (a) ingest hashes over the
  canonical scope form (so the stored hash == the reconstructable hash), or
  (b) `scope_canonical` persists enough to reproduce the EXACT wire bytes the
  hash covered. Whichever is chosen, a unit test pins
  `Unmarshal(Marshal(scope))` → re-hash == ingest hash for a table of scopes
  (empty, single-dim, multi-dim, unsorted-input, duplicate-values).
- **AC-4** Legacy pre-474 rows (`scope_canonical` NULL) keep the slice-464
  scope-free reconstruction (no regression for old ledger rows).
- **AC-5** The receipt-hash contract clients reproduce (EVIDENCE_SDK) is either
  unchanged, or the change is documented + the SDK/CLI updated in the same slice.
  Do NOT silently change the wire receipt hash clients depend on.
- **AC-6 (process)** Add a CI guard so a slice cannot merge with a required
  integration shard red AND document the path-filter/concurrency masking in the
  decisions log. (If the guard is non-trivial, spin it to its own spillover.)

## Constitutional invariants honored

- **#2** (ingestion/evaluation separation + append-only ledger + point-in-time
  replay): the verify walk must validate the record as reconstructable from the
  ledger — this slice restores that.

## Canvas references

- `Plans/canvas/04-evidence-engine.md` §4.3 (append-only ledger), the
  EVIDENCE_SDK receipt-hash contract (`Plans/EVIDENCE_SDK.md`).

## Dependencies

- #474 (merged, `ea3541fb`) — this fixes its regression.

## Anti-criteria (P0 — block merge)

- Does NOT "fix" the test by stamping a baseline hash or skipping the production
  case — that would re-hide the real divergence (the slice-464 workaround 474
  explicitly removed).
- Does NOT weaken AC-2 tamper detection to make AC-1 pass.
- Does NOT change the client-facing receipt hash without updating the SDK/CLI
  contract + docs in the same slice.
- Does NOT quarantine the integrity test as the permanent resolution (a temporary
  quarantine to unblock the queue is acceptable ONLY if paired with this slice as
  the immediate follow-up and a tracking note in `_STATUS.md`).

## Skill mix (3-5)

- Go (proto serialization + sha256 hashing), Postgres/sqlc (the `scope_canonical`
  column + queries), integration-test authoring (real Postgres), canonical-JSON
  codec reasoning (`internal/canonjson`), CI workflow (the AC-6 guard).

## Notes for the implementing agent

- Reproduce locally first: `go test -tags=integration -run TestEvidenceVerify_ProductionRecordValidates_474 ./internal/evidence/ingest/...`
  against a real Postgres (docker-compose bring-up).
- The decisive diff is `ea3541fb` — read `internal/canonjson/canonjson.go`
  (`MarshalCanonicalScope`/`UnmarshalCanonicalScope`), `ingest.go` (the hash +
  persist), and `verify.go` (`RecordFromLedgerRow`). Print the original wire
  scope bytes, the canonical bytes, and the rehydrated-then-reserialized bytes
  side by side — the divergence will be visible.
- `docs/audit-log/474-ingest-hash-align-decisions.md` records 474's reasoning
  (canonical-hash choice) — read it; the fix either completes that approach or
  changes the hash basis with a documented trade-off.
