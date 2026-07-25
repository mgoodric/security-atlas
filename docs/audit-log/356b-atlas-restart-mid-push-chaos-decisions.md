# Slice 356b — Chaos Experiment 5 execution: atlas container restart mid-evidence-push · decisions log

**Type:** JUDGMENT · **Approach:** execute the slice-335 design as written (no redesign) · **Date:** 2026-07-24

- detection_tier_actual: `manual_review` (chaos run)
- detection_tier_target: `integration`

> The headline gap (G-1 below) is not reachable from any existing tier: nothing
> in the test suite restarts atlas and then keeps pushing on a credential
> issued before the restart. `integration` is the right target — the Go
> integration tier already runs real services and could bounce the process —
> which is itself the argument for the follow-up filed at the end.

**Design contract:** [`docs/audits/335-chaos-experiment-design.md`](../audits/335-chaos-experiment-design.md) §Experiment 5.
**Slice narrative:** [`docs/issues/356-data-tier-outage-chaos-round-1.md`](../issues/356-data-tier-outage-chaos-round-1.md).
**Scope of THIS log:** Experiment 5 only. Slice 356 bundles Experiment 3 with
Experiment 5; Experiment 3 was executed separately by slice 356a
(`docs/audit-log/356a-pg-primary-outage-chaos-decisions.md`). This log is filed
at `356b-` for the same reason 356a was filed at `356a-`: so the bundled path
named by slice 356 AC-10 is not created half-populated.

---

## Headline

**The hypothesis is FALSIFIED, and it fails earlier than the design anticipated.**

The design predicted a transient gap: the container goes away for 5-10 seconds,
the SDK sees `Unavailable`, backs off, retries, and the idempotency key stops
the retry from double-writing. What actually happens is that the container comes
back in **1 second** and then rejects every subsequent push with
**`Unauthenticated`, permanently** — because the credential store is in-memory
and the restart wipes it.

The design's expected failure is transient and self-healing. The measured
failure is immediate, total, and self-healing only with operator intervention.

- **49 of 60 records were lost** in the as-shipped arm. Not delayed — lost.
- **The design's own retry schedule was exercised and saved nothing.** Two
  retries fired into the restart window; both returned `Unauthenticated`.
  Backoff cannot recover a failure that is not transient.
- **A push credential issued before the restart never works again.** Recovery
  for an unattended push client is unbounded, not the ~10s the design implies.

The ledger itself came through clean: zero duplicates, zero hash collisions,
one row per key, in every arm. What the platform loses under this failure it
loses at the front door, never in the ledger.

Per the design's own framing, a falsified hypothesis is a successful
experiment. It is recorded plainly here and carried into follow-ups.

---

## Pre-execution checklist — sign-off (AC-6)

The design's §Experiment 5 checklist has two items. Both were executed by
`preflight()` in `scripts/chaos/run-exp5-atlas-restart.sh` against the running
stack and signed off against real output, not asserted. Four gates were added
because the design's two items are not sufficient on their own to make the run
meaningful; the additions are part of the sign-off and are marked as such.

| ID       | Item                                                            | Source              | Result on the run of record                                                  |
| -------- | --------------------------------------------------------------- | ------------------- | ---------------------------------------------------------------------------- |
| S-1      | Whole stack up, atlas healthy before injecting                  | added (operational) | PASS — 5/5 services running, atlas health=healthy                            |
| C-1a     | SDK client uses idempotency keys                                | design              | PASS — a push without `--idempotency-key` is refused client-side             |
| C-1b(i)  | The checklist push actually LANDS in the ledger                 | added (see D2)      | PASS — ledger 0 → 1                                                          |
| C-1b(ii) | Same key + same content deduplicates                            | design (C-1 half)   | PASS — identical `record_id` returned, ledger steady at 1                    |
| C-1c     | Same key + different content is refused                         | added               | PASS — `AlreadyExists`                                                       |
| C-1d     | Uniqueness enforced in the schema, not only in application code | added               | PASS — `evidence_records_tenant_idem_uniq` on `(tenant_id, idempotency_key)` |
| C-2      | Fresh tenant scope so prior runs don't pollute the count        | design              | PASS — all four arm tenants at 0 rows                                        |
| H-1      | Harness floor recorded so `wall_ms` is not misread              | added (measurement) | INFO — a no-RPC `atlas-cli version` costs 37ms through the same construct    |

