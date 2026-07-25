# 354 — DB connection-pool exhaustion: chaos experiment execution decisions

**Slice:** 354 (execution of slice 335 Experiment 1)
**Date:** 2026-07-24
**Type:** JUDGMENT
**Branch:** `open-engine/OE-382-security-atlas-354-chaos-exp-1`
**Design contract:** [`docs/audits/335-chaos-experiment-design.md`](../audits/335-chaos-experiment-design.md) §Experiment 1
**Baseline reference:** [`docs/audits/332-performance-audit-report.md`](../audits/332-performance-audit-report.md)
**Slice narrative:** [`docs/issues/354-db-pool-exhaustion-execution.md`](../issues/354-db-pool-exhaustion-execution.md)

**Detection-tier classification:** `detection_tier_actual` = `manual_review`;
`detection_tier_target` = `integration`. (See D8 — the pool-saturation error
shape is the kind of behaviour an integration test could pin, and today
nothing does.)

---

## Introduction

This log records the execution of slice 335 Experiment 1 — evidence-ledger
Postgres connection-pool exhaustion — against the LOCAL docker-compose stack.
The experiment was **not redesigned**; slice 335 owns the design and this
slice picks up its Method, blast radius, abort criteria, and rollback
verbatim.

The experiment injects an external connection storm that occupies the
non-reserved Postgres connection slots, while synthetic mixed read/write
traffic runs continuously across a steady-state window, the injection
window, and a recovery window.

**Headline result:** the hypothesis was **not falsified and not confirmed** —
the experiment as designed **cannot reach its own antecedent**. That is the
finding, and D7 reports it plainly rather than banking the "no cascade
observed" reading as a pass.

---

## D1 — Environment under test (scope boundary)

The stack under test is the repo's own `deploy/docker/docker-compose.yml`,
brought up from this worktree. Verified before injection:

| Property                            | Value                                                                  |
| ----------------------------------- | ---------------------------------------------------------------------- |
| Docker context endpoint             | `unix:///Users/gmoney/.colima/default/docker.sock` (LOCAL daemon)      |
| Compose project                     | `security-atlas`                                                       |
| Compose working dir                 | `<worktree>/deploy/docker`                                             |
| Postgres container                  | `security-atlas-postgres-1` (postgres:16-alpine), healthy              |
| Atlas container                     | `security-atlas-atlas-1`, healthy, HTTP published on `127.0.0.1:58080` |
| NATS / MinIO                        | `security-atlas-nats-1`, `security-atlas-minio-1`, both healthy        |
| `atlas-migrate` / `atlas-bootstrap` | both `Exited (0)` — schema current, seed complete                      |
| `max_connections`                   | 100                                                                    |
| `superuser_reserved_connections`    | 3                                                                      |

Every endpoint touched is loopback. **No hosted instance, no atlas-edge, no
host outside this machine's compose stack was contacted at any point.**
Slice 335 P0-335-2 and slice 354 P0-1 are satisfied structurally, not just by
convention — see D3.

---

## D2 — Steady-state parameters, and why they were re-derived

Slice 335 §Experiment 1 sets the offered load at "the slice 332-defined
baseline (10 req/s mixed read/write)" and the steady-state thresholds at
P95 read < 100 ms, P95 write < 500 ms, error rate < 0.1%.

