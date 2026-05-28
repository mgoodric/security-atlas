# 335 — Chaos experiment design

**Slice:** 335
**Date:** 2026-05-28
**Author:** `voltagent-qa-sec:chaos-engineer` persona (instance run)
**Scope:** design-only — eight chaos experiments specified; **NO experiment execution in this slice**
**Branch:** `quality/335-chaos-experiment-design`

---

## Introduction

This document captures the chaos-engineering experiment designs called for
by slice 335. The product makes implicit resilience claims — separation
of ingest and evaluation stages, append-only ledger durability, fail-closed
authz, fail-fast schema validation, RLS-enforced isolation. None of these
claims has been verified under controlled failure. This slice designs the
experiments that would verify (or falsify) each, deferring execution to
v2+ slices listed under spillover.

### Methodology

The eight experiments follow the Principles of Chaos Engineering standard
shape:

| Field                       | Purpose                                                                      |
| --------------------------- | ---------------------------------------------------------------------------- |
| **Hypothesis**              | Falsifiable claim about steady-state behavior under perturbation             |
| **Steady state**            | Metric + threshold defining "normal" before injection                        |
| **Variable**                | The single thing the experiment perturbs                                     |
| **Method**                  | Concrete injection mechanism (manual command, scripted, optional chaos tool) |
| **Blast radius**            | Scope of failure — what the experiment can affect                            |
| **Abort criteria**          | Metric + threshold that triggers immediate rollback                          |
| **Expected outcome**        | What should be observed if the hypothesis holds                              |
| **Rollback**                | How to restore steady state                                                  |
| **Pre-execution checklist** | Prerequisites the executor must verify                                       |
| **Execution-deferral note** | Spillover slice + deferred-to-v2 reason                                      |

The single-variable discipline keeps each experiment falsifiable. The
abort criteria + rollback discipline keeps each experiment safe.

### Scope discipline

All eight experiments target **local docker-compose only**. None targets
atlas-edge, hosted tenants, or production. Slice anti-criterion
**P0-335-2** enforces this; the executing slices (354-358) inherit the
constraint via cross-reference back to 335.

### Tool stance

The Method field of some experiments names chaos-engineering tools
(chaos-mesh, litmus, gremlin, AWS FIS). **No tool is committed as a
runtime or dev dependency by this slice.** The named tools are
documentation of viable injection mechanisms; the actual choice is
deferred to the executing slice. Several experiments require nothing
beyond `docker stop`, `iptables`, and a synthetic-traffic script —
no framework introduction needed.

### Severity rubric for cross-experiment observations

- **Criticality high** = falsification falsifies a constitutional invariant
- **Criticality medium** = falsification falsifies a design claim but not an invariant
- **Criticality low** = falsification surfaces a UX gap (error message, retry behavior)

### Source references

- Slice 335 narrative (`docs/issues/335-chaos-experiment-design.md`)
- Persona spec at
  `~/.claude/plugins/marketplaces/voltagent-subagents/categories/04-quality-security/chaos-engineer.md`
- `CLAUDE.md` constitutional invariants
- `Plans/canvas/04-evidence-engine.md` §4.3 (ingest/eval separation)
- `Plans/canvas/09-tech-stack.md` (NATS JetStream, sqlc pooling, cosign)
- Slice 027 (walkthrough recordings — operator-runbook surface) — per AC-6
- Slice 332 (performance audit — load-test parameters) — per AC-8

---

## Experiment 1 — Evidence ledger DB connection-pool exhaustion

