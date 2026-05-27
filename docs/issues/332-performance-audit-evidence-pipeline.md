# 332 — Performance audit (evidence pipeline + UCF + frontend) via voltagent-qa-sec:performance-engineer

**Cluster:** Performance
**Estimate:** 2d
**Type:** JUDGMENT
**Status:** `ready`

## Narrative

Runs `voltagent-qa-sec:performance-engineer` against the four
load-bearing performance surfaces in security-atlas. The v1 binary
success test ("does the user run their next SOC 2 out of this?")
implicitly requires the platform to feel fast — a GRC tool that takes
30 seconds to load the dashboard, or that times out on an evidence
push from a busy CI run, fails the diligence-the-diligence-tool
thesis on day two.

**Audit surface.** Performance pass across four load-bearing paths:

- **Evidence ingest pipeline.** From `POST /v1/evidence:push` → through
  `IngestEvidence` validation → schema-registry lookup → NATS JetStream
  publish → durable consumer fan-out → eval-engine evaluation →
  freshness UPSERT → drift snapshot. Target characteristic: sustained
  throughput under realistic connector load (slice 004 AWS S3 inspect
  produces a batch every 6 hours per scope cell — multi-tenant
  multi-scope means the actual rate per atlas-edge node can be
  ~10s-100s of pushes/sec).
- **UCF graph traversal.** Slice 008 measured 5.89ms mean two-hop
  JOIN at slice-006-data scale. The current graph has grown
  (additional crosswalks per slices 007 + 010 + others). Re-measure
  P50/P95/P99 against current data + with realistic concurrency.
- **Scope evaluation.** `effective_scope(control, framework) =
applicability_expr ∩ framework_scope.predicate` (canvas §5.5). This
  runs on every control-state evaluation in `internal/eval`. Heavy
  use of recursive CTEs + JSONB containment. Identify P95 query
  plans.
- **Freshness UPSERT + drift query.** Slice 016 introduced the
  four-policy RLS UPSERT on `evidence_freshness` + the daily
  worst-cell snapshot on `control_drift_snapshots`. Re-measure at
  current data volume.
- **Next.js bundle size + LCP / CLS.** The frontend has grown to ~30+
  routes. Bundle size, initial-page LCP, CLS during data loads — all
  measurable. Slice 277's mobile-responsive work introduced bundle
  changes that should be sanity-checked.
- **NATS JetStream backpressure.** Slice 015 introduced the buffer +
  ingestion stage. Under sustained-rate evidence push, does the
  consumer keep up? What's the max in-flight depth before consumer
  lag triggers operator concern?

**Why now:** none of the above have been re-measured systematically
since their original slice's release. Performance drift accumulates
silently in a codebase this size. Catching it pre-v1-binary-test
beats catching it during a SOC 2 customer's first sample-pull.

**Trigger:** Surfaced 2026-05-27 during the agent-driven audit-planning
session — audit slice 6/12.

**Disposition:** read-only performance audit + follow-up-slice fan-out.

## Threat model

Perf-audit-only slice. STRIDE pass:

- **S (Spoofing):** No auth surface. CLEAN.
- **T (Tampering):** Read-only — no production data writes, but the
  audit will GENERATE synthetic load against a local dev environment.
  AC enforces local-only.
- **R (Repudiation):** Findings logged in
  `docs/audit-log/332-performance-audit-evidence-pipeline-decisions.md`.
- **I (Information disclosure):** Performance findings include query
  plans + table sizes + concurrency observations. None are
  customer-confidential. CLEAN.
- **D (Denial of service):** **Load-bearing.** The audit RUNS load —
  it's a self-inflicted DoS vector if mis-targeted. AC enforces:
  local docker-compose only, never against atlas-edge, never against
  production.
- **E (Elevation of privilege):** Dev-level access.

## Acceptance criteria

- [ ] **AC-1.** The `voltagent-qa-sec:performance-engineer` agent
      runs against the six performance surfaces in the narrative
      against a local docker-compose deployment with demo seed data.
- [ ] **AC-2.** For each surface, the decisions log records: P50 ·
      P95 · P99 · max sustained rate · concurrency level tested ·
      observed regression vs original-slice baseline (where the
      original slice published a number).
- [ ] **AC-3.** Significant regressions (>2× from baseline OR
      operator-visible: >500ms P95 on any UI-blocking query) fan out
      as individual `/idea-to-slice` follow-up slices.
- [ ] **AC-4.** Minor regressions (1-2× baseline, no operator visibility)
      bundled into a single "perf cleanup round 1" slice OR per-surface
      individual slices — engineer's call.
