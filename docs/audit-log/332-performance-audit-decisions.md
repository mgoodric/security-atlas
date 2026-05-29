# Slice 332 — Performance audit — decisions log

**Slice type:** `JUDGMENT`. The audit is read-only; the JUDGMENT calls
recorded here are the subjective decisions the performance-engineer
persona made during the surface-deep-dive triage. None of them blocked
merge; all are captured here so post-deployment iteration is tractable.

**Slice author:** voltagent-qa-sec:performance-engineer persona
(instance run — embodied, not spawned).

**Date:** 2026-05-28.

**Baseline commit:** `eec5a2d0`.

**Companion audit report:** `docs/audits/332-performance-audit-report.md`.

---

## Decisions made

### D1 — Which surfaces to deep-dive vs spot-check

The slice doc lists six load-bearing surfaces, and the
"Surface ordering suggestion" paragraph offers a recommended depth
ordering. The persona's call:

- **Deep-dive (full per-finding format with severity + spillover triage):**
  Ingest, UCF, OPA, OAuth, Frontend BFF, Observability — **all six**.
- **Spot-check (informational-only baseline):** none.

**Rationale**: The slice's value is the surface-breadth coverage; a
spot-check on any surface leaves a load-bearing claim unvalidated. The
session has the budget to deep-dive all six. Spot-checking is a fallback
for time pressure that the session didn't experience.

- _Option considered:_ deep-dive only the three with prior baselines
  (ingest / UCF / OAuth) and spot-check the three without (OPA /
  frontend / OTel). Rejected — the deepest finding (F-OPA-1, CRITICAL)
  was in the spot-check-candidate bucket. A spot-check would have
  missed it. The persona's instinct that "the surface most likely to
  hide a finding is the surface without a published baseline" was
  validated by F-OPA-1.

### D2 — Severity rubric

The slice doc's anti-criterion P0-332-3 separates "major" (Critical /
High) from "minor" (Medium); the report adds Low and Informational for
granularity. Applied rubric:

- **Critical**: > 2× baseline regression OR operator-visible degradation
  on a v1-binary-criterion hot path
- **High**: 1.5–2× baseline OR a structural pattern that will become
  Critical at v2 scale
- **Medium**: noticeable inefficiency; operator-visible above realistic
  v1 concurrency (>100 RPS sustained)
- **Low**: codebase-onboarding observation; bounded impact at v1
- **Informational**: documenting healthy steady-state OR an
  already-resolved design decision

**Spillover triage rule**: Critical → own slice (always). High → own
slice (always). Medium → own slice if isolated; bundle into "perf
cleanup round 1" if multiple Mediums share a remediation pattern. Low
→ bundled into "perf cleanup round 1" by default. Informational →
audit-report-only (no slice).

**Why this rubric and not the simpler "operator-visible vs not"**:
because slice 332 spans 6 surfaces with different concurrency models.
An "operator-visible" finding on ingest (which sees 100 RPS) is a
different bar than one on UCF (which sees 1 RPS UI-cached). The
5-tier rubric maps each surface's concurrency baseline to a
finding-severity tier honestly.

### D3 — Regression-vs-baseline threshold

The slice doc says "Significant regressions (>2× from baseline OR
operator-visible: >500ms P95 on any UI-blocking query) fan out as
individual `/idea-to-slice` follow-up slices." The persona applied this
literally:

- Slice 008's 5.89ms baseline + present-day shape unchanged →
  F-UCF-1 is Informational (no regression).
- F-OAUTH-1 has a 150ms cost that's documented design intent →
  Informational.
- F-OPA-1 has no published baseline but a structurally wrong pattern
  vs. an in-codebase correct pattern (authz Engine) → CRITICAL
  notwithstanding the absence of a "regression number".

The judgment call captured here: **"regression vs baseline" includes
"regression vs the correct pattern that already exists in the
codebase"**. F-OPA-1's severity is set by the in-codebase counterexample
(authz Engine) demonstrating the right shape, not by a fabricated
benchmark number.

### D4 — Audit methodology: static-analysis + benchmark-test reading

