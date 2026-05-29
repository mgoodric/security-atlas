# Slice 379 — Eliminate double `protojson.Marshal` on ingest redaction path (decisions log)

Closes slice 332 audit finding **F-ING-1 (MEDIUM)**.

Source finding: `docs/audits/332-performance-audit-report.md` lines 158–182.
Slice spec: `docs/issues/379-ingest-redaction-single-marshal.md`.

This log captures the build-time judgment calls. The product runtime
AI-assist boundary (CLAUDE.md) is untouched; this is purely an ingest
hot-path refactor.

---

## D1 — Marshal-count interpretation: "marshals whose output is retained" == 1

**Decision.** AC-1 is interpreted as "exactly one marshal whose output
BYTES are RETAINED for the ledger payload column and feed the canonjson
hash" — not "exactly one `protojson.Marshal` call anywhere in
`Service.Process` on the redaction-bearing path".

**Why this matters.** A naive reading of AC-1 ("marshals payload EXACTLY
ONCE") could be read as "do exactly one `protojson.Marshal` call". But
slice doc note 2 explicitly says: _"Keep the marshal-for-size-check but
discard those bytes after, and re-marshal AFTER redact for the hash +
DB write. That's still one marshal-for-bytes-we-keep."_ That makes
explicit that two `protojson.Marshal` calls survive on the redaction-
bearing path (pre-redact for size + validate, post-redact for ledger),
but exactly one of them produces bytes that are KEPT.

**Mechanism.** The bytes-we-keep marshal is routed through a Service
field `marshalLedger func(proto.Message) ([]byte, error)`. Production
constructs `New()` with `marshalLedger: protojson.Marshal`. Tests in
`marshal_count_test.go` swap in a counting wrapper and assert
`count == 1`. The pre-redact `protojson.Marshal(rec.GetPayload())` for
size-check and schema-validation is a DIRECT call (not through the
seam), because its bytes are not retained.

**Alternative considered.** Reducing the redaction-bearing path to
exactly one `protojson.Marshal` call requires either:

1. Computing the JSON byte length of `rec.GetPayload()` without
   marshaling — pragmatically impossible because the JSONB column
   length is the ground truth and `protojson.Marshal` is the only
   library-supported way to produce JSON-form bytes from a `*structpb.Struct`.
2. Reordering to redact-then-size-check-then-validate — violates
   slice 015 D2 invariant (P0-4).
3. Mutating the redactor to operate in-place on the proto so we could
   skip the pre-redact marshal — violates P0-1 ("Does NOT change the
   redactor's proto-in/proto-out API").

All three options violate stated anti-criteria. The slice-doc-note-2
interpretation is the only one consistent with the full P0 set.

**What this slice actually buys.** The CPU cost on the redaction-bearing
path is unchanged (still two `protojson.Marshal` calls). The deliverable
is the explicit marshal-of-record SEAM that:

- Makes the "exactly one ledger marshal" invariant testable (AC-1).
- Makes future structural simplifications (e.g., a future slice that
  changes the redactor API to in-place mutation — outside slice 379's
  P0 fence) STRUCTURALLY trivial: collapse the seam call site, the
  AC-1 assertion remains valid.
- Documents in code (see comment block at `Service.marshalLedger`
  declaration) the semantic distinction between pre-redact (throwaway)
  and post-redact (retained) marshals — eliminating the ambiguity
  that produced slice 332 F-ING-1 in the first place.

The audit's framing — "two full proto-serialization passes" — is
correct as a description of CPU work. The slice's framing — "one
marshal-for-bytes-we-keep" — is the right level of abstraction for
the constitutional invariant the refactor preserves.

## D2 — AC-3 contradiction resolution: slice-doc typo, preserve P0-4

**Decision.** AC-3 ("schema-validation hook receives the redacted
payload bytes, NOT the unredacted bytes") DIRECTLY contradicts P0-4
("Does NOT change the order: size-check → schema-validate → redact-if-
rules → hash → write. Slice 015's D2 invariant"). The slice 015 D2
invariant is the canonical order. AC-3 is a slice-doc typo — the
author likely meant "the hash is computed on the redacted payload
bytes" or "the ledger receives the redacted bytes" (both of which
ARE true under the canonical order). The implementation preserves
P0-4 and the slice 015 D2 invariant unchanged.

**Practical effect.** Schema-validation runs on the UNREDACTED bytes
(the pre-redact `payloadJSON` from line ~323 of post-refactor `ingest.go`).
This is the current behavior and is correct under slice 015 D2.

**What we tested.** The acceptance test exercises a redaction-bearing
push end-to-end (within the seam-short-circuit boundary) and verifies:

- The pre-redact bytes reach the schema-validator (covered transitively
  by the existing integration tests in `integration_test.go`, which
  have not changed).
- The post-redact bytes contain the `<<REDACTED>>` marker
  (TestProcess_RedactionApplied_BytesAreRedacted).
- The original unredacted scalar (`shh-this-is-a-secret`) does NOT
  appear in the post-redact bytes (P0-2 anti-criterion guard, same
  test).

**Flag for slice spec correction.** Recommend updating
`docs/issues/379-ingest-redaction-single-marshal.md` AC-3 to read:
_"AC-3. The bytes written to the ledger payload column are the redacted
form (the redact-then-marshal-then-write ordering must hold)."_ That
matches slice 015 D2 and aligns with AC-5. Maintainer JUDGMENT:
post-merge slice-doc patch, not a blocker for this slice.

## D3 — Test seam placement: same-package vs sibling subpackage

**Decision.** The marshal-count tests live in
`internal/evidence/ingest/marshal_count_test.go` (same package, NOT
`package ingest_test`). This lets the tests poke the unexported
`Service.marshalLedger` field + the unexported `withLedgerMarshaler`
constructor directly.

**Why not an exported `WithLedgerMarshaler`.** The seam is a
test-only construct — exposing it as a public method would invite
production callers to mistakenly override it. Lower-case keeps it
inside the package boundary while remaining accessible to tests in
the same package.

**Why not `package ingest_test` with an exported method.** Would
require an exported method on `Service` purely for testability, which
is anti-pattern (production API shaped by test convenience). Same-
package tests + unexported seam is the idiomatic Go pattern for this
shape.

## D4 — Test short-circuit via sentinel error, not a real pool

**Decision.** The marshal-count tests short-circuit `Process` at the
seam by returning `sentinelStopAfterMarshal` from the test seam. This
avoids the need for a real `pgxpool.Pool` — the tests stay pure-Go,
no Postgres needed, no integration tag.

**Consequence.** `Service.Process` would call `s.writeAudit(...)` on
the `DecisionRejectedInternalError` path when the seam errors. With a
nil pool, `writeAudit` would panic inside `pgx.BeginTxFunc`. Two paths:

1. **Add a nil-pool guard inside writeAudit** (the choice). Tiny
   defensive check at the top: `if s.pool == nil { return }`. Aligns
   with `writeAudit`'s already-best-effort design (it can fail
   silently; that's documented behavior). Production constructors
   (`New()`) panic on nil pool, so this branch is unreachable in
   production.
2. **Make tests use a real pool fixture** — pulls in the integration
   tag, the docker-compose dependency chain, and makes a pure-Go
   compile gate into a 30+s integration gate. Not worth it for a
   marshal-count assertion.

Going with (1). The nil-pool guard is a one-line addition and makes
the package more robust to future test-only constructions of `Service`
without changing production behavior.

## D5 — Benchmark shape: marshal count assertion, no wall-clock claim

**Decision.** `BenchmarkIngestRedactionPath` asserts that
`ledgerCalls.Load() == int64(b.N)` at the end of the loop. It does
NOT publish a wall-clock baseline.

**Why.** Slice doc note 3 is explicit: _"the benchmark should assert
marshal count, not wall-clock time — wall-clock will be noisy. Use a
counting `protojson.MarshalOptions` or wrap the marshal call via an
unexported test seam."_ The marshal-count claim is the structural
invariant; wall-clock would be a comparison-to-baseline (slice 015
behavior) which the audit explicitly says is operator-invisible at v1
RPS profiles.

**Side benefit.** The benchmark serves as a CI canary: any future PR
that accidentally re-introduces a double-marshal-of-record on the
redaction path will trip `b.Fatalf("AC-8: expected ledger-marshal
count == b.N=%d, got %d", want, got)`. The benchmark is short — 1000
iterations at ~4.8 µs/op = sub-5 ms total — so it's cheap to run in CI
if a future maintainer adds it to the benchmark suite.

## D6 — `RedactionRulesFor` returns nil rules for the no-rules-path test

**Decision.** `TestProcess_NoRedactionPath_LedgerMarshalCountIsZero`
uses a `countingValidator` constructed with `rules: nil`. This means
the `RedactionLookup` interface assertion succeeds (the validator
implements it), but the inner `if len(rules) > 0` branch in `Process`
is FALSE, so the seam never fires.

**Why.** This is the guard that catches a regression where a future
refactor accidentally fires the seam on the no-rules path (would
DOUBLE-marshal the no-rules path, regressing the formerly-1-marshal
common case to 2 marshals). The test recovers from the nil-pool panic
that fires when Process eventually reaches the DB write, and observes
that the counter is exactly 0 — confirming the seam was bypassed.

**Alternative considered.** Make `countingValidator` NOT implement
`RedactionLookup` at all (the slice-013 InMemory fallback path). Same
effect — seam never fires — but loses the per-branch granularity. The
chosen shape exercises the EXACT code branch (`len(rules) == 0` inside
the `RedactionLookup` block) we care about.

## D7 — Defensive copy in TestProcess_RedactionApplied_BytesAreRedacted

**Decision.** The captured-bytes test copies the seam's bytes via
`captured = append(captured[:0], b...)` instead of `captured = b`.

**Why.** `protojson.Marshal` may return a slice backed by a pool-managed
buffer in future versions; capturing by reference could see the bytes
change between the seam call and the assertion. The append-copy is
~free at sub-200-byte payloads and makes the assertion order-of-events
independent.

## D8 — No SchemaValidator interface widening

**Decision.** The `SchemaValidator` interface in `ingest.go` is
unchanged. The redaction-rules lookup remains the optional
`RedactionLookup` interface assertion — same shape as slice 015.

**Why.** P0-1 forbids changing the redactor's proto-in/proto-out API.
The validator interfaces are adjacent but not the redactor itself; even
so, widening any of them would ripple into all production validators
(slice 013 InMemory + slice 014 DB-backed Service + slice 015 tenant-
aware probe). The seam mechanism keeps the contract surface narrow:
ONE new private field on Service, ONE new private method
(`withLedgerMarshaler`), ZERO interface changes.

## D9 — Explicit non-touches

The following were considered and explicitly NOT touched:

- **`internal/evidence/redact/redact.go`** — P0-1. Stays clone-don't-
  mutate. A future slice can revisit if a structural CPU win is
  needed; slice 379 is scoped to the seam refactor.
- **`internal/canonjson/canonjson.go`** — out of scope. Its
  `proto.Marshal` is binary (not protojson) and serves the hash, not
  the ledger bytes column. Slice 332 F-ING-3 (Informational) covers
  canonjson and explicitly recommends no action.
- **`internal/api/schemaregistry/service.go`** — its `RedactionRulesFor`
  is the same return shape; no changes needed.
- **`internal/api/credstore`** — no changes; tests construct
  `credstore.Credential` values directly.
- **`writeAudit` semantics beyond the nil-pool guard** — F-ING-2 is a
  separate finding bundled into slice 381. Slice 379 does not touch
  the audit-write transaction model.

## D10 — Status row management

This PR does NOT modify `docs/issues/_STATUS.md`. Per slice 382, the
status-row flip from `in-progress` → `in-review` is the orchestrator's
job on the reconcile branch, not the engineer's job on the impl branch.
The slice 379 row will be flipped to `in-review` by the orchestrator
after this PR merges, and to `merged` thereafter.