- [ ] **AC-5.** Healthy surfaces documented in the decisions log
      with current numbers as the new baseline.
- [ ] **AC-6.** **The bundle-size + LCP / CLS check** uses the same
      method as a future operator (Lighthouse against the running dev
      server). Method documented for reproducibility.
- [ ] **AC-7.** No production data, no atlas-edge load tests, no
      hosted-tenant load. Local docker-compose only. AC-8 enforces.
- [ ] **AC-8.** The audit's load-generation parameters (concurrency,
      duration, rate) are documented in the decisions log so future
      audits can re-run with the same parameters.
- [ ] **AC-9.** No code modified. Diff = doc files only.
- [ ] **AC-10.** `pre-commit run --files` passes.

## Constitutional invariants honored

- **Ingestion and evaluation are separated stages (invariant #2).**
  The pipeline audit verifies the separation holds under load —
  evaluation never blocking ingest, ingest never starving evaluation.
- **Survive third-party security review (canvas §6).** Performance is
  a security property: a system that grinds to a halt under load is
  trivially DoS-able.

## Canvas references

- `Plans/canvas/04-evidence-engine.md` — pipeline architecture
- `Plans/canvas/03-ucf.md` — graph traversal semantics
- `Plans/canvas/05-scopes.md` — scope evaluation surface
- `Plans/canvas/09-tech-stack.md` — performance-relevant stack choices
  (sqlc, recursive CTEs, NATS JetStream)

## Dependencies

- **#004** (AWS connector) — `merged`. Realistic-rate generator.
- **#008** (UCF graph traversal API) — `merged`. Original baseline:
  5.89ms mean two-hop.
- **#012** (control state evaluation engine) — `merged`. Eval stage.
- **#015** (NATS JetStream ingestion) — `merged`. Buffer stage.
- **#016** (evidence freshness + drift) — `merged`. UPSERT + snapshot
  surfaces.

## Anti-criteria (P0 — block merge)

- **P0-332-1.** Does NOT run load against atlas-edge, hosted tenants,
  production, or any non-local environment. Local docker-compose
  only.
- **P0-332-2.** Does NOT run load against production tenant data —
  demo seed only.
- **P0-332-3.** Does NOT bundle major regressions (>2× baseline OR
  operator-visible) into one follow-up slice. One regression = one
  tracer-bullet slice.
- **P0-332-4.** Does NOT auto-merge.
- **P0-332-5.** Does NOT modify code.
- **P0-332-6.** Does NOT introduce a new perf-testing framework as a
  dependency — use stock tools (go test -bench, hey, k6 if already
  available, Lighthouse).
- **P0-332-7.** Does NOT touch CLAUDE.md, canvas.

## Skill mix

- `voltagent-qa-sec:performance-engineer` — the named audit agent
- `/idea-to-slice` — for follow-ups
- `go test -bench` for Go-side micro-benchmarks
- `hey` or equivalent for HTTP load
- `psql EXPLAIN ANALYZE` for query plans
- Lighthouse for frontend

## Notes for the implementing agent

**Surface ordering suggestion:**

1. **Evidence ingest pipeline first.** Highest impact on operator
   experience — slow push = connector backoff = operator pages
   themselves at 2am.
2. **UCF graph traversal second.** Slice 008's 5.89ms baseline is the
   easiest concrete number to compare against.
3. **Scope evaluation third.** Heavy CTE + JSONB — likely to have
   the most-interesting query plans.
4. **Freshness UPSERT + drift fourth.** Recent work; likely fine but
   worth re-measuring.
5. **NATS backpressure fifth.** Requires sustained load — most
   time-consuming.
6. **Frontend bundle + LCP last.** Cheapest to measure; needs a
   running dev server which the load tests already require.

**Baseline lookup.** Each original slice should have a perf
characteristic in its decisions log (slice 008's "5.89ms mean
two-hop JOIN" is the canonical example). If the original slice
didn't publish a number, document the new measurement as the
baseline-of-record going forward.

**Concurrency parameters suggestion.** The 50-150-person customer
profile per CLAUDE.md drives the realistic concurrency model:

- Evidence push: ~10-100 RPS sustained (a busy CI org)
- UCF queries: ~1-5 RPS sustained (mostly cached at the UI)
- Scope eval: ~1 RPS sustained (background eval-engine ticks)
- Frontend page load: 1 RPS per logged-in user, ~10-50 concurrent

**Do NOT max out the dev host.** The audit's value is the
characterization curve (P50/P95/P99 vs load), not "how high can it
go". Aim for realistic loads + measure quality of service, not
saturation.

**Audit log filename:**
`docs/audit-log/332-performance-audit-evidence-pipeline-decisions.md`
