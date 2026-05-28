# 335 — Chaos experiment design — decisions log

**Slice:** 335
**Date:** 2026-05-28
**Branch:** `quality/335-chaos-experiment-design`
**Primary artifact:** `docs/audits/335-chaos-experiment-design.md`

This log captures the per-experiment design decisions made during slice
335 and the spillover slots filed for v2+ execution. The primary
artifact is the audit document; this log is the audit-binding decisions
trail per slice 335 AC-2.

---

## Decision D1 — Persona interpretation

**Decision.** The `voltagent-qa-sec:chaos-engineer` persona was applied
as a methodology adoption (style + structure), not as a sub-agent
invocation. The primary engineer wrote the experiment designs directly,
following the persona's hypothesis · steady-state · blast-radius · abort
discipline.

**Rationale.** Slices 333 and 334 set the precedent by adopting their
respective `voltagent-qa-sec` personas as methodology rather than as
spawned subagents. Adopting the same shape here matches established
project convention and avoids a sub-agent coordination cost on a
single-author design slice.

---

## Decision D2 — Field union over slice-doc subset

**Decision.** Each experiment includes both the **user-prompt-requested
fields** (Hypothesis · Steady-state · Variable · Method · Expected
outcome · Rollback · Execution-deferral note) AND the **slice-doc
AC-2-requested fields** (hypothesis · steady state · blast radius ·
abort criteria · execution method · expected runtime).

**Rationale.** The two requirement sets overlap substantially. Including
the union (rather than picking one) produces a complete chaos-
engineering design without orphaning either set of acceptance criteria.
The redundancy cost is small; the future-execution clarity is high.

---

## Decision D3 — Five spillover slots via bundling

**Decision.** Eight experiments → five spillover slots:

| Slot | Bundles                                                | Reason                                                                                                  |
| ---- | ------------------------------------------------------ | ------------------------------------------------------------------------------------------------------- |
| 354  | Exp 1 (DB pool exhaustion)                             | Standalone — high criticality, distinct injection mechanism                                             |
| 355  | Exp 2 (NATS consumer lag)                              | Standalone — verifies invariant #2, distinct from data-tier                                             |
| 356  | Exp 3 (Postgres down) + Exp 5 (atlas restart mid-push) | Bundled — both use `docker stop`/`restart` as injection; both verify error-shape under data-tier outage |
| 357  | Exp 4 (OIDC) + Exp 6 (cosign) + Exp 8 (OPA)            | Bundled — all three test fail-closed-vs-fail-open on auth substrate; share decision-tree shape          |
| 358  | Exp 7 (schema-registry)                                | Standalone — schema-registry-shape-specific injection; warrants own decisions log                       |

**Rationale.** The anti-criterion P0-335-3 says "does NOT bundle
experiment-execution slices — each experiment design = one execution-
deferral slice." The user prompt softens this to "cap at 5 — bundle
related experiments." Honoring the user-prompt cap and bundling on
**injection-mechanism similarity** and **fail-closed-vs-fail-open
discipline** produces the cleanest spillover. The slice-doc anti-
criterion is acknowledged but superseded by the explicit user-prompt
instruction to cap at 5 with bundling permitted.

---

## Decision D4 — Tool stance: name but do not commit

**Decision.** The Method field of each experiment names viable injection
tools (docker, `iptables`, chaos-mesh, litmus, gremlin, AWS FIS) but
does NOT commit to any framework. No tool is added as a runtime or dev
dependency by this slice.

**Rationale.** Anti-criterion P0-335-6 forbids framework introduction
without a separate slice. The named tools are documentation of viable
mechanisms; the actual choice is deferred to the executing slice. Most
experiments can be executed with shell + docker alone — no framework
needed.

---

## Decision D5 — Local docker-compose only

**Decision.** All eight experiments scope to local docker-compose. None
targets atlas-edge, hosted tenants, or production.

**Rationale.** Anti-criterion P0-335-2 enforces this. The executing
slices (354-358) inherit the constraint via cross-reference back to 335. Production / atlas-edge chaos is a future-v3 problem requiring
traffic-shadowing and SLO-budget gates — out of scope for any
spillover here.

---

## Decision D6 — High-risk flagging (AC-3)

**Decision.** Experiments 1 (DB pool exhaustion) and 8 (OPA timeout)
are flagged as high-risk. Each has an additional pre-execution
checklist item or reviewer requirement encoded in the audit doc and
restated in the executing slice.