**Criticality:** high (verifies constitutional invariant #3 — append-only ledger durability)

**Hypothesis.** When the Postgres connection pool is saturated, evidence
**reads** continue to succeed at P95 < 5s and evidence **writes** fail
fast with a structured error (no infinite hang, no stack-trace leakage to
the client). The append-only ledger remains readable. Older records do
not become unreachable.

**Steady state.** P95 evidence-read latency < 100ms; P95 evidence-write
latency < 500ms; error rate < 0.1%, measured over the prior 10 minutes
of synthetic traffic at the slice 332-defined baseline (10 req/s mixed
read/write).

**Variable.** Postgres connection-pool max-conn count, perturbed via
`docker-compose exec postgres` setting `max_connections=10` and
restarting the connection-handling process; OR alternative: hold all
pool slots via an external connection storm from a `psql` script while
the platform runs.

**Method.**

1. Start docker-compose local with synthetic-traffic generator at
   baseline.
2. Capture 10-minute steady-state metrics from the OTEL stack (Prometheus
   /metrics from `cmd/atlas`).
3. Inject: run an external connection-hogger script
   (`scripts/chaos/db-pool-hog.sh` — to be authored by slice 354) that
   opens N connections (N = configured `max_connections` minus
   reserved-for-platform headroom) and holds them for 5 minutes.
4. Continue synthetic traffic; capture metrics.
5. Release connections; observe recovery.

**Blast radius.** Local docker-compose Postgres container only. No
cross-tenant impact possible (single-tenant test environment by
construction). No risk to host filesystem or adjacent containers.

**Abort criteria.** P95 evidence-read > 5s sustained > 60s; OR
container-supervisor reports postgres OOM; OR synthetic-traffic
generator's error rate exceeds 50% sustained > 60s.

**Expected outcome.**

- Reads: succeed (P95 may rise but should not exceed 1s — the pool
  prioritization should keep read queries flowing).
- Writes: fail fast with `pgxpool: timeout` → translated to structured
  4xx (not 5xx) at the API surface, with a `retry_after` hint header.
- After connection-storm releases: P95 returns to baseline within 30
  seconds; no orphaned transactions; ledger remains consistent (no
  partial writes).

**Rollback.** Kill the connection-hogger script process. Postgres
releases connections automatically. If pool stays exhausted, restart
the `atlas` container (`docker-compose restart atlas`). Full
docker-compose tear-down restores from scratch.

**Pre-execution checklist (HIGH-RISK — flag for executor).**

- [ ] Confirm test runs against `docker-compose.yaml` only, not
      `deploy/helm/` or atlas-edge.
- [ ] Confirm no other developer is using the same docker-compose env.
- [ ] Snapshot the `evidence_records` table row-count BEFORE the
      experiment; verify identical AFTER (proves no data loss).
- [ ] Snapshot the `evidence_records.observed_at` MAX value BEFORE
      and AFTER; verify monotonic increase (no rollback).
- [ ] Have `docker-compose down -v` ready in a second terminal as
      hard-reset.

**Execution-deferral note.** Deferred to slice **354**
(`docs/issues/354-db-pool-exhaustion-execution.md`). v2+ work because
the script `scripts/chaos/db-pool-hog.sh` does not exist and writing
it touches the executable surface that this slice is explicitly
forbidden from modifying (P0-335-5).

---

## Experiment 2 — NATS JetStream consumer lag spike

**Criticality:** high (verifies constitutional invariant #2 —
ingest/eval separation; this is the load-bearing experiment for the
"append-only ledger between stages" architecture)

**Hypothesis.** When the evaluation consumer on the
`evidence.evaluations` NATS JetStream subject is paused, evidence
**ingest** continues at baseline rate. The ledger absorbs new records
durably. Eval-stage backlog grows linearly with input rate. On consumer
resume, the backlog drains without data loss.

**Steady state.** Ingest rate at baseline (10 ingest/s synthetic);
evaluation latency (push → eval-complete) P95 < 2s; NATS stream
`evidence.evaluations` consumer-pending count < 50.

**Variable.** NATS JetStream durable consumer state: paused vs active.
Perturb via `nats consumer pause <stream> <consumer>` (NATS CLI v0.1.5+)
or via direct admin RPC call.

**Method.**

1. Start docker-compose with synthetic ingest generator at 10/s.
2. Capture 5-minute steady-state metrics from OTEL.
3. Inject: pause the eval consumer
   (`nats consumer pause atlas_eval evidence-evaluator`).
4. Continue ingest. Watch:
   - `evidence.records` table row count (should keep climbing)
   - NATS consumer-pending count on `evidence.evaluations`
     (should climb linearly with ingest)
   - Push API P95 latency (should be unaffected — separation invariant)
5. Hold for 10 minutes (build backlog of ~6000 messages).
6. Resume consumer; measure drain time.

**Blast radius.** Local docker-compose NATS + atlas containers only.
NATS JetStream durable consumer state survives container restarts, so
the experiment is recoverable even if the host crashes mid-test (the
stream retains pending messages).

**Abort criteria.** Push API P95 > 5s sustained > 60s (this is the
falsification signal: if the push API slows when eval is paused, the
separation invariant is broken). OR ingest backlog exceeds 1000
messages while consumer is _not_ paused (signals an independent eval
failure, not the experiment).

**Expected outcome.**

- Push API latency: **unchanged** from baseline (this is the
  falsification check — any change falsifies the separation claim).
- Ledger growth: linear with ingest rate, no gap, no duplicate
  receipts.
- On resume: consumer drains at >= 50 msg/s (it should outrun input
  since input is only 10/s); zero messages dropped (durable consumer
  contract).

**Rollback.** `nats consumer resume atlas_eval evidence-evaluator`.
If the consumer has issues resuming, redeliver from the durable
position via `nats consumer info` and `nats stream replay`. Worst
case: `docker-compose down -v` (acceptable — this is the test env).

**Pre-execution checklist.**

- [ ] Confirm the durable consumer's `ack_wait` is configured to a
      value greater than 10 minutes (else messages may redeliver
      mid-pause and confuse the experiment).
- [ ] Snapshot `evidence.evaluations` consumer config BEFORE pausing.
- [ ] Confirm synthetic-ingest generator uses fresh tenant /
      idempotency keys so failed-eval rerun semantics are clean.

**Execution-deferral note.** Deferred to slice **355**
(`docs/issues/355-nats-consumer-lag-execution.md`). v2+ work because
it requires the synthetic-ingest generator (slice 332's load-test
script) to be stable enough to drive baseline traffic without itself
being the experiment's noise floor.

---

## Experiment 3 — Postgres primary unavailable

**Criticality:** medium (no invariant directly; tests error-shape
discipline)

**Hypothesis.** When the Postgres primary becomes unavailable, the
platform returns structured 5xx responses (not stack traces, not raw
pgx errors) within 5 seconds of request arrival. The `/healthz`
endpoint flips to a degraded state. No request hangs indefinitely.

**Steady state.** All API endpoints return 2xx for valid requests;
`/healthz` returns `{status: "ok"}`; pool reports healthy connections.

**Variable.** Postgres container running vs stopped, perturbed via
`docker-compose stop postgres`.

**Method.**

1. Start docker-compose; verify steady state.
2. Inject: `docker-compose stop postgres`.
3. For the next 60 seconds, fire synthetic requests:
   - `GET /v1/anchors` (read)
   - `POST /v1/evidence:push` (write)
   - `GET /healthz` (health)
4. Capture: status code, latency, response body shape, OTEL trace.
5. Restart postgres (`docker-compose start postgres`); measure
   recovery time.

**Blast radius.** Local docker-compose postgres container. Other
containers continue to run (atlas, NATS, minio).

**Abort criteria.** Any request hangs > 30 seconds with no response
(this signals a missing timeout — itself a bug, but flagged as abort
not as success). OR atlas container crashes (should _not_ crash on
DB unavailability — the experiment falsifies the "graceful
degradation" claim if it does).

**Expected outcome.**

- API responses: 503 with body
  `{error: "database_unavailable", retry_after: 5}` within 5s.
- `/healthz`: returns 503 with degraded sub-status field within 1s
  (since the health-check should probe the DB).
- No stack traces in response bodies.
- After postgres restarts: full recovery within 30s; no orphaned
  connections.

**Rollback.** `docker-compose start postgres`. If the atlas container
hung on connection-retry loops, restart it too.

**Pre-execution checklist.**

- [ ] Confirm no in-flight evidence pushes that would orphan
      (idempotency key registry should make this safe; verify).
- [ ] Snapshot `evidence_records` row count BEFORE; verify identical
      AFTER (no partial-write recovery state).

**Execution-deferral note.** Deferred to slice **356** (bundled with
experiment 5 as "data-tier outage chaos round 1" —
`docs/issues/356-data-tier-outage-chaos-round-1.md`). Bundled because
both experiments use `docker stop` as the injection mechanism and
both verify error-shape discipline at the API boundary.

---

## Experiment 4 — OIDC IdP unavailable

**Criticality:** medium (verifies AS-as-OIDC-RP graceful degradation
claim; resolves part of OQ #21 — slice 187+ work)

**Hypothesis.** When the external OIDC IdP becomes unreachable,
**existing** JWT sessions continue to authenticate successfully for
the remainder of their TTL. **New** logins fail with a user-friendly
error (not a stack trace). The atlas-issued JWT key-rotation is
unaffected (the AS-as-issuer surface is independent of the
AS-as-RP surface).

**Steady state.** Existing JWTs (issued before injection) return 2xx
on protected endpoints; new logins via `/oauth/authorize` succeed via
the IdP; key-rotation cron runs without errors.

**Variable.** Network egress from atlas to IdP URL, perturbed via
`iptables -A OUTPUT -d <idp-ip> -j DROP` (or via docker-compose network
isolation: detach atlas from the network that reaches the simulated
IdP container).

**Method.**

1. Start docker-compose with a containerized IdP (e.g., Dex / Keycloak)
   on the same network.
2. Mint a JWT via normal flow; capture it.
3. Verify the JWT works on a protected endpoint.
4. Inject: detach IdP container from the network or drop egress.
5. Test:
   - Existing JWT on protected endpoint: should still succeed (key
     verification is local; no IdP roundtrip).
   - New login attempt via `/oauth/authorize`: should fail with a
     friendly error.
   - Atlas-issued JWT key-rotation: should continue to work (uses
     local keystore).
6. Restore network; verify new logins resume.

**Blast radius.** Local docker-compose network only. The atlas
keystore is at `/var/lib/security-atlas/keys` — a volume — so the
experiment cannot corrupt key material.

**Abort criteria.** Existing JWT verification fails (this falsifies
the design claim — local key verification should never depend on the
IdP). OR atlas crashes on IdP-unreachable (signals a missing timeout
on the OIDC discovery refresh).

**Expected outcome.**

- Existing sessions: continue to work for TTL remaining.
- New logins: 503 with body
  `{error: "auth_provider_unavailable", retry_after: 30}`.
- Key-rotation cron: continues, no error log.
- After restore: new logins resume within 30s (OIDC discovery
  refresh interval).

**Rollback.** Re-attach IdP container or restore `iptables` rule.

**Pre-execution checklist.**

- [ ] Use a containerized IdP (Dex). Do NOT target a real external
      IdP — the experiment's chaos must not affect any production
      identity surface.
- [ ] Have an active JWT minted BEFORE injection; record its `exp`
      claim for the verification step.

**Execution-deferral note.** Deferred to slice **357** (bundled
with experiments 6 and 8 as "auth-substrate chaos round 1" —
`docs/issues/357-auth-substrate-chaos-round-1.md`). Bundled because
all three verify fail-closed-vs-fail-open discipline on auth
surfaces and share decision shape.

---

## Experiment 5 — atlas-edge container restart mid-evidence-push

**Criticality:** medium (verifies slice 003 Evidence SDK retry +
idempotency-key claim)

**Hypothesis.** When the atlas container restarts during an active
evidence-push, the SDK's retry-with-backoff (slice 003) re-sends the
record. The idempotency key prevents duplicate ledger entries. No
evidence is lost. The client perceives one successful push, even
though the wire-level call was retried.

**Steady state.** Synthetic SDK client pushes records at 1/s for 60
seconds; ledger row count grows by 60; receipts contain unique
record IDs.

**Variable.** atlas container running vs restarting, perturbed via
`docker-compose restart atlas`.

**Method.**

1. Start docker-compose; SDK client pushing 1/s.
2. After 10 seconds, inject: `docker-compose restart atlas` (this
   takes ~5-10 seconds during which the container is unreachable).
3. SDK client should observe transient errors, backoff, retry.
4. Continue pushing through second 60.
5. Count rows in `evidence_records`: should equal 60 (the client's
   counter) — NOT 60 + retried-duplicates and NOT < 60 (lost).

**Blast radius.** atlas container restart only — no DB / NATS /
minio impact.

**Abort criteria.** SDK client crashes on transient error (signals
missing retry, falsifies the SDK contract). OR ledger row count >
60 (duplicate writes — falsifies the idempotency claim).

**Expected outcome.**

- SDK client: emits warning logs during the gap, retries succeed.
- Ledger: row count exactly 60 after the test.
- All receipts have unique `record_id`.
- Recovery is bounded by the SDK's backoff schedule (currently 1s,
  2s, 4s, 8s with jitter per slice 003 spec).

**Rollback.** None needed — restart was the injection AND its own
recovery.

**Pre-execution checklist.**

- [ ] Confirm SDK client uses idempotency keys (slice 003 standard).
      If not, the duplicate-write expectation changes.
- [ ] Use a fresh tenant scope so prior runs don't pollute the
      count.

**Execution-deferral note.** Deferred to slice **356** (bundled with
experiment 3 as "data-tier outage chaos round 1" —
`docs/issues/356-data-tier-outage-chaos-round-1.md`). Bundled because
the injection mechanism is `docker restart` / `docker stop` in both.

---

## Experiment 6 — Cosign signing key absent at audit-export time

**Criticality:** medium (verifies "platform refuses to export
unsigned bundles" claim — load-bearing for audit-binding-artifact
integrity)

**Hypothesis.** When the cosign signing key is absent or unreadable
at audit-export time, the platform refuses the export with a clear
error. It does NOT export an unsigned bundle that looks signed. It
does NOT crash. The error is structured and contains no key path
disclosure.

**Steady state.** Audit-export endpoint succeeds and returns a
cosign-signed bundle; the bundle verifies against the configured
public key.

**Variable.** Cosign private key file readability, perturbed via
`chmod 000 /var/lib/security-atlas/cosign.key` (in the atlas
container) OR moving the key file aside.

**Method.**

1. Start docker-compose; perform a baseline audit-export; verify
   signature.
2. Inject: `docker-compose exec atlas chmod 000 /var/lib/security-atlas/cosign.key`
   (or `mv` the key aside).
3. Trigger audit-export.
4. Capture: status code, response body, log lines.

**Blast radius.** atlas container only; key file is restored by
rollback step. Audit-export endpoint is a tenant-scoped read of
the ledger — no cross-tenant or external impact.

**Abort criteria.** Atlas crashes on key-unreadable (falsifies
"clean error" claim). OR an export bundle is produced (falsifies
the refusal claim — should never happen).

**Expected outcome.**

- HTTP 500 with body
  `{error: "signing_unavailable", correlation_id: "...", contact: "ops"}`.
- Log line at error level: `audit_export: signing key not readable`
  (no file path disclosed in the response; path may appear in log).
- No partial bundle artifact left in object storage.

**Rollback.** `chmod 600 /var/lib/security-atlas/cosign.key` (or
restore the file).

**Pre-execution checklist.**

- [ ] Backup the cosign key before chmod (in case the rollback
      `chmod` is forgotten and a later test needs it).

**Execution-deferral note.** Deferred to slice **357** (bundled with
experiments 4 and 8 as "auth-substrate chaos round 1" —
`docs/issues/357-auth-substrate-chaos-round-1.md`).

---

## Experiment 7 — Schema-registry unavailable

**Criticality:** medium (verifies "ingest fails fast on unknown
evidence_kind" claim — load-bearing for ledger-quality invariant)

**Hypothesis.** When the schema registry is unavailable, evidence
push for an `evidence_kind` whose JSON Schema is not in the local
hot-cache returns 503 with a structured error. The push is NOT
silently accepted with an unvalidated payload. Cached schemas
continue to work (hot-cache decouples from registry availability
for known kinds).

**Steady state.** Push for a known `evidence_kind` (cached) succeeds;
push for an unknown `evidence_kind` returns 400 with
`{error: "evidence_kind_not_found"}`.

**Variable.** Schema-registry process availability, perturbed via
container stop (if schema registry is a separate process per slice 068) OR via in-process flag (if embedded).

**Method.**

1. Verify the platform's current schema-registry shape (per
   slice 068 / 088): is it an in-process Go service or a separate
   container? The injection mechanism differs.
2. Start docker-compose; push one known-kind record; verify
   success.
3. Inject: stop the registry process (or set the embedded flag to
   fail).
4. Push a known-kind record (should succeed — cached).
5. Push an unknown-kind record (should 503 with structured error
   indicating registry unavailable, NOT 400 / accepted).
6. Restore the registry; verify unknown-kind push transitions back
   to 400.

**Blast radius.** Schema-registry surface only.

**Abort criteria.** Unknown-kind push returns 2xx (falsifies the
fail-fast claim). OR known-kind push fails (falsifies the
hot-cache decoupling).

**Expected outcome.**

- Known-kind push: 200 / 202 (cache hit serves it).
- Unknown-kind push during outage: 503 with body
  `{error: "schema_registry_unavailable", correlation_id: "..."}` —
  distinguishable from 400 `{error: "evidence_kind_not_found"}`.
- After restore: unknown-kind push transitions to 400 (the actual
  kind-not-found error).

**Rollback.** Restart the registry process / container.

**Pre-execution checklist.**

- [ ] Identify the schema-registry's current deployment shape
      (in-process vs containerized) — slice 068 documents the v1
      shape; later slices may have changed it.
- [ ] Choose a known-kind that has been pushed at least once in the
      current process lifetime (else hot-cache may not have it).

**Execution-deferral note.** Deferred to slice **358**
(`docs/issues/358-schema-registry-chaos-execution.md`). Standalone
because the injection mechanism is registry-shape-specific and the
verification distinguishes hot-cache vs cold-miss behavior, which
warrants its own decisions log.

---

## Experiment 8 — OPA decision-engine timeout

**Criticality:** high (verifies fail-closed authz discipline —
constitutional posture for security-product GRC platform)

**Hypothesis.** When the OPA policy evaluation exceeds its configured
timeout, the authz decision fails closed (denies the request). The
endpoint returns 403, not 500, and not 200. Audit logs capture the
timeout. No user gains unintended access via a timeout race.

**Steady state.** Protected endpoint returns 2xx with valid JWT +
authorized scope; returns 403 with valid JWT + unauthorized scope.

**Variable.** OPA policy evaluation runtime, perturbed via a
slow-policy injection: load a Rego rule that intentionally sleeps
(`time.parse_ns_format` + comparison loop) or use OPA's built-in
trace mode at a verbosity that exceeds the timeout.

**Method.**

1. Start docker-compose; baseline policy evaluation.
2. Inject: hot-reload a Rego rule that includes a known-slow
   computation OR mock the OPA library's `Eval` call to sleep
   longer than the configured timeout (requires a test build of
   atlas — see deferral note).
3. Fire requests on the protected endpoint with a valid JWT.
4. Capture: status code, latency, audit log entries.
5. Restore the original policy; verify behavior returns to
   baseline.

**Blast radius.** atlas container only; the bad policy is
hot-reloaded so existing requests in flight are unaffected.

**Abort criteria.** Endpoint returns 2xx during the slow-policy
window (falsifies fail-closed — direct authz bypass). OR endpoint
returns 500 (signals an unhandled timeout error path).

**Expected outcome.**

- During slow-policy window: 403 returned within timeout + 100ms;
  audit log emits a `policy_eval_timeout` event with
  correlation_id, principal, requested resource.
- After policy restore: endpoint returns to 2xx for authorized
  requests.

**Rollback.** Hot-reload the original Rego policy; verify behavior
restored.

**Pre-execution checklist (HIGH-RISK — flag for executor).**

- [ ] Confirm OPA is the embedded Go library (per `CLAUDE.md`
      tech-stack table). If OPA has been promoted to a sidecar in
      a later slice, the injection mechanism changes (container
      stop instead of in-process slow-policy).
- [ ] Have the original policy file content saved to a known path
      before hot-reload.
- [ ] Confirm no production-style data in the docker-compose env
      (the experiment exercises the authz path; in principle should
      not leak data, but pre-flight the dataset).

**Execution-deferral note.** Deferred to slice **357** (bundled with
experiments 4 and 6 as "auth-substrate chaos round 1" —
`docs/issues/357-auth-substrate-chaos-round-1.md`). v2+ work because
the injection requires either a test-build of atlas with an
injectable OPA stub OR a hot-reload primitive that does not yet
exist — both touch the executable surface that slice 335 forbids.

---

## Cross-experiment observations

### Pattern: fail-closed vs fail-fast is the dominant discipline

Six of the eight experiments (1, 3, 4, 6, 7, 8) test some form of
"the platform fails _correctly_ under dependency failure." The
project's resilience claim is **not** "the platform stays available
under any failure" — it is "the platform fails in a recognizable,
non-data-corrupting way." That is a more honest claim and easier to
verify.

### Pattern: the ledger is the resilience anchor

Three experiments (1, 2, 5) verify ledger-related guarantees
(append-only durability, ingest/eval separation, idempotency under
restart). The ledger is the constitutional anchor; its resilience
properties carry the most weight if any of them falsify.

### Pattern: error-shape is itself a resilience claim

Experiments 3, 4, 6, 7, 8 all verify that the error returned under
failure is _structured_ and _non-leaky_ (no stack traces, no file
paths in response bodies, no internal error codes). The error shape
is a security boundary; the chaos experiments expose its
sufficiency.

### Pattern: docker-compose is the v1 blast radius

All eight experiments target docker-compose local. No experiment
targets atlas-edge or hosted. The v2 execution slices will inherit
this constraint and the future-v3 work of "chaos in atlas-edge
staging" is its own design problem (it needs traffic-shadowing,
canary-namespacing, and SLO budget gates — not in scope for any
spillover slice filed by 335).

### Pattern: tooling stance is "start with docker, add framework only if needed"

Experiments 1, 2, 4, 6, 7 can be executed with shell + docker +
optional `iptables`. Only experiment 8 (OPA timeout) is awkward
without a framework's policy-hotload primitive — and even there, a
test-build of atlas with a `--slow-policy` flag is simpler than
introducing chaos-mesh. The first execution slice that finds
itself reaching for a framework should file the framework-adoption
decision as its own slice (per slice 335 anti-criterion P0-335-6).

### High-risk experiments (per AC-3)

Two experiments are flagged as high-risk for the executor:

1. **Experiment 1 (DB pool exhaustion)** — connection-storming
   adjacent containers may share the postgres instance in atypical
   docker-compose configurations. Mitigation: pre-execution
   checklist verifies single-tenant scope and a `docker-compose
down -v` is staged as hard reset.
2. **Experiment 8 (OPA timeout)** — the slow-policy hot-reload
   primitive does not yet exist; introducing it via a test-build
   touches the auth-critical-path. Mitigation: the executing slice
   (357) must include an additional reviewer per the JUDGMENT-slice
   discipline.

### Deferred — needs additional infrastructure (per AC-5)

Several resilience claims are NOT covered by the eight experiments
because they require infrastructure beyond local docker-compose:

| Claim                                                  | Why deferred                                                                                                         | Future-v3 slice         |
| ------------------------------------------------------ | -------------------------------------------------------------------------------------------------------------------- | ----------------------- |
| "S3 unavailable mid-evidence-push for large artifacts" | Needs S3-compatible failure injection; minio in docker-compose handles bounded test but not real-S3 outage semantics | TBD                     |
| "Atlas-edge zone outage failover"                      | Needs multi-zone atlas-edge deployment                                                                               | TBD                     |
| "Postgres replica lag breaks read-after-write"         | Needs replicated Postgres topology (v1 is single-primary)                                                            | TBD                     |
| "Cross-tenant RLS holds during DB partial-failure"     | Needs DB partial-failure simulation framework (e.g., pgbouncer with kill-9 on specific connection-ids)               | TBD                     |
| "Sustained 10x ingest spike"                           | Needs perf-test infrastructure beyond synthetic-traffic generator                                                    | Composes with slice 332 |
| "Cosign keystore transit corruption"                   | Needs Sigstore-transparency-log integration (v3 per `CLAUDE.md` tech-stack)                                          | TBD                     |

These are recorded here so the v2 execution slices do not
accidentally try to verify a claim that the eight experiments
cannot reach.

### Top-three by criticality

The three experiments most worth executing first:

1. **Experiment 2 (NATS consumer lag)** — directly verifies
   constitutional invariant #2 (ingest/eval separation). If this
   falsifies, the entire two-stage architecture is in question.
2. **Experiment 1 (DB pool exhaustion)** — directly verifies
   constitutional invariant #3 (ledger durability) under realistic
   resource pressure.
3. **Experiment 8 (OPA timeout)** — verifies fail-closed authz.
   Authz is the security-product surface that the GRC platform
   itself depends on; a bypass here falsifies the project's
   own security posture.

The auth-substrate bundle (slice 357) carries experiments 4, 6,
and 8 — meaning the bundle slice carries the third-most-critical
experiment plus two medium-criticality ones, making it the highest
single-slice value-density in the spillover set.

---

## Cross-references

- Slice **027** (walkthrough recording) per AC-6 — the operator-
  facing manual runbook surface; chaos experiments documented here
  should each ship with a walkthrough recording when their
  executing slice lands. The walkthroughs serve as a teaching
  artifact for incident-response game days.
- Slice **332** (performance audit) per AC-8 — the baseline
  steady-state values used by experiments 1 and 2 (10 req/s
  synthetic, P95 < 100ms reads) come from the slice 332 load-test
  parameters. The executing slices should re-derive from slice
  332's current baseline if it has shifted.
- Slice **003** (Evidence SDK push) — provides the retry-with-
  backoff surface used by experiment 5. The SDK contract is the
  resilience claim being verified.
- Slice **068** (schema-registry evidence_kind fix) — defines the
  schema-registry shape used by experiment 7.
- Slice **187+** (OAuth AS / OIDC RP work) — defines the auth-
  substrate shape used by experiments 4 and 8.

---

## Spillover slot summary

| Spillover slot | Title                           | Experiments           | Status                |
| -------------- | ------------------------------- | --------------------- | --------------------- |
| 354            | DB pool exhaustion execution    | Exp 1                 | filed, deferred-to-v2 |
| 355            | NATS consumer lag execution     | Exp 2                 | filed, deferred-to-v2 |
| 356            | Data-tier outage chaos round 1  | Exp 3, 5 (bundled)    | filed, deferred-to-v2 |
| 357            | Auth-substrate chaos round 1    | Exp 4, 6, 8 (bundled) | filed, deferred-to-v2 |
| 358            | Schema-registry chaos execution | Exp 7                 | filed, deferred-to-v2 |

Five spillover slices, eight experiments — bundle ratio honors the
slice 335 cap-at-5 anti-criterion.

---

## Closing note

This design ships. The eight experiments are NOT executed in this
slice. The spillover slices 354-358 carry the execution forward as
v2+ work. Future contributors picking up any spillover slice should
re-read this design document and the per-slice notes alongside any
shift in the production architecture (especially OPA deployment
shape and schema-registry deployment shape, which the experiments
depend on).