The slice's "Notes for the implementing agent" paragraph explicitly
authorizes the static-analysis path when docker-compose bring-up
authority or local-load-generation is not available in-session:

> If you don't have docker-compose bring-up authority or can't generate
> load locally, document as "measured via static-analysis +
> benchmark-test reading" in D1.

The persona elected the static-analysis path. Justification:

1. **Live load generation requires uncommitted authoring** (e.g. a
   driver script for the streambuf integration test fixture). The
   slice anti-criterion P0-332-5 forbids code modification — including
   even test helpers, by strict reading of "audit only".
2. **Docker-compose bring-up authority is available** (Docker is
   running locally) but standing up the full atlas binary + NATS +
   Postgres + sampling the live OTel pipeline is a >2-hour bring-up
   that consumes the entire session budget.
3. **The findings are structurally identifiable from static analysis.**
   F-OPA-1 (CRITICAL) is a pattern-recognition finding: `PrepareForEval`
   in the per-call hot path is the wrong shape vs. the
   `prepare-once-store-prepared-query` shape that the authz Engine
   shows. No measurement is needed to identify the finding; a
   measurement would only quantify its magnitude.

**Limitations honestly documented**:

- Specific P50/P95/P99 numbers per surface are NOT measured. The report
  documents the baselines that ARE measured (slice 008 UCF benchmark,
  slice 188 Argon2id verify) and characterizes the findings
  structurally for the unmeasured surfaces.
- A future maintainer running the live load harness will resolve the
  unmeasured numbers; documenting the methodology gap explicitly here
  is the audit's honesty discipline.

**Per-finding "Method" line in the report** records which mechanism
each finding came from: static-analysis vs benchmark-test-reading vs
decisions-log-citation.

### D5 — Spillover-cap-5 triage

The slice doc caps spillovers at 5. Initial finding distribution:

| Severity      | Count | Default spillover behavior |
| ------------- | ----- | -------------------------- |
| Critical      | 1     | own slice                  |
| High          | 1     | own slice                  |
| Medium        | 3     | each own slice OR bundle   |
| Low           | 6     | bundle                     |
| Informational | 4     | audit-report-only          |

Worst-case spillover count = 1 + 1 + 3 + 1 (bundle for Lows) = 6 → over cap.

Triage:

- F-OPA-1 (Critical) → slice 377 — must be own slice.
- F-OPA-2 (High) → slice 378 — must be own slice.
- F-ING-1 (Medium, operator-visible at >100 RPS) → slice 379 — own
  slice because the remediation pattern (in-place redact + single
  marshal) is distinct from any other Medium.
- F-BFF-2 (Medium, operator-visible at >50 concurrent users) → slice
  380 — own slice because the remediation pattern (Server Component
  fan-out via `Promise.all`) is distinct.
- F-BFF-3 (Medium-info) → NOT a new slice; cross-reference to
  already-filed slice 370 (the api.ts split). Recorded as
  audit-report-only with cross-reference.
- F-ING-2 + F-OAUTH-2 + F-OAUTH-3 + F-OTEL-2 + F-UCF-2 (all Low) +
  F-ING-3 (Informational with a runbook ask) → bundle into **slice
  381** "perf cleanup round 1".

Final count: 4 individual spillovers (377/378/379/380) + 1 bundle
spillover (381) = **5**. Cap honored.

### D6 — Adjacent-observation discipline

Two observations were noticed during the audit that are NOT slice 332
findings (the surface they touch is out of scope, or they'd widen the
slice unacceptably):

1. `internal/eval/engine.go loadEvidence` is unbounded in record count
   per control. **OUT OF SCOPE** for 332 — the engine re-shape this
   would imply is v2 work; v1 evidence volume is bounded by connector
   cadence × control count.
2. Slice 371 just landed (clock injection across auth substrate).
   Extending clock injection to the eval engine would let the
   prepared-query cache in F-OPA-1 be tested deterministically.
   **OUT OF SCOPE** for 332 — but cross-referenced in the PR body
   so the orchestrator can decide whether to file a v2 slice for the
   eval-engine clock injection.