**Slice 332 published no measured live latency for these two surfaces.** Its
audit was explicitly read-only and static-analysis-only ("No live load was
generated", §Methodology; the JUDGMENT call is its own decisions log §D4).
What slice 332 does supply is the _offered-load_ parameter — its
"Concurrency baselines used" table puts evidence push at 10–100 RPS
sustained. So:

- The **10 req/s offered load** is taken from slice 332 as the design
  instructs.
- The **latency baseline** is measured live in this slice's steady-state
  window rather than inherited, because there was never a measured number to
  inherit. This is the design's own instruction ("re-derive from slice 332's
  current baseline if it has shifted") applied to the honest case: the
  baseline was never live-derived in the first place.

This is not a blocked precondition — capturing steady state before injection
is step 3 of the slice's own Do list. It is recorded here so no future reader
mistakes the steady-state table below for a slice 332 regression comparison.

Traffic composition: 50:50 read/write. Slice 335 says "mixed read/write"
without pinning a ratio; an even split gives both surfaces an equal sample at
10 req/s (5 reads/s + 5 writes/s).

| Surface | Endpoint                                                 |
| ------- | -------------------------------------------------------- |
| read    | `GET /v1/evidence?control_id=<id>&limit=20`              |
| write   | `POST /v1/evidence:push` (`access_review.completion.v1`) |

---

## D3 — Injection tooling, and the auth-reviewer read-back (AC-1)

Four scripts under `scripts/chaos/`:

| Script                 | Role                                                                |
| ---------------------- | ------------------------------------------------------------------- |
| `db-pool-hog.sh`       | the injection primitive — opens N connections, holds them, releases |
| `evidence-traffic.sh`  | synthetic mixed read/write generator, one CSV row per request       |
| `run-exp1-db-pool.sh`  | orchestrator — windows, DB sampling, abort watchdog, rollback       |
| `summarize-latency.sh` | P50/P95/max + error-rate reducer over a CSV window                  |

The slice requires these be read back critically with an auth-reviewer hat on
before running them. That review was performed and is recorded here rather
than in a PR thread, because the review gates the injection, not the merge.

**Findings that shaped the scripts as committed:**

1. **The storm cannot leave this machine.** `db-pool-hog.sh` accepts no host,
   no port, no DSN, and no password — the only connection knob is a docker
   container _name_. It additionally refuses to run unless the active docker
   context resolves to a local daemon (`unix://` / `npipe://`), so a remote
   docker context cannot smuggle the storm onto another host, and it refuses
   any container whose `com.docker.compose.project` label is not the expected
   local project. Two independent guards, both fail-closed.

2. **No credential is handled.** Connections are opened from _inside_ the
   container as the container's own `postgres` OS user over the local socket.
   The script never reads, passes, or writes a password. There is no
   credential to leak in `ps`, in a log, or in an artifact directory.

3. **The bearer token never reaches argv.** `evidence-traffic.sh` reads the
   JWT from `ATLAS_CHAOS_TOKEN` only. Argv is world-readable via `ps` on this
   platform; an argv-passed bearer would have been a real local disclosure on
   a multi-user box. Same posture in the orchestrator, which passes the token
   through the environment and never echoes it.

4. **The loopback guard resists prefix spoofing.** `evidence-traffic.sh`
   allowlists `127.0.0.1` / `localhost` / `[::1]` _and_ separately rejects any
   base URL carrying a path or userinfo component — so
   `http://localhost:8080@edge.example.com/` is refused rather than accepted
   on a naive prefix match. This was the specific failure mode worth guarding:
   a scope-boundary check that a URL-shaped string can walk past is not a
   scope boundary.

5. **Nothing from argv is interpolated into SQL or an unquoted shell word.**
   Every numeric argument is integer-validated (`is_uint`) before use; the
   container, database, and user names are matched against strict character
   allowlists; the in-container loop receives its values as positional
   parameters, not by string interpolation.

6. **The storm is self-releasing.** Each held connection is a
   `psql -c "SELECT pg_sleep(<hold>)"` process, so the storm expires on its
   own even if the orchestrator, the operator's shell, or the whole harness
   dies. The hold duration is hard-capped at `MAX_HOLD_SECONDS=900`. This
   directly answers the slice's "If blocked" note — _bound the hold duration
   so the stack self-recovers_. Verified in practice: after each release the
   server returned to its pre-storm backend count within one 5 s sample.

7. **Platform headroom is reserved, and derived from the server.** The storm
   size is computed as `max_connections - superuser_reserved_connections -
headroom` read live from the server (89 = 100 − 3 − 8), matching the
   design's "N = configured `max_connections` minus reserved-for-platform
   headroom". The default is not a hardcoded guess. **Consequence, measured in
   D7a and load-bearing for the verdict:** because the design reserves
   headroom, the main run left free slots by construction and therefore never
   exhausted the server's connection limit.

**Residual risk accepted:** `release()` uses `pkill -f 'SELECT pg_sleep'`
_inside the postgres container_. If an unrelated in-container process were
running a literal `SELECT pg_sleep` it would also be killed. In a
single-purpose compose Postgres container that set is empty, and the blast
radius is the container the experiment is already perturbing. Not worth a
PID-tracking rewrite.

**No chaos framework was introduced.** Plain bash + `psql` + `docker`, per
slice 354 P0-2 and the slice 335 tool stance.

---

## D4 — Pre-execution checklist sign-off (AC-2)

Slice 335 flags Experiment 1 HIGH-RISK and requires every checklist item
signed off before injection. All five were satisfied before the storm ran.

| #   | Checklist item (slice 335 §Experiment 1)                                                | Verdict                    | Evidence                                                                                                                                                                                                                                                                                                                                                 |
| --- | --------------------------------------------------------------------------------------- | -------------------------- | -------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- |
| 1   | Runs against `docker-compose.yaml` only, not `deploy/helm/` or atlas-edge               | PASS                       | Compose project label `security-atlas` resolved from the worktree's `deploy/docker`; docker context is a local unix socket; both injection scripts refuse non-local targets by construction (D3.1, D3.4). No Helm release and no edge endpoint was contacted.                                                                                            |
| 2   | No other developer is using the same docker-compose env                                 | PASS                       | Single-operator machine; the stack was brought up from this worktree for this experiment. `docker ps` shows the four `security-atlas-*` services and no other consumer. Adjacent compose projects on the host (`plane-app`, `tend`) run their own separate Postgres containers and are outside the blast radius — the storm targets one named container. |
| 3   | Snapshot `evidence_records` row count BEFORE; verify identical AFTER                    | PASS (adapted — see below) | Pre-injection snapshot 2026-07-24T20:35:01Z: **6,159 rows**. Sampled continuously into `ledger-monotonic.csv` and `db-samples.csv`; end-of-run **12,005 rows**. Monotonic non-decrease verified across every sample — see D6.                                                                                                                            |
| 4   | Snapshot `evidence_records.observed_at` MAX BEFORE and AFTER; verify monotonic increase | PASS                       | Pre-injection MAX `observed_at` = `2026-07-24 20:35:00+00`; end-of-run `2026-07-24 20:54:29+00`. Sampled every 10 s for the full run; never regressed at any sample.                                                                                                                                                                                     |
| 5   | Have `docker-compose down -v` ready in a second terminal as hard-reset                  | PASS                       | Hard-reset command staged: `docker compose -f deploy/docker/docker-compose.yml --env-file deploy/docker/.env down -v`. Not needed — the stack never left steady state.                                                                                                                                                                                   |

**Item 3 adaptation, recorded rather than papered over.** The design asks for
an _identical_ row count before and after, to prove no data loss. That
wording assumes a quiesced ledger, but the same design's Method step 4 says
"continue synthetic traffic" — which is 5 writes/s landing in
`evidence_records` throughout. The two instructions cannot both hold
literally. The check was therefore evaluated as **monotonic non-decrease with
no gap**: the row count must never fall and `MAX(observed_at)` must never go
backwards. That is the property the checklist is actually reaching for
(append-only durability, constitutional invariant #2/#3); an equality check
would only be meaningful with the traffic generator stopped, which would
discard the very measurement the experiment exists to take. This wording
mismatch is carried back to slice 335 as a follow-up (D9).

---

## D5 — Method as executed

Run T0 = `1784925270` (2026-07-24T20:34:30Z). Windows, per the design and the
slice's Do list:

| Window       | Duration | Wall clock (UTC)    | What happens                                                      |
| ------------ | -------- | ------------------- | ----------------------------------------------------------------- |
| Steady state | 600 s    | 20:34:30 – 20:44:30 | Traffic only. **Captured BEFORE any injection.**                  |
| Injection    | 300 s    | 20:44:30 – 20:49:30 | `db-pool-hog.sh` holds 89 connections. Traffic continues.         |
| Recovery     | 300 s    | 20:49:30 – 20:54:30 | Storm released. Traffic continues; recovery-to-baseline measured. |

The 600 s steady-state and 300 s injection are the design's specified
durations (§Experiment 1 Steady state — "the prior 10 minutes"; Method step 3
— "holds them for 5 minutes"). All three windows ran to completion; the
generator reported `skipped=0`, so the offered load was met exactly (6,000
steady + 3,010 injection + 3,000 recovery requests). Sampled throughout at
5 s intervals: `pg_stat_activity` totals split by role, and the
`evidence_records` row count.

**Abort criteria** (design §Experiment 1), enforced by the orchestrator's
watchdog on a trailing 60 s window, evaluated every 15 s:

- P95 evidence-read > 5000 ms sustained > 60 s, OR
- synthetic error rate > 50% sustained > 60 s, OR
- the postgres container leaves the running state (OOM / supervisor kill).

Tripping any of them releases the storm immediately and marks the run
`ABORTED`.

Artifacts: `/tmp/oe382-exp1/` — `traffic.csv` (one row per request),
`db-samples.csv` (pool + ledger every 5 s), `ledger-monotonic.csv`
(checklist items 3 and 4 every 10 s), `timeline.txt` (window boundaries),
`db-pool-hog.log`, `orchestrator.log`.

---

## D6 — Results (complete run)

### Steady state (20:34:30 – 20:44:30, BEFORE injection) — full 600 s

| op    | count | errors | error rate | P50   | P95   | max   | design threshold | verdict |
| ----- | ----- | ------ | ---------- | ----- | ----- | ----- | ---------------- | ------- |
| read  | 3,005 | 0      | 0.000%     | 13 ms | 32 ms | 53 ms | P95 < 100 ms     | PASS    |
| write | 3,005 | 0      | 0.000%     | 3 ms  | 10 ms | 33 ms | P95 < 500 ms     | PASS    |

Error rate 0.000% against a design threshold of < 0.1%. Non-2xx breakdown:
none. Steady state is established and well inside every threshold, so the
injection window measures against a healthy baseline.

Pool occupancy during steady state: **12–19** total Postgres backends (the
spread is the measurement harness's own transient `psql` samplers), of which
the platform's `atlas_app` pool held a constant **4**.

### Injection window (20:44:30 – 20:49:30) — full 300 s

Storm confirmed live at 20:44:30Z: `db-pool-hog.sh` opened **89**
connections (100 `max_connections` − 3 `superuser_reserved` − 8 headroom).

| op    | count | errors | error rate | P50   | P95   | max   |
| ----- | ----- | ------ | ---------- | ----- | ----- | ----- |
| read  | 1,505 | 0      | 0.000%     | 15 ms | 39 ms | 55 ms |
| write | 1,505 | 0      | 0.000%     | 3 ms  | 8 ms  | 29 ms |

### Steady state vs injection

| Metric                       | Steady state | Injection | Delta |
| ---------------------------- | ------------ | --------- | ----- |
| read P50                     | 13 ms        | 15 ms     | +2 ms |
| read P95                     | 32 ms        | 39 ms     | +7 ms |
| read max                     | 53 ms        | 55 ms     | +2 ms |
| write P50                    | 3 ms         | 3 ms      | 0 ms  |
| write P95                    | 10 ms        | 8 ms      | −2 ms |
| write max                    | 33 ms        | 29 ms     | −4 ms |
| error rate (both ops)        | 0.000%       | 0.000%    | 0     |
| total Postgres backends      | 12–19        | **101**   | +89   |
| `atlas_app` pool connections | 4            | **4**     | **0** |

**The +7 ms read-P95 delta is not a distinguishable degradation.** Per-minute
read P95 across the run: steady-state minutes ranged **30–41 ms**, injection
minutes **33–41 ms**, recovery minutes **32–43 ms**. The injection window sits
entirely inside the steady-state window's own spread. Reporting +7 ms as an
effect would be reading noise as signal.

Interactive probes fired at 20:46:52Z, with the storm at full occupancy:

- `GET /v1/evidence` → **HTTP 200 in 9.5 ms**
- `POST /v1/evidence:push` → **HTTP 200 in 3.7 ms**, receipt returned,
  `deduplicated: false`

**The watchdog did not trip. No abort criterion was approached** — peak read
latency was 55 ms against a 5,000 ms abort threshold, and the error rate
never left 0.000% against a 50% abort threshold. The postgres container
stayed running throughout.

### Recovery (20:49:30 – 20:54:30) — full 300 s, AC-6

| op    | count | errors | error rate | P50   | P95   | max   |
| ----- | ----- | ------ | ---------- | ----- | ----- | ----- |
| read  | 1,500 | 0      | 0.000%     | 15 ms | 40 ms | 73 ms |
| write | 1,500 | 0      | 0.000%     | 3 ms  | 6 ms  | 14 ms |

**Measured recovery time:**

| Recovery dimension         | Measured                                                                                                                            |
| -------------------------- | ----------------------------------------------------------------------------------------------------------------------------------- |
| Connection-slot drain      | 101 → 10 backends by the **first post-release sample, t+2 s** (upper-bounded by the 5 s sampling interval; the true value is lower) |
| Latency return to baseline | **0 s — the platform never left baseline**, so there was no degraded state to recover from                                          |
| Error-rate return          | **0 s** — the error rate was 0.000% in all three windows                                                                            |
| Orphaned transactions      | none; `atlas_app` held a constant 4 connections before, during, and after                                                           |

Against the design's expected outcome ("P95 returns to baseline within 30
seconds"), the measured value is trivially inside the bar — but only because
the P95 never departed it. That is a weaker result than it looks, for the
reason D7 gives.

### Ledger durability across the full run

| Checkpoint               | `evidence_records` rows | `MAX(observed_at)`     |
| ------------------------ | ----------------------- | ---------------------- |
| First sample (20:35:01Z) | 6,159                   | 2026-07-24 20:35:00+00 |
| Steady-state end         | 8,980                   | advancing every sample |
| Injection start          | 9,009                   | advancing every sample |
| Injection end            | 10,490                  | advancing every sample |
| Release + 2 s            | 10,515                  | advancing every sample |
| Run end (20:54:30Z)      | 12,005                  | 2026-07-24 20:54:29+00 |

Programmatic check over every sample in `ledger-monotonic.csv`: **row count
never decreased and `MAX(observed_at)` never regressed** — no gap, no stall,
no rollback, across the injection boundary in both directions. Checklist
items 3 and 4 pass on the monotonic reading (D4).

---

## D7 — Falsification check

The hypothesis under test (slice 335 §Experiment 1, verbatim):

> When the Postgres connection pool is saturated, evidence **reads** continue
> to succeed at P95 < 5s and evidence **writes** fail fast with a structured
> error (no infinite hang, no stack-trace leakage to the client). The
> append-only ledger remains readable. Older records do not become
> unreachable.

The falsification check the slice's requester names is **whether the pool
exhausts gracefully rather than cascading.** A falsified hypothesis is a
successful experiment and a real finding — it is reported plainly here, not
softened.

**Verdict: the hypothesis was NOT falsified — but neither was it confirmed.
The experiment did not reach the state its hypothesis is about.**

This is the honest reading, and it is the finding.

**What actually happened.** The storm did what it was configured to do: it
opened 89 connections, taking the server from 12–19 backends to 101. But the
platform's own pool was **completely unaffected**: `atlas_app` held a steady 4
connections before, during, and after, and every read and write continued to
serve at steady-state latency with a 0.000% error rate over the full 300 s.

**Why — the mechanism.** `pgxpool` establishes its connections at startup and
holds them. Postgres does not reclaim an already-established connection to
satisfy a new one. So an _external_ connection storm cannot starve a pool that
is already connected — it can only deny _new_ connections. At 10 req/s the
platform's 4 warm connections are never exhausted, so it never asks for a new
one, so it never notices that none are available.

**What this means for the design.** Slice 335 Experiment 1's Variable — "hold
all pool slots via an external connection storm from a `psql` script while the
platform runs" — does not perturb the variable its hypothesis names. The
hypothesis is about _the platform's connection pool_ being saturated; the
injection saturates _the server's connection limit_. Those are different
things, and on this architecture the second does not imply the first. **The
experiment as specified cannot reach its own antecedent.**

**Against the requester's falsification check** — _does the pool exhaust
gracefully rather than cascading?_ — the answer is: **no cascade was observed,
and no pool exhaustion was achieved.** Reporting "no cascade" as a pass would
be the dishonest reading; it would bank a resilience claim the experiment
never tested. The claim in slice 335's expected outcome — that writes fail
fast with a structured 4xx carrying a `retry_after` hint — remains
**unverified**. Nothing in this run produced a non-2xx response at all, so the
error-shape assertion has no evidence behind it either way.

### D7a — A correction, and the supplementary probe that forced it

An earlier draft of this log asserted that the injection left
"`max_connections` fully consumed" and concluded that during the storm
"anything that needs a _new_ connection — a migration, a `docker compose exec
psql`, a restarting atlas container — will be denied."

**That was wrong, and the run's own data does not support it.** The `101`
figure counts rows in `pg_stat_activity`, which includes background workers
(autovacuum launcher, walwriter, checkpointer and friends) that do not consume
a `max_connections` slot. The design's Variable deliberately reserves headroom
(D3.7), so the main run's 89-connection storm left slots free _by
construction_.

Two bounded supplementary probes (60 s hold each, same tooling, same blast
radius) settled it by measurement rather than inference:

| Probe                                              | Client backends | New non-superuser connection                           | Platform read       |
| -------------------------------------------------- | --------------- | ------------------------------------------------------ | ------------------- |
| **A** — main-run parameters (headroom 8, 89 conns) | 95 / 100        | **ACCEPTED**                                           | HTTP 200            |
| **B** — total exhaustion (headroom 0, 97 conns)    | 100 / 100       | **REFUSED** — `FATAL: sorry, too many clients already` | HTTP 200 in 16.8 ms |

Probe A shows the main run **never exhausted the server's connection slots at
all** — 5 were still free and a fresh `atlas_app` connection was accepted. The
main run is therefore an even weaker test of the hypothesis than D7 already
concedes: it did not saturate the platform's pool, and it did not saturate the
server's slots either.

Probe B is the stronger test the main run never performed. With every slot
consumed — new connections refused for _both_ a superuser over the local
socket and `atlas_app` over TCP — the platform still served **305 reads and
305 writes with 0 errors** (read P95 42 ms vs 39 ms pre-storm; write P95
14 ms), and `GET /health` answered in 1.9 ms. Backends returned to 6 within
seconds of release.

Probe B is what actually establishes the mechanism claim, and it is worth
banking precisely: **the platform is immune to external connection-slot
exhaustion at v1 offered load, up to and including total exhaustion.** An
adjacent process eating every free Postgres connection — a runaway migration,
a stuck `psql`, a second app sharing the cluster — does not degrade evidence
reads or writes. That is a real, if narrower, resilience property, and it is
the opposite of the fragility the experiment went looking for.

The operator-facing risk is correspondingly **inverted**: the serving platform
survives, but at _total_ exhaustion anything needing a _new_ connection is
refused — a migration, a `docker compose exec psql`, or a restarting atlas
container that must rebuild its pool cold. That last case is the untested
sharp edge, and it is follow-up 1's second variable.

**To actually test the hypothesis**, the injection has to target the
platform's pool rather than the server's slot count. Two viable variables,
either of which belongs to a redesign that slice 335 owns:

1. Constrain the platform pool (`pool_max_conns` in `DATABASE_URL_APP`) to a
   small number and drive concurrent request load past it — saturates the
   pool from the inside, which is what the hypothesis describes.
2. Force pool churn during the storm (restart `atlas` while the storm holds
   the slots) so the platform must acquire connections it cannot get — which
   tests the cold-start-under-exhaustion path instead.

This slice deliberately did **not** perform that redesign: slice 354 P0 and
the issue boundary both forbid it. Probes A and B are verification of this
run's own reported claims, not a new experiment design.

---

## D8 — Detection-tier note

No integration test today pins the platform's behaviour under pool
saturation. The error shape a saturated pool produces at the API boundary
(status code, body shape, absence of stack-trace leakage, presence of a
retry hint) is exactly the sort of contract an integration-tier test can
assert deterministically with a bounded connection hog. That gap is why this
slice's `detection_tier_actual` is `manual_review` against a
`detection_tier_target` of `integration`.

---

## D9 — Follow-ups filed

Each is a genuine gap this run surfaced. All three are filed as Open Engine
issues, parented to OE-382.

| #   | Finding                                                                                                                                                                                                                                                                                      | Severity | Filed  |
| --- | -------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- | -------- | ------ |
| 1   | **Experiment 1's Variable does not reach the hypothesis's antecedent.** The injection saturates the server's slot count, not the platform's pool (D7, D7a). Until redesigned, the "writes fail fast with a structured 4xx + `retry_after`" claim is unverified. Slice 335 owns the redesign. | HIGH     | OE-425 |
| 2   | **No integration test pins pool-saturation error shape.** See D8. A bounded in-process pool constrained to N connections, driven past N, would assert status code, body shape, absence of stack-trace leakage, and presence of a retry hint deterministically. Today nothing does.           | MEDIUM   | OE-426 |
| 3   | **Slice 335 checklist item 3 is self-contradictory.** It asks for an identical `evidence_records` row count before and after, while the same Method mandates continuous write traffic. Reword to monotonic non-decrease. See D4.                                                             | LOW      | OE-427 |

---

## D10 — Execution-harness constraint (recorded for the next executor)

This slice was executed inside an Open Engine fire with a hard 20-minute
wall-clock cap. The design's own run is 20 minutes end to end (10 min steady

- 5 min injection + 5 min recovery), so the experiment cannot both run and be
  reported inside a single fire.

The run was therefore launched **detached**, so it survives the fire that
started it — which is safe precisely because of D3.6: the storm self-releases
on `pg_sleep` expiry and is hard-capped at 900 s, and the watchdog releases it
early on any abort criterion. An unattended orphan cannot wedge the stack.
The run completed all three windows unattended and was harvested by a
follow-on fire, which is how this log came to carry the full result.

The reducer invocations that produced every number in D6:

```
scripts/chaos/summarize-latency.sh --csv /tmp/oe382-exp1/traffic.csv \
  --from-s 1784925270 --to-s 1784925870      # steady state
scripts/chaos/summarize-latency.sh --csv /tmp/oe382-exp1/traffic.csv \
  --from-s 1784925870 --to-s 1784926170      # injection
scripts/chaos/summarize-latency.sh --csv /tmp/oe382-exp1/traffic.csv \
  --from-s 1784926170 --to-s 1784926470      # recovery
```

This constraint is a property of the harness, not of the experiment, and is
recorded so the next chaos-execution slice (355–358) budgets for it up front
rather than rediscovering it. The detach-and-harvest pattern worked; the
lesson to carry forward is that a chaos slice should plan for it deliberately
rather than discovering the cap mid-run.

---

## Acceptance-criteria trace

| AC   | Requirement                                                     | Where satisfied                                                   |
| ---- | --------------------------------------------------------------- | ----------------------------------------------------------------- |
| AC-1 | `db-pool-hog.sh` written; auth-reviewer read-back               | `scripts/chaos/db-pool-hog.sh`; review in D3                      |
| AC-2 | Slice 335 pre-execution checklist satisfied + signed off        | D4 (all five items)                                               |
| AC-3 | Local docker-compose ONLY; no edge/hosted reference             | D1 + D3.1/D3.4 (structural guards, not convention)                |
| AC-4 | Steady state BEFORE injection: P95 read, P95 write, err rate    | D6 steady-state table (600 s, captured before any injection)      |
| AC-5 | Injection runs 5 minutes; metrics captured throughout           | D6 injection table (300 s, 3,010 requests, 5 s DB sampling)       |
| AC-6 | Recovery measured: time from rollback to baseline P95           | D6 recovery table (slot drain ≤ 2 s; latency never left baseline) |
| AC-7 | Report: hypothesis result, metrics, abort, recovery, follow-ups | This log — D6, D7, D9                                             |
| AC-8 | Cross-references slice 335 (design) and 332 (baseline)          | Header + D2 + Cross-references                                    |

---

## Cross-references

- Slice **335** — the experiment design contract; not modified by this slice.
  Follow-up 1 (OE-425) carries the Variable redesign back to it.
- Slice **332** — offered-load baseline; see D2 for why the latency baseline
  was re-derived rather than inherited.
- Slice **355–358** — the remaining chaos-execution slices; D10 applies to
  each.