`C-1a` is the item the design flags as conditional ("if not, the duplicate-write
expectation changes"). It passed, so the duplicate-write expectation stands as
written and duplicates are a live falsification criterion throughout.

Scope-discipline gates enforced in the tooling itself, not by operator care
(slice 335 §Scope discipline / P0-335-2, slice 356 P0-1):

- All three scripts **refuse to run** unless the gRPC endpoint host is a
  loopback literal.
- The atlas container to restart is resolved via `docker compose ps -q atlas`
  against the compose file passed in, so the restart cannot reach a container
  belonging to atlas-edge, a hosted deployment, or any unrelated stack.
- No hosted or edge endpoint appears anywhere in `scripts/chaos/`.

Injection was not started until every row above read PASS. `preflight()` exits
non-zero on any FAIL rather than continuing (slice 356 P0: do not skip the
checklist to get to the injection faster).

---

## What was run

- **Stack:** local docker-compose only — `deploy/docker/docker-compose.yml`.
- **Endpoint:** `127.0.0.1:50051` (loopback guard passed).
- **Run of record:** `2026-07-25T00:21:14Z` → `00:26:59Z`, tag `oe385a`.
- **Push client:** `cmd/atlas-cli evidence push`, built from this worktree —
  the shipped SDK path (`cmd/atlas-cli` → `pkg/sdk-go`.Client.Push → the
  `EvidenceIngestService` gRPC RPC). Nothing re-implements the wire protocol.
- **Rate and window:** 1 push/s for 60s per arm, the design's parameters.
- **Injection:** `docker compose restart atlas` at t=+10s, the design's
  mechanism and timing.
- **Arms:** three 60-second windows, each on its own fresh tenant, plus a
  separate recovery stage. See D1 for why there are two injection arms.

Tooling: [`scripts/chaos/run-exp5-atlas-restart.sh`](../../scripts/chaos/run-exp5-atlas-restart.sh)
(orchestrator), [`scripts/chaos/evidence-push-probe.sh`](../../scripts/chaos/evidence-push-probe.sh)
(synthetic SDK client), [`scripts/chaos/ensure-chaos-tenant.sh`](../../scripts/chaos/ensure-chaos-tenant.sh)
(fixture; see D2).

---

## Steady state vs injection — the measured comparison (AC-7, AC-8)

Each arm is 60 ticks at 1/s on a fresh tenant. `ledger` is the settled
`evidence_records` count for that tenant after the stream drained.

| Arm                               | Injected | Retry mode      | Attempts | ok  | err | Error codes                             | **Ledger** | Expected |
| --------------------------------- | -------- | --------------- | -------- | --- | --- | --------------------------------------- | ---------- | -------- |
| `steady_state`                    | no       | none            | 60       | 60  | 0   | —                                       | **60**     | 60       |
| `injection_armA` (as shipped)     | yes      | none            | 60       | 11  | 49  | `Unauthenticated` ×49                   | **11**     | 60       |
| `injection_armB` (design backoff) | yes      | 1/2/4/8s+jitter | 62       | 10  | 52  | `Unauthenticated` ×50, `Unavailable` ×2 | **10**     | 60       |

Ledger integrity within each arm — the append-only guarantee:

| Arm              | Rows | Distinct `record_id` | Distinct `idempotency_key` | Distinct `hash` |
| ---------------- | ---- | -------------------- | -------------------------- | --------------- |
| `steady_state`   | 60   | 60                   | 60                         | 60              |
| `injection_armA` | 11   | 11                   | 11                         | 11              |
| `injection_armB` | 10   | 10                   | 10                         | 10              |

**Every arm is 1:1:1:1. No duplicate row, no key collapse, no hash collision
anywhere in the run.**

The failure boundary is exact. In arm A, ticks **0–10 succeeded** and ticks
**11–59 failed**, with no mixed region:

```
last ok tick = 10 · first err tick = 11 · restart issued at t=+10s
error codes across all 49 failures: Unauthenticated ×49  (zero Unavailable)
```

The container was unreachable for so little of the window that the as-shipped
arm never observed a single transport error. It went straight from healthy to
permanently rejected.

**Latency.** `wall_ms` is end-to-end CLI process wall time — process start,
dial, RPC, exit — against a 37ms harness floor (H-1). It is **not** the
platform's ack latency and no platform conclusion is drawn from it.

| Arm              | p50 | p95  | max  |
| ---------------- | --- | ---- | ---- |
| `steady_state`   | 54  | 7547 | 7966 |
| `injection_armA` | 45  | 58   | 65   |
| `injection_armB` | 47  | 7232 | 8164 |

The two large p95s are a harness artifact, not a platform signal, and are
reported rather than dropped. In `steady_state` exactly 10 of 60 attempts
exceeded 1s, and they are contiguous ticks 31–40 with monotonically decaying
wall times (7966, 7952, 7566, 7547, 6652, 5389, 4214, 2932, 2913, 1636 ms) —
the signature of a queue draining after one host-level stall, not of platform
degradation. All 60 records still landed. Arm B's tail is the same shape plus
the deliberate backoff sleeps. The clean arm-A numbers (p95 58ms), measured on
the same host minutes apart, are the better read of the harness's normal cost.

---

## Falsification verdicts

### The slice-356 AC-8 / AC-9 checks

| #   | Check                                    | Measured                            | Verdict       |
| --- | ---------------------------------------- | ----------------------------------- | ------------- |
| F-1 | Ledger equals 60 — not more (duplicates) | arm A **11**, arm B **10**          | **FALSIFIED** |
| F-2 | Ledger equals 60 — not less (lost)       | 49 and 50 records lost respectively | **FALSIFIED** |
| F-3 | All record IDs unique (AC-9)             | 11/11 and 10/10 distinct            | HOLDS         |

F-1 and F-2 are the same design assertion — "should equal 60" — evaluated in
both directions because the design names both failure modes. The duplicate
direction is clean; the loss direction is not.

### The design's abort criteria

| Criterion                                | Measured                        | Tripped |
| ---------------------------------------- | ------------------------------- | ------- |
| SDK client crashes on a transient error  | probe exit 0 in all three arms  | no      |
| Ledger row count > 60 (duplicate writes) | max observed 60, never exceeded | no      |

Neither abort criterion tripped. The run completed as designed.

### The design's hypothesis and expected outcome — measured against its own text

| Design claim (§Experiment 5)                                             | Measured                                                                      | Verdict       |
| ------------------------------------------------------------------------ | ----------------------------------------------------------------------------- | ------------- |
| "the SDK's retry-with-backoff (slice 003) re-sends the record"           | `pkg/sdk-go`.Client.Push issues one RPC; no retry, no backoff exists — see D3 | **FALSIFIED** |
| "The idempotency key prevents duplicate ledger entries"                  | zero duplicates in every arm; 1:1 rows to keys to hashes                      | HOLDS         |
| "No evidence is lost"                                                    | **49 of 60 lost** as shipped; **50 of 60** even with the design's own backoff | **FALSIFIED** |
| "The client perceives one successful push"                               | the client perceives 49 hard failures                                         | **FALSIFIED** |
| "SDK client: emits warning logs during the gap, retries succeed"         | 2 retries fired, 0 succeeded                                                  | **FALSIFIED** |
| "Ledger: row count exactly 60 after the test"                            | 11 / 10                                                                       | **FALSIFIED** |
| "All receipts have unique `record_id`"                                   | yes, in every arm                                                             | HOLDS         |
| "Recovery is bounded by the SDK's backoff schedule (1s, 2s, 4s, 8s)"     | unbounded — the pre-restart credential never recovers; see the recovery stage | **FALSIFIED** |
| Rollback: "none needed — restart was the injection AND its own recovery" | true for the process, false for the push path                                 | **FALSIFIED** |

The one conjunct that holds is the idempotency/dedup claim. It holds
unambiguously and it is the reason the ledger is the healthiest thing in this
report.

### Recovery (design step 6) — three clocks, not one

The design's Rollback field asserts no recovery is needed. That assertion is
itself measurable, so the recovery stage measured it on a dedicated tenant
rather than inheriting it. A credential was proven working **before** the
restart so that any post-restart rejection is attributable to the injection.

| Clock                                            | Measured                 |
| ------------------------------------------------ | ------------------------ |
| gRPC surface answering at all again              | **t+1s**                 |
| Credential issued BEFORE the restart works again | **never** (90s deadline) |
| Push path works again after operator re-issuance | **t+91s**                |

The first non-`Unavailable` status code observed after the restart, at t+1s,
was already `Unauthenticated` — the process was up and rejecting before a
retry schedule could plausibly have fired.

**"The restart is its own recovery" HOLDS for the process and is FALSIFIED for
the push path.** The 91s figure is not a platform recovery time; it is the 90s
observation deadline plus one re-issue round trip. The honest reading is that
recovery of an unattended push client does not happen at all — it waits for a
human.

---

## D1 — Why two injection arms, and why that was load-bearing

The hypothesis has two conjuncts: the SDK re-sends, AND the idempotency key
prevents duplicates. Reading `pkg/sdk-go/client.go` before the run showed
`Client.Push` issuing exactly one RPC with no retry anywhere in the package, so
a single-arm run could only ever have reported on the first conjunct and would
have left the second silently untested — a run that "falsifies the hypothesis"
without ever having exercised half of it.

Two arms were run instead:

- **Arm A, `--retry-mode none`** — `pkg/sdk-go` exactly as shipped. Answers
  "does evidence survive a restart today?"
- **Arm B, `--retry-mode design`** — the design's own "1s, 2s, 4s, 8s with
  jitter" applied by the caller around the same single-shot Push, with a
  byte-identical record and the same idempotency key on every attempt.
  Answers "if the retry the design assumes existed were added, would the
  ledger dedup it?"

This is not a redesign of the experiment — the injection, rate, window, and
assertion are unchanged. It is the same Method run twice so both conjuncts get
a verdict.

The second arm earned its place. It produced the run's sharpest result: the
design's own remediation, applied faithfully, **recovered zero records**. Ticks
10 and 11 caught the container genuinely down, returned `Unavailable`, backed
off, retried — and got `Unauthenticated`. Had only arm A been run, the natural
conclusion would have been "add retry to the SDK and this is fixed." Arm B shows
that conclusion is wrong.

## D2 — The fixture guard inherited from slice 356a

`evidence_records` carries a composite FK on `(tenant_id, control_id)`. Because
the ingest path acks at NATS stream-commit **before** the ledger write
(constitutional invariant #2, `streambuf.go`), a control belonging to another
tenant still returns a 200 receipt while every insert fails its FK and
redelivers forever. The ledger then reads zero rows and a count-based
falsification check passes vacuously. Slice 356a hit exactly this trap (its
decisions log D2).

`ensure-chaos-tenant.sh` provisions the tenant, a **tenant-owned** control, and
a tenant-scoped credential as one unit, and refuses a tenant that already holds
ledger rows. Preflight `C-1b(i)` independently confirms a real row appears
before any injection is permitted. Both guards passed on the run of record.

## D3 — Where the "retry-with-backoff (slice 003)" claim actually comes from

The design attributes retry-with-backoff to slice 003. Slice 003's own
acceptance criteria never mention retry or backoff — they cover the proto
contract, the CLI surface, required-field rejection, and mock dedup. The claim
originates in [`Plans/EVIDENCE_SDK.md`](../../Plans/EVIDENCE_SDK.md):

> §"SDK surfaces": "Each SDK exposes the same surface: `client.evidence.push(record)`,
> `client.evidence.push_batch([...])`, schema validation client-side before
> transport, **automatic retry/backoff**, structured errors."
>
> §"429 with `Retry-After` on overage. The reference SDK respects this and
> exponential-backs-off."

`pkg/sdk-go` implements none of it. This matters for how G-2 is scoped: the
retry gap is a **spec-versus-implementation** divergence in the SDK contract
document, not a regression in slice 003 against its own ACs. The design's
attribution is a misreading of provenance, not of substance — the claim is
real, it just lives in `EVIDENCE_SDK.md`.

Slice 356 P0-3 forbids modifying the SDK's retry configuration, and nothing here
does. The experiment verifies the existing contract; the divergence is reported,
not patched.

## D4 — Why the loss is total rather than transient: the in-memory credstore

Root cause, confirmed in code rather than inferred from the status code.

`internal/api/credstore/credstore.go` is, by its own package comment, "an
in-memory credential store." Its state is two Go maps (`byID`, `byTokenHash`)
behind a mutex. There is no DB table behind it and no load-at-boot path.
`cmd/atlas/main.go:518` says so explicitly:

> the two bootstrap credential-issuance calls below … write into the
> **IN-MEMORY** `credstore.Store` — NOT the `api_keys` table.

So `docker compose restart atlas` does not merely interrupt in-flight pushes —
it **destroys every credential ever issued to a connector or CI job**. Every
subsequent push on that bearer is `Unauthenticated` forever. That is precisely
the measured shape: an instantaneous cliff at the restart boundary, 49
consecutive `Unauthenticated`, and a pre-restart credential that never recovers.

The bootstrap fixed-token admin credential survives only because
`ATLAS_BOOTSTRAP_TOKEN` is re-minted from the environment on each boot — which
is why the recovery stage's re-issue path works while the tenant credential
never does. The asymmetry is the tell: the credential that lives in config
survives, the credential that lives in RAM does not.

## D5 — A correction to the tooling's own verdict heuristic

Recorded because the run of record printed a line that overstates its evidence,
and the artifact is committed alongside this log.

`verdict()` asks whether the injection actually exercised the dedup conjunct.
The heuristic as it ran counted retry attempts (`attempt > 1`) and printed:

> YES — 2 retry attempts fired in the backoff arm, so a same-key re-send did
> reach the ledger and the dedup claim is under test by the injection.

That is wrong. Both retries returned `Unauthenticated`, and a re-send rejected
at the auth boundary never reaches `Service.Process` — it cannot exercise the
ledger's dedup no matter how faithfully the client re-sent it. Re-derived from
the same artifacts:

```
retry attempts that FIRED:          2
retry attempts the LEDGER answered: 0
```

The corrected condition is a retry attempt answered by the ledger — `ok`
(deduplicated to the original receipt) or `AlreadyExists` (same key, different
content). On this run it yields **NO**: the dedup conjunct was **not** exercised
by the injection. It is verified out-of-band by preflight `C-1b(ii)` and `C-1c`,
which is the basis for the HOLDS verdict recorded above — and that basis is now
stated precisely rather than borrowed from a heuristic that could not carry it.

The heuristic is fixed in `run-exp5-atlas-restart.sh` as committed. The run of
record's `verdict.txt` predates the fix; this decision is the correction of
record, and the underlying `injection_armB.tsv` supports the corrected reading
directly.

## D6 — On the slice-332 baseline

Slice 335 §Cross-references ties the slice-332 load-test parameters (10 req/s
synthetic, P95 < 100ms reads) to Experiments **1 and 2**, and instructs the
executing slice to re-derive if the baseline has shifted.

Experiment 5 does not draw on them. Its steady state is defined quantitatively
and self-containedly by the design itself — "pushes records at 1/s for 60
seconds; ledger row count grows by 60" — so there is no slice-332 rate
parameter in play and nothing to re-derive. The blocked-path in the slice
narrative ("baseline stale or unreproducible") therefore does not apply.

For completeness: slice 332's ingest-surface section publishes **no measured
per-push latency number** at all — it is a static-analysis review whose stated
reference is `Plans/EVIDENCE_SDK.md` §3's "ack within 50ms at p95 under typical
connector load (1–10 RPS per credential)". This experiment's 1/s rate sits
inside that band. Slice 332's one published figure (5.89ms / 6.91ms p95) is the
slice-008 UCF graph-traversal benchmark on a different surface — noted so a
later reader does not mistake it for a baseline this run regressed against.

Steady state was measured rather than assumed regardless, and its numbers are in
the comparison table above. They are this experiment's own baseline.

## D7 — Duplicates were a live criterion, not an untested one

Worth stating because "no duplicates" is easy to report vacuously: a run in
which nothing is ever re-sent trivially produces no duplicates.

The dedup path was genuinely exercised on this stack, three ways, all before
injection: `C-1b(ii)` replayed an identical record and got the identical
`record_id` back with the ledger unmoved; `C-1c` replayed the same key with
different content and was refused `AlreadyExists`; `C-1d` confirmed the
guarantee is enforced by a schema-level partial-unique index
(`evidence_records_tenant_idem_uniq`), not only by application code. The ledger
also holds JetStream `Nats-Msg-Id` dedup upstream of it (`streambuf.go`).

What the injection did not do is drive a same-key re-send through to the ledger
(D5), because auth rejected the re-sends first. The HOLDS verdict on the dedup
conjunct rests on the out-of-band checks above and on the 1:1:1:1 row/key/hash
ratios in all three arms — stated here so the strength of that verdict is not
overread.

---

## Resilience gaps and follow-ups filed

| ID  | Gap                                                                                                                                  | Severity | Follow-up      |
| --- | ------------------------------------------------------------------------------------------------------------------------------------ | -------- | -------------- |
| G-1 | Push credentials live only in an in-memory store; any atlas restart silently invalidates every issued credential, permanently        | high     | OPENENGINE-435 |
| G-2 | `pkg/sdk-go`.Client.Push has no retry/backoff, though `Plans/EVIDENCE_SDK.md` twice states the SDK retries and exponential-backs-off | medium   | OPENENGINE-436 |

G-1 is the significant finding. A connector or CI job pushing evidence gets
`Unauthenticated` forever after a routine restart, with no signal that a
credential re-issue is what's required — and the platform whose job is to hold
the evidence record is the thing that stops accepting evidence. Severity is
high on operational impact even though nothing in the ledger is corrupted.

G-2 is filed separately and at lower severity precisely because this experiment
showed retry alone does not fix G-1 (D1). It is a real contract divergence worth
closing on its own merits — a genuinely transient failure would benefit — but it
must not be mistaken for the remedy to G-1. Both OE bodies say so explicitly, so
whichever is picked up first does not absorb the other's scope.

Both are filed as children of OPENENGINE-385.

---

## Acceptance criteria — status

Slice 356's ACs, Experiment 5 half.

| AC          | Status                | Note                                                                                                        |
| ----------- | --------------------- | ----------------------------------------------------------------------------------------------------------- |
| AC-1 – AC-5 | NOT IN SCOPE          | Experiment 3; executed by slice 356a                                                                        |
| AC-6        | MET                   | Checklist executed and signed off against real output; see table above                                      |
| AC-7        | MET                   | 1/s for 60s per arm; `docker compose restart atlas` injected at t=+10s; metrics captured per attempt        |
| AC-8        | **FAILED — reported** | Ledger is 11 (arm A) and 10 (arm B), not 60. Under (lost), never over (duplicated). Recorded as the finding |
| AC-9        | MET                   | All record IDs unique in every arm                                                                          |
| AC-10       | PARTIAL               | This log covers Experiment 5; 356a covers Experiment 3. The bundled 356 path is intentionally not created   |
| AC-11       | MET                   | Cross-references slice 335 (design) and slice 003 / `EVIDENCE_SDK.md` (the retry contract under test)       |

AC-8 is an assertion about the platform, not about this slice's execution. It
did not hold. Slice 356's own guidance — "if Exp 5's ledger count is OFF by even
one, the SDK's idempotency contract is broken — file a high-severity follow-up
immediately" — is honored by G-1, with the refinement that the count is off for
an authentication reason rather than an idempotency one: idempotency is the part
that worked.

## Cross-references

- Slice **335** — [`docs/audits/335-chaos-experiment-design.md`](../audits/335-chaos-experiment-design.md) §Experiment 5, the design contract. Not modified by this slice.
- Slice **356** — [`docs/issues/356-data-tier-outage-chaos-round-1.md`](../issues/356-data-tier-outage-chaos-round-1.md), the bundled execution narrative.
- Slice **356a** — Experiment 3 execution; source of the D2 fixture guard.
- Slice **003** — [`docs/issues/003-evidence-sdk-proto-push-client-cli.md`](../issues/003-evidence-sdk-proto-push-client-cli.md), the SDK push contract; see D3 for where the retry claim actually originates.
- Slice **332** — [`docs/audits/332-performance-audit-report.md`](../audits/332-performance-audit-report.md); see D6 for why its baseline is not load-bearing here.
- Slice **015** — the NATS streambuf ingest substrate; its ack-before-persist shape is what makes the D2 guard necessary.