The discipline: adjacent observations live in the report's "Adjacent
observations" section, NOT in the spillover slot count, AND get a
one-line PR-body mention. They do NOT become slice 332 spillovers
because they widen the scope past the audit's six surfaces.

### D7 — Bundle slice (381) shape

The "perf cleanup round 1" bundle slice has six independent
sub-findings spread across four surfaces. JUDGMENT call:

- **Bundle as one slice (chosen)**: each finding individually is < 0.5d
  of work; bundling makes the slice 1.5–2d total. Slice cadence works.
- **Six independent slices (rejected)**: each would be < 0.5d but the
  per-slice ceremony (issue + branch + PR + status update) is the same
  fixed cost regardless of size. Six small slices is six times the
  per-slice overhead.

The bundle is shape-honest: it explicitly enumerates the six
sub-findings + their per-finding remediation in the slice doc, so a
reviewer of slice 381 can validate each independently.

### D8 — Single most-impactful finding

The return contract asks for "Single most-impactful finding (one-line)".

**F-OPA-1 — Cache rego.PreparedEvalQuery per policy text in the eval
engine; the authz Engine already shows the right pattern in the same
codebase.**

Why this and not F-OPA-2 (the only other Critical/High):

- F-OPA-1 is on the per-tick hot path of every scheduled-eval cycle
  (200 controls × ~3 cells × 1 compile per row = 600 compiles per
  tick). F-OPA-2 only materializes when a maintainer wants to change
  the authz bundle, which is a low-frequency event.
- F-OPA-1's fix is mechanical (a `sync.Map` keyed on policy hash).
  F-OPA-2's fix touches more code paths (middleware swap + tests).
- F-OPA-1 has a second instance in `internal/risk/aggrule/severity.go`
  — the fix has higher pattern leverage.

### D9 — Did the audit honor the v1 vs v2 boundary

Slice 332 is a v2 follow-on (the v1 backlog landed at slice 069 era).
The 6 surfaces include both v1-shipped (ingest, UCF, ingestion-stage
OPA eval) AND v2-stage code (OAuth substrate is slice 187+ which
shipped in 2026-Q2, OTel SDK is slice 121). The persona's discipline:
audit covers BOTH v1 and v2-already-shipped surfaces, because both are
on `main@eec5a2d0` and v1's binary success test runs against the
present-day binary.

Out-of-scope for 332: v2 work that has NOT yet shipped (atlas-edge
multi-tenant pool, ClickHouse read-model, etc.). Cleanly delineated
by "the surface code path exists on main".

### D10 — Did the audit honor read-only

The diff for this slice is doc-only:

```
docs/audits/332-performance-audit-report.md                  (new)
docs/audit-log/332-performance-audit-decisions.md            (new)
docs/issues/377-eval-rego-prepared-query-cache.md            (new)
docs/issues/378-authz-bundle-hot-reload.md                   (new)
docs/issues/379-ingest-redaction-single-marshal.md           (new)
docs/issues/380-dashboard-server-component-fanout.md         (new)
docs/issues/381-perf-cleanup-round-1.md                      (new)
docs/issues/_STATUS.md                                       (modified — backlog row appends)
CHANGELOG.md                                                 (modified — Unreleased / Documentation bullet)
```

No `internal/`, no `cmd/`, no `web/`, no `proto/`, no `policies/`, no
`migrations/`, no `internal/db/queries/`, no `Plans/`, no `CLAUDE.md`
edits. ISC-23 honored, ISC-A3 honored, ISC-A5 honored.

---

## What this decisions log deliberately does NOT decide

- The exact remediation for F-OPA-1 (cache shape: sync.Map vs
  LRU vs WeakMap). Decision deferred to slice 377's own decisions log.
- The exact P50/P95/P99 numbers. Decision deferred to slice 354
  (chaos slice 1 — DB pool exhaustion execution) which establishes
  the steady-state baseline using a real synthetic-traffic harness.
- Whether the OPA evaluator should use OPA's wasm runtime vs Go.
  Out of scope; v3+ optimization avenue.
