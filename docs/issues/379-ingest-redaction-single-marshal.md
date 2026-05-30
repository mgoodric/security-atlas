# 379 — Eliminate double `protojson.Marshal` on ingest redaction path (close slice 332 F-ING-1)

**Cluster:** Performance
**Estimate:** 0.5d
**Type:** AFK (mechanically verifiable)
**Status:** `ready`

## Narrative

Closes slice 332 finding **F-ING-1 (MEDIUM)**.
`internal/evidence/ingest/ingest.go Service.Process` marshals the
evidence payload twice on the redaction code path:

1. **Line 269** — first marshal for size-check and schema-validation
2. **Line 329** — second marshal after the redactor mutates the
   payload, so the hashed-and-stored form is the redacted bytes

The redactor operates on the unmarshaled proto
(`*evidencev1.EvidenceRecord`), not on bytes, so the second marshal
is structurally avoidable: redact in-place on the same proto in
memory, marshal once at hash-time, hash the result.

At v1 push rates (1–10 RPS per credential) the double-marshal is
operator-invisible; it becomes measurable above sustained 100 RPS on
redaction-bearing kinds. The redaction-kind list will grow with
PCI-CDE-bound kinds, so the fix is preemptive.

### Why now

Slice 332 surfaced this as the only operator-visible Medium on the
ingest pipeline. Bounded blast radius + single-file fix + clear
remediation pattern.

### Trigger

Slice 332 performance audit, surface 1 (evidence ingest), finding
F-ING-1.

### Disposition

Code change to `internal/evidence/ingest/ingest.go` only.
`internal/evidence/redact/redact.go` is unchanged; the redactor's
proto-in/proto-out API stays the same.

## Threat model

Refactor-only. STRIDE:

- **S/T/R/I/E:** No change to threat surface.
- **D:** Marginal reduction in per-push CPU; no DoS impact.

**Constitutional invariants honored**:

- **Ingestion/evaluation separated (invariant #2).** Refactor stays
  inside `internal/evidence/ingest`.
- **Audit-log decision discipline.** The `writeAudit` call sites are
  unchanged; the refactor only re-orders the marshal-redact-marshal
  to marshal-once-after-redact.

## Acceptance criteria

- [ ] **AC-1.** `Service.Process` marshals the payload exactly ONCE
      on the redaction-bearing code path. Asserted by a unit test
      that swaps in a marshal-counter `MarshalOptions` and asserts
      count == 1.
- [ ] **AC-2.** The size-check (1 MiB cap, slice 015 P0-15-6) STILL
      runs BEFORE the schema-validation step — operator order of
      operations preserved.
- [ ] **AC-3.** The schema-validation hook receives the redacted
      payload bytes, NOT the unredacted bytes (the redact-then-
      validate ordering must hold — slice 015 D2 invariant).
- [ ] **AC-4.** The hash is computed on the redacted form (no
      change from current behavior).
- [ ] **AC-5.** The `evidence_records.payload` JSONB column is
      written with the redacted form (no change from current
      behavior).
- [ ] **AC-6.** Idempotency dedup compares hashes-of-redacted-forms
      (no change from current behavior).
- [ ] **AC-7.** Existing `internal/evidence/ingest` integration
      tests pass unchanged.
- [ ] **AC-8.** New benchmark `BenchmarkIngestRedactionPath`
      asserts the per-push marshal count is exactly 1.
- [ ] **AC-9.** `pre-commit run --files` passes.

## Anti-criteria (P0)

- **P0-1.** Does NOT change the redactor's proto-in/proto-out API.
- **P0-2.** Does NOT log the unredacted payload bytes at any error
  path (anti-criterion preserved from slice 015 P0).
- **P0-3.** Does NOT skip the size-check on redacted payloads — the
  cap is enforced on the bytes that go into the ledger, which is
  the redacted form (smaller-or-equal to unredacted by definition,
  so a passing pre-redact size check guarantees a passing post-
  redact size check).
- **P0-4.** Does NOT change the order: size-check → schema-validate
  → redact-if-rules → hash → write. Slice 015's D2 invariant.
- **P0-5.** Does NOT auto-merge.

## Dependencies

- **#332** (performance audit) — `merged`. Source finding.
- **#013** (evidence push API) — `merged`. Owner of `Service.Process`.
- **#015** (JetStream + redaction) — `merged`. Owner of the
  redaction code path.

## Notes for the implementing agent

1. The cleanest shape: marshal once for size-check, then if
   redaction rules apply, redact the in-memory proto AND
   compute the hash from the same redaction-resulting bytes.
   `protojson.Marshal` once after redact, NOT twice.
2. The slice 015 marshal-then-redact ordering exists because the
   size-check needed the bytes to count length. An alternative:
   bound size by counting the unmarshaled proto's payload — but
   that's a wider refactor. Keep the marshal-for-size-check but
   discard those bytes after, and re-marshal AFTER redact for the
   hash + DB write. That's still one marshal-for-bytes-we-keep.
3. The benchmark should assert marshal count, not wall-clock time
   — wall-clock will be noisy. Use a counting `protojson.MarshalOptions`
   or wrap the marshal call via an unexported test seam.
