# Slice 417 — integration-shard decisions log

**Type:** JUDGMENT. The package→leg split, the shard count, the per-shard
infra shape, and the watermark rewiring are subjective calls made here and
recorded for post-deployment revisit. No human sign-off gate.

- detection_tier_actual: none
- detection_tier_target: none

(No product bug surfaced during the slice — this is a pure CI restructure.
The slice's own guards — the shard-coverage union check + the per-leg `-p 1`
proof — are the detection surface for the class of bug this slice could
introduce; both are wired into CI on Leg A.)

## Decisions made

### D1 — Three Phase B shards (not two), plus Leg A. Confidence: medium.

Options: 2 Phase B shards (~32 pkgs each) vs 3 (~24 each). Chose **3**.
~89 packages with Leg A holding the 16-package catalog/platform cluster
leaves ~73 tenant-scoped packages for Phase B. Three shards land at
24/23/26 — a balanced split well under the old ~89-serial wall-clock. Two
shards (~36/37) would still roughly halve wall-clock but leave less
headroom as the package set grows. Three is the sweet spot for the current
size; the manifest makes re-balancing (or going to 4) a one-file edit
guarded by the coverage-union check. **Revisit** if a fourth shard's
fixed bring-up overhead (~Postgres+MinIO+NATS startup, ~30-60s) starts to
dominate the per-leg test time — at that point fewer, fatter shards win.

### D2 — Each shard gets its OWN service container (matrix replication), not a shared DB. Confidence: high.

A `strategy.matrix` over `leg: [A, B1, B2, B3]` replicates the entire job
— including the `services.postgres` block and the MinIO/NATS `docker run`
bring-up steps — onto a separate runner per leg. So each leg has its own
ephemeral Postgres/MinIO/NATS and its own seed. This is the whole point:
sharding works **because** the databases are isolated, so two parallel
legs cannot race on `scf_anchors` / `evidence_kind_schemas` /
`evidence_records` the way two binaries in one shared DB could. A shared DB
across legs would re-introduce exactly the race `-p 1` existed to prevent
(threat T-2) — rejected. The cost is N× the bring-up; accepted (see D6).

### D3 — Leg A = the SCF-catalog seed cluster + eval/control consumers. Confidence: high.

P0-2 pins `db`, `schemaregistry`, `evidence/ingest`, and the SCF-catalog
cluster (`scfimport`, `anchors`, `scfseed`, `soc2import`, `ucfcoverage`)
to Leg A. I extended Leg A to also hold `eval`, `decision`,
`controlstate`, `control`, `controls`, `controldetail`, and
`catalog/metrics` — the consumers most tightly coupled to the
freshly-seeded catalog and the ones the slice-461 order-independence guard
truncates+reseeds. Keeping them with the seeder on the same serial leg
removes any chance of a catalog-state surprise and keeps the
order-independence guard (which lives on Leg A) operating against the
catalog it expects. **Migration analysis (the load-bearing one):**
`scf_anchors` has **no `tenant_id`** (global catalog) — the genuine
cross-binary collision. `evidence_kind_schemas` has a `tenant_id` but a
catalog read-path (`tenant_or_catalog_read`) whose seed rows are
catalog-global; `schemaregistry` + `evidence/ingest` seed them, both on
Leg A. `evidence_records` is fully tenant-scoped (`(tenant_id,
idempotency_key)` unique, RLS-forced) — its cross-binary safety is the RLS
tenant key, NOT `-p 1`, so the many Phase B packages that write
`evidence_records` are safe to shard (each test uses its own tenant).
**Revisit** if a new global (no-`tenant_id`) seed table lands: its
seeders must join Leg A and the P0-2 pin set in
`check-integration-shard-coverage.sh` must grow to match.

### D4 — Manifest file as source of truth; CI matrix + guards read it. Confidence: high.

The package→leg map lives in `scripts/integration-shards.txt`, read by
`run-integration-shard.sh` (the test runner) and
`check-integration-shard-coverage.sh` (the completeness guard). Inlining
89 packages × 4 matrix `include:` blocks in ci.yml would be unreadable and
un-auditable; a single checked-in manifest makes the assignment a reviewable
diff and re-balancing a one-file edit. The slice-345 enrolment guard was
updated to read the manifest too, so "every tagged package is enrolled
somewhere" keeps holding after the list moved out of ci.yml's inline
`go test`.

### D5 — Coverage merged across ALL legs before the slice-279 gate. Confidence: high.

The fan-in `tests-integration` job downloads the unit profile + every
leg's integration profile and `gocovmerge`s them into the single profile
the slice-279 gate reads (AC-7 / P0-3). Running the gate on one leg's
fragment would under-count and break the ratchet (threat T-3). Floors are
**not** lifted (P0-3 — the ratchet is monotonic; lifting is a separate PR
with tests). **AC-8 union==pre-shard proof:** the merged-union profile is
the gocovmerge of the SAME per-package `go test ... -coverpkg=./...` runs
the old single job produced — identical package set (proven by the
shard-coverage guard: union == the 89-package tagged set), identical
`-coverpkg`, identical `-covermode=atomic`. gocovmerge is associative and
commutative over coverage blocks, so merging unit + {A,B1,B2,B3} yields the
same union profile as the old unit + {single-serial-run}. The slice-279
gate therefore sees the same statement universe and the same per-package
numbers (within atomic-counter rounding) as before the shard. No floor
moved; the gate passing on the union is the runtime evidence.

