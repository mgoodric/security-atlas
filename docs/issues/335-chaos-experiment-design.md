# 335 — Chaos experiment design via voltagent-qa-sec:chaos-engineer

**Cluster:** Resilience
**Estimate:** 1.5d
**Type:** JUDGMENT
**Status:** `ready`

## Narrative

Runs `voltagent-qa-sec:chaos-engineer` against security-atlas to
**design** (not execute) chaos experiments targeting the platform's
load-bearing dependencies. Chaos engineering is the discipline of
deliberate failure-injection to verify resilience claims; this slice
captures the experiment-design artifact so future v2+ slices can
execute the experiments against atlas-edge / hosted environments.

The product makes implicit resilience claims:

- "Ingestion and evaluation are separated stages" (invariant #2) →
  the system should keep ingesting evidence even when evaluation is
  down
- "Append-only evidence ledger" (invariant #3) → the ledger should
  remain readable even under DB connection-pool exhaustion
- "Multi-tenant isolation via RLS at the DB layer" (invariant #6) →
  isolation should hold under chaos
- "OIDC IdP-relying" auth design → the platform should gracefully
  degrade when the IdP is unavailable (not all-or-nothing)

Each claim is a chaos hypothesis. This slice designs the experiments
that verify (or falsify) each.

**Audit surface.** Chaos experiment designs for:

- **Evidence ledger DB connection pool exhaustion.** Hypothesis:
  the ledger remains readable + slow-fails writes when the pool is
  saturated. Steady state: P95 evidence-read < 100ms. Blast radius:
  one local docker-compose tenant. Abort criteria: read P95 > 5s
  for > 60s.
- **NATS JetStream consumer lag spike.** Hypothesis: the
  ingest-evaluation separation holds — ingest continues even when
  the eval consumer is paused. Steady state: ingest rate continues
  at baseline. Blast radius: pause one consumer; do not touch the
  durable subscription. Abort: ingest backlog > 1000 messages.
- **Postgres primary unavailable.** Hypothesis: the platform fails
  cleanly with a recognizable error (not a stack trace to the user).
  Steady state: 5xx responses with structured error payload. Blast
  radius: docker-compose stop the postgres container.
- **OIDC IdP unavailable.** Hypothesis: existing JWT sessions
  continue to work for the remainder of their TTL; new logins fail
  with a user-friendly message. Steady state: existing JWT requests
  succeed. Blast radius: block egress to the IdP URL.
- **atlas-edge container restart mid-evidence-push.** Hypothesis:
  the SDK retry-with-backoff (slice 003) handles the gap; no
  evidence is lost. Steady state: push succeeds after retry. Blast
  radius: docker restart atlas container during a synthetic push.
- **Cosign signing key absent at audit-export time.** Hypothesis:
  the platform refuses to export (rather than exporting an unsigned
  bundle that looks signed). Steady state: clean error message.
- **Schema-registry unavailable.** Hypothesis: ingest fails-fast
  on unknown evidence_kind rather than accepting unvalidated payload.
  Steady state: 400 with kind-not-found error.
- **OPA decision-engine timeout.** Hypothesis: authz failures fail
  closed (deny). Steady state: 403 on protected endpoint.

**Why now:** the platform has resilience claims that have never been
systematically verified. Designing the experiments now means the v2
execution slices can pick up a ready spec — and even before
execution, the design surfaces architectural assumptions that may
turn out not to hold.

**Trigger:** Surfaced 2026-05-27 during the agent-driven audit-planning
session — audit slice 9/12.

**Disposition:** read-only experiment-design audit + follow-up-slice
fan-out for execution.

## Threat model

Design-only slice. STRIDE pass:

- **S (Spoofing):** No auth surface. CLEAN.
- **T (Tampering):** Read-only — design artifact only, NO experiment
  execution in this slice. AC-1 enforces.
- **R (Repudiation):** Designs logged in
  `docs/audit-log/335-chaos-experiment-design-decisions.md`.
- **I (Information disclosure):** Chaos experiment designs document
  failure modes — same Information-disclosure consideration as slice
  329 / 333. Document failure modes at architecture level, not
  exploit level.
- **D (Denial of service):** **Load-bearing.** Chaos experiments
  by definition introduce failures. The design phase is safe;
  execution is the failure-injection event. AC-1 enforces
  design-only-no-execution.
- **E (Elevation of privilege):** Dev-level access.

## Acceptance criteria

- [ ] **AC-1.** The `voltagent-qa-sec:chaos-engineer` agent produces
      experiment designs for the eight failure scenarios in the
      narrative. **DESIGN ONLY — NO EXPERIMENT EXECUTION IN THIS
      SLICE.**
- [ ] **AC-2.** Each experiment design recorded in
      `docs/audit-log/335-chaos-experiment-design-decisions.md` with
      the Chaos Engineering standard structure: hypothesis · steady
      state · blast radius · abort criteria · execution method
      (manual command or scripted runner) · expected runtime.
- [ ] **AC-3.** **High-risk experiments** (those that touch shared
      state or could affect adjacent dev environments) flagged with
      explicit pre-execution checklist for the v2 execution slice.
- [ ] **AC-4.** For each experiment, a follow-up slice is filed via
      `/idea-to-slice` deferring execution to v2. The follow-up
      slice's slot is appended to the design entry. **The
      follow-up slices are NOT executed in this session.**
- [ ] **AC-5.** Resilience claims that the audit cannot verify
      (e.g. "what does the platform do if AWS S3 is unavailable
      mid-evidence-push?" requires a separate-AWS-account
      experiment) are documented as "deferred — needs infrastructure
      X".
- [ ] **AC-6.** Cross-references slice 027 (walkthrough recording —
      provides the manual-runbook surface for chaos execution).
- [ ] **AC-7.** No code modified. Diff = doc files only.
- [ ] **AC-8.** Cross-references slice 332 (performance audit) — the
      perf audit's load-test parameters inform the chaos experiments'
      steady-state characterization.
- [ ] **AC-9.** `pre-commit run --files` passes.

## Constitutional invariants honored

- **Ingestion and evaluation are separated stages (invariant #2).**
  The chaos experiments verify this claim under failure.
- **Append-only evidence ledger (invariant #3).** A chaos experiment
  explicitly verifies the ledger survives DB pool exhaustion.
- **Tenant isolation via RLS (invariant #6).** A chaos experiment
  verifies isolation holds under partial DB failure.

## Canvas references

- `Plans/canvas/04-evidence-engine.md` §4.3 — separation of stages
- `Plans/canvas/09-tech-stack.md` — failure-mode-relevant stack
  choices (NATS JetStream durable consumers, sqlc pool sizing,
  cosign key locations)

## Dependencies

- **#003** (Evidence SDK push) — `merged`. Retry-with-backoff
  surface for the SDK chaos experiment.
- **#015** (NATS JetStream) — `merged`. Consumer lag experiment
  surface.
- **#027** (walkthrough recording) — `merged`. Manual-runbook
  surface.

## Anti-criteria (P0 — block merge)

- **P0-335-1.** Does NOT execute any chaos experiment in this
  slice. Design-only. AC-1 enforces.
- **P0-335-2.** Does NOT design experiments that target atlas-edge,
  hosted tenants, or production. Local docker-compose only.
- **P0-335-3.** Does NOT bundle experiment-execution slices —
  each experiment design = one execution-deferral slice.
- **P0-335-4.** Does NOT auto-merge.
- **P0-335-5.** Does NOT modify code.
- **P0-335-6.** Does NOT introduce a chaos-engineering framework as
  a runtime dependency — use existing tools (docker, shell scripts,
  k6 if available). A framework-introduction decision is a separate
  slice if proposed.
- **P0-335-7.** Does NOT touch CLAUDE.md, canvas.

## Skill mix

- `voltagent-qa-sec:chaos-engineer` — the named audit agent
- `/idea-to-slice` — for execution-deferral follow-ups
- Standard read/grep — surface enumeration

## Notes for the implementing agent

**Design structure (per Chaos Engineering principles):**

```
### Experiment: <name>

**Hypothesis:** <falsifiable claim about steady-state behavior>
**Steady state:** <metric + threshold that defines normal>
**Blast radius:** <what fails + scope of failure>
**Abort criteria:** <metric + threshold that signals abort>
**Execution method:** <command(s) to execute>
**Expected runtime:** <duration>
**Pre-execution checklist:** <prerequisites>
**Verification:** <how to confirm the hypothesis holds or fails>
```

**Game day vs runbook framing.** Each design should be runnable as
either an interactive game day (operator-driven, narrative-style)
OR as a scripted runner (CI-friendly). Document both modes if
applicable.

**Cross-reference protocol.** Resilience claims that surface from
chaos experiments but are too big to verify with a single
experiment (e.g. "the platform handles arbitrary IdP unavailability")
get filed as multiple follow-up slices (one per IdP failure mode).

**Audit log filename:**
`docs/audit-log/335-chaos-experiment-design-decisions.md`