**Rationale.**

- Exp 1: connection-storming adjacent containers in atypical
  docker-compose configurations could cause unintended impact.
- Exp 8: the slow-policy hot-reload primitive does not yet exist;
  introducing it touches the auth-critical-path. The executing slice
  carries an extra-reviewer requirement.

The other six experiments are normal-risk for the docker-compose env.

---

## Decision D7 — Deferred-with-infrastructure section (AC-5)

**Decision.** The audit doc includes a dedicated section listing
resilience claims that the eight experiments CANNOT verify within
slice 335's scope, with the missing infrastructure named per claim.

**Rationale.** AC-5 explicitly requires it. Surfacing the gap honestly
prevents v2 executing slices from accidentally attempting verification
that needs infra beyond local docker-compose.

---

## Decision D8 — Top-three criticality

**Decision.** The three highest-criticality experiments are surfaced in
the audit doc's "Top-three by criticality" subsection:

1. Experiment 2 — NATS consumer lag (invariant #2)
2. Experiment 1 — DB pool exhaustion (invariant #3)
3. Experiment 8 — OPA timeout (fail-closed authz posture)

**Rationale.** Each verifies a load-bearing claim — two are
constitutional invariants, one is the project's own security posture.
The ordering informs v2 executor prioritization.

---

## Decision D9 — No code modified (AC-7 / P0-335-5)

**Decision.** Diff = doc files only. Verified post-write via `git
status`.

**Rationale.** Slice anti-criterion. Verified.

---

## Per-experiment summary table

| Exp | Title                  | Invariant tested                   | Spillover slot | Criticality | High-risk? |
| --- | ---------------------- | ---------------------------------- | -------------- | ----------- | ---------- |
| 1   | DB pool exhaustion     | #3 (append-only ledger)            | 354            | high        | yes        |
| 2   | NATS consumer lag      | #2 (ingest/eval separation)        | 355            | high        | no         |
| 3   | Postgres primary down  | (error-shape discipline)           | 356            | medium      | no         |
| 4   | OIDC IdP unavailable   | (auth graceful degradation)        | 357            | medium      | no         |
| 5   | atlas restart mid-push | (SDK retry + idempotency)          | 356            | medium      | no         |
| 6   | Cosign key absent      | (audit-binding refusal)            | 357            | medium      | no         |
| 7   | Schema-registry down   | (ingest fail-fast on unknown kind) | 358            | medium      | no         |
| 8   | OPA timeout            | (fail-closed authz posture)        | 357            | high        | yes        |

---

## Acceptance criteria status

| AC   | Status    | Evidence                                                                                           |
| ---- | --------- | -------------------------------------------------------------------------------------------------- |
| AC-1 | satisfied | 8 experiment designs in audit doc, no execution attempted                                          |
| AC-2 | satisfied | This decisions log exists at `docs/audit-log/335-chaos-experiment-design-decisions.md`             |
| AC-3 | satisfied | High-risk experiments 1 and 8 carry pre-execution checklists in the audit doc                      |
| AC-4 | satisfied | Five spillover slices filed (354-358) per D3 bundling decision                                     |
| AC-5 | satisfied | "Deferred — needs additional infrastructure" section in the audit doc lists six unreachable claims |
| AC-6 | satisfied | Slice 027 cross-reference in the audit doc                                                         |
| AC-7 | satisfied | `git status` shows doc-only diff                                                                   |
| AC-8 | satisfied | Slice 332 cross-reference in the audit doc                                                         |
| AC-9 | satisfied | `pre-commit run --files` passes on all touched files                                               |

---

## Spillover slot summary (filed slices)

| Slot | File path                                            | Experiments | Status                 |
| ---- | ---------------------------------------------------- | ----------- | ---------------------- |
| 354  | `docs/issues/354-db-pool-exhaustion-execution.md`    | Exp 1       | ready (deferred-to-v2) |
| 355  | `docs/issues/355-nats-consumer-lag-execution.md`     | Exp 2       | ready (deferred-to-v2) |
| 356  | `docs/issues/356-data-tier-outage-chaos-round-1.md`  | Exp 3, 5    | ready (deferred-to-v2) |
| 357  | `docs/issues/357-auth-substrate-chaos-round-1.md`    | Exp 4, 6, 8 | ready (deferred-to-v2) |
| 358  | `docs/issues/358-schema-registry-chaos-execution.md` | Exp 7       | ready (deferred-to-v2) |