### D6 — AC-14 billable-minutes trade-off (recorded honestly). Confidence: medium.

Sharding trades **wall-clock** for **parallel billable minutes**. Before:
one runner × ~89 packages serial (~9-13 min wall-clock == ~9-13 billable
min, plus one bring-up). After: 4 runners, each with its own bring-up
(~30-60s Postgres+MinIO+NATS+migrate+seed) + its package subset, running
concurrently. **Wall-clock** drops to roughly the critical-path leg
(target: the slowest of A/B1/B2/B3, ~3-5 min) + the fan-in (~1-2 min for
download+merge+gate). **Billable minutes** rise: 4× the fixed bring-up
overhead (~2-4 extra runner-min total) + the fan-in job's minutes, but the
test execution minutes are unchanged in aggregate (the same packages run,
just spread across runners). Net: **~2-3× faster wall-clock for the
merge-blocking path at the cost of ~30-50% more billable minutes on the
integration tier.** For an OSS project on free-for-public-repos GHA
minutes, wall-clock (merge velocity) is the scarce resource, not minutes —
the trade is favorable. The real numbers land on the first few green CI
runs; **revisit** the exact before/after from `docs/integration-wallclock.tsv`
(now per-leg) once the watermark has a few samples.

### D7 — AC-9 known-flaky tests + leg assignment. Confidence: medium.

The one documented integration flake is
`TestRun_FiresInlineSweepAndExitsOnCancel`
(`internal/metrics/scheduler/integration_test.go`) — the scheduler timing
flake that blocked slice 346's PR #788. `internal/metrics/scheduler` is
assigned to **Leg B2** in the manifest. **Decision: do NOT block this
shard on a scheduler-flake fix.** Rationale: (a) the flake is a
single-package timing race, not a cross-binary collision sharding could
worsen at the platform layer; (b) under the old single serial job a
scheduler flake already failed the whole integration check — under
sharding it fails only Leg B2, and the fan-in surfaces _which_ leg flaked,
which is strictly **more** diagnosable, not less; (c) Q-16 (no retry,
investigate every flake) is unchanged — no `-retry` is added on any leg
(P0-6). The D-1 "sharding multiplies blast radius across re-runs" concern
in the spec is real but bounded: a flake now fails 1 of 4 legs instead of
1 of 1, and the per-leg check name pinpoints it. The scheduler-flake fix
remains worth doing on its own merits (a separate slice), but it is **not
a blocker** for this shard. **Revisit** if, post-shard, the scheduler
flake's per-leg failure rate is materially higher than its historical
single-job rate (it should not be — same package, same `-p 1`, same DB
shape) — that would indicate a sharding-induced timing change worth a
focused look.

### D8 — Wall-clock watermark measures per-leg (critical-path readable). Confidence: medium.

AC-10: the slice-393 watermark now records one row per leg on clean main,
stamping `<sha>-shard-<leg>` in the SHA column. The critical-path leg is
the max across legs, computed when _reading_ `docs/integration-wallclock.tsv`,
not at write time — matching slice 393's "recorder, not gate" posture
(P0-393-3). The 20-min trip-wire fires off whichever leg crosses it, which
is correct: the merge-blocking latency is the slowest leg, and any single
leg exceeding 20 min is the signal to re-balance. **Revisit** if a
consumer of the TSV wants an explicit per-push max-row; that is a watermark
read-side enhancement (a slice), not part of this restructure.

## Revisit once in use

1. **(D6, high priority)** Pull the real before/after wall-clock + billable
   minutes from the first ~5 green CI runs and `docs/integration-wallclock.tsv`;
   confirm the wall-clock win and the minutes cost match the D6 estimate.
2. **(D1)** Re-balance B1/B2/B3 if one leg is the persistent critical path.
   If a 4th shard's bring-up overhead starts to dominate, consider fewer,
   fatter shards instead.
3. **(D3)** When a new global (no-`tenant_id`) seed table lands, add its
   seeders to Leg A and to the `PHASE_A_PINNED` set in the coverage guard.
4. **(D7)** Watch the scheduler-flake per-leg rate; fix-before-shard was
   judged unnecessary, but confirm sharding did not change its timing.
5. **(slice 418 pairing)** When the slice-418 composite stack-up action
   lands, fold the inlined Postgres/MinIO/NATS bring-up (currently
   matrix-replicated) into the composite so the bring-up is defined once.
   Until then, the matrix replication is the deduplication mechanism (one
   job def, N legs) — it is NOT 4× copy-pasted YAML.

## Confidence summary

| Decision                                  | Confidence |
| ----------------------------------------- | ---------- |
| D1 — 3 Phase B shards                     | medium     |
| D2 — own service container per shard      | high       |
| D3 — Leg A = catalog seed + consumers     | high       |
| D4 — manifest source of truth             | high       |
| D5 — merge all legs before slice-279 gate | high       |
| D6 — billable-minutes trade-off           | medium     |
| D7 — scheduler flake not a blocker        | medium     |
| D8 — per-leg watermark                    | medium     |
