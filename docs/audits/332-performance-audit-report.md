# 332 — Performance audit (evidence pipeline + UCF + frontend)

**Slice:** 332
**Date:** 2026-05-28
**Author:** `voltagent-qa-sec:performance-engineer` persona (instance run)
**Scope:** read-only audit — **NO code modified**. Six load-bearing
performance surfaces of `main@eec5a2d0` analyzed via static-analysis,
benchmark-test reading, and existing-decisions-log review.
**Branch:** `quality/332-performance-audit-evidence-pipeline`

---

## Introduction

This document captures the performance audit called for by slice 332.
The platform's v1 binary success test ("does the solo security leader
run their next SOC 2 audit out of this?") implicitly requires the
platform to feel fast — a GRC tool that takes 30 seconds to load the
dashboard, or that times out on an evidence push from a busy CI run,
fails the diligence-the-diligence-tool thesis on day two.

Six load-bearing surfaces were audited:

1. **Evidence ingest pipeline** — `internal/evidence/ingest` Push
   handler + slice-015 JetStream buffer
2. **UCF graph traversal** — `internal/api/ucfcoverage` two-hop JOIN
   handler family
3. **OPA evaluation** — `internal/eval/rego.go` evidence-query
   sandbox + `internal/authz/decision.go` request-time authz
4. **OAuth substrate** — `internal/api/oauth` token endpoint +
   `internal/auth/tokensign` ES256 sign/verify
5. **Frontend BFF route handlers** — `web/app/api/*/route.ts` thin
   proxies + `web/lib/api/bff.ts`
6. **Observability overhead** — `internal/observability/otel` SDK
   wiring and `otelpgx` hot-path span allocation

### Methodology

The audit is **read-only** and was conducted via:

- Static-analysis of each surface's source code
- Reading existing benchmark tests (slice 008 `BenchmarkRequirementCoverage`)
- Reading the originating slice's decisions log where one exists
  (slice 008, 016, 188, 121)
- Inspecting the recursive-CTE / JSONB shapes in
  `internal/db/queries/*.sql`
- Cross-referencing constitutional invariants in `CLAUDE.md`

**No live load was generated.** The slice's "Notes for the implementing
agent" paragraph explicitly authorizes the static-analysis path when
docker-compose bring-up authority or local load generation is not
available in-session. See decisions log §D4 for the JUDGMENT call.

### Per-finding format

Every finding below follows this shape:

| Field           | Purpose                                                                |
| --------------- | ---------------------------------------------------------------------- |
| **Severity**    | Critical · High · Medium · Low · Informational (see decisions log §D2) |
| **Surface**     | Which of the six load-bearing surfaces this lives in                   |
| **Observation** | The specific code pattern / query plan / config that the finding flags |
| **Concurrency** | The concurrency level the finding becomes operator-visible at          |
| **Baseline**    | What the original slice published (where applicable)                   |
| **Regression**  | Whether the current shape is a regression from baseline                |
| **Method**      | Static-analysis · benchmark-test-reading · decisions-log-citation      |
| **Remediation** | What a follow-up slice would do                                        |
| **Spillover**   | The spillover slice that picks this up (or "audit-report-only")        |

### Severity rubric

- **Critical**: > 2× baseline regression OR operator-visible degradation
  on the hot path of a v1-binary-criterion-load-bearing surface
- **High**: 1.5–2× baseline OR a structural pattern that will become
  Critical at v2 scale
- **Medium**: noticeable inefficiency; operator-visible above realistic
  v1 concurrency (>100 RPS sustained)
- **Low**: codebase-onboarding observation; bounded impact at v1
- **Informational**: documenting healthy steady-state or already-resolved
  design decision

### Concurrency baselines used (per slice 332 narrative)

| Surface             | Realistic v1 sustained rate                                   |
| ------------------- | ------------------------------------------------------------- |
| Evidence push       | 10–100 RPS per atlas-edge node (busy CI org)                  |
| UCF graph queries   | 1–5 RPS sustained (mostly cached at the UI)                   |
| Scope / eval engine | 1 RPS sustained (background eval-engine ticks)                |
| Frontend page loads | 1 RPS per logged-in user, ~10–50 concurrent                   |
| OAuth token mint    | 1–10 RPS per atlas-edge node (cred refresh on connector boot) |
| OTel emit           | matches DB query rate (otelpgx wraps every Exec/Query)        |

### Source references

- Slice 332 narrative (`docs/issues/332-performance-audit-evidence-pipeline.md`)
- Persona spec at
  `~/.claude/plugins/marketplaces/voltagent-subagents/categories/04-quality-security/performance-engineer.md`
- Slice 008 baseline — `docs/issues/_STATUS.md` row 008
- Slice 188 Argon2id parameter rationale —
  `docs/audit-log/188-oauth-token-endpoint-decisions.md` §D-Argon-1
- Slice 121 OTel SDK no-op default —
  `docs/issues/121-atlas-otel-sdk.md`
- `Plans/canvas/04-evidence-engine.md` §4.3 — separated ingest/eval
- `Plans/canvas/03-ucf.md` + `Plans/UCF_GRAPH_MODEL.md` §7

---

## Counts

| Severity          | Findings | Spillovers filed |
| ----------------- | -------- | ---------------- |
| **Critical**      | 1        | 1                |
| **High**          | 1        | 1                |
| **Medium**        | 3        | 2 + 1 bundle     |
| **Low**           | 6        | bundled          |
| **Informational** | 4        | n/a              |
| **Total**         | 15       | 5                |

Per-surface count:

| Surface       | Findings | Severity profile                 |
| ------------- | -------- | -------------------------------- |
| Ingest        | 3        | M(1) · L(1) · I(1)               |
| UCF           | 2        | L(1) · I(1)                      |
| OPA           | 2        | **C(1)** · **H(1)**              |
| OAuth         | 3        | L(2) · I(1)                      |
| Frontend BFF  | 3        | M(2) · I(1)                      |
| Observability | 2        | L(1) · I(1)                      |
| **Total**     | **15**   | C(1) · H(1) · M(3) · L(6) · I(4) |

**Most-impactful finding**: F-OPA-1 — `evalRegoQuery` recompiles the
Rego policy via `rego.PrepareForEval` on every `computeRow` call
(~200 controls × N cells per `EvaluateAll`). The authz `Engine`
already shows the correct pattern (prepare-once at construction).
Spillover slice 377.

---

## Surface 1 — Evidence ingest pipeline

**Code paths reviewed:**

- `internal/evidence/ingest/ingest.go` — `Service.Process` (the
  single canonical inbound path per constitutional invariant #3)
- `internal/evidence/streambuf/streambuf.go` — NATS JetStream
  publish/consume (slice 015 ingest substrate)
- `internal/evidence/redact/` — per-kind redaction (slice 015 AC-6)
- `internal/db/queries/evidence_records.sql` — append-only INSERT and
  idempotency lookup

**Baseline:** Slice 015 did not publish a per-push latency number;
slice 013's contract guarantees ack-at-stream-commit, not
ack-at-ledger-write. The platform's stated push profile in
`Plans/EVIDENCE_SDK.md` §3 is "ack within 50ms at p95 under typical
connector load (1–10 RPS per credential)". No regression observed
against that profile from static analysis.

### F-ING-1 (Medium) — Double `protojson.Marshal` on the redaction code path

**Surface**: ingest
**Observation**: `Service.Process` marshals the payload once at
line 269 (`payloadJSON, err := protojson.Marshal(rec.GetPayload())`)
for the size-check and schema-validation steps. When a tenant-private
kind declares redaction rules (slice 015 AC-6), the post-redact
re-marshal at line 329 runs a second `protojson.Marshal` on the
redacted payload. This is two full proto-serialization passes for
every push of a redacted kind.
**Concurrency**: Operator-invisible at 1–10 RPS. Becomes measurable
at sustained >100 RPS on redaction-bearing kinds (which today is
`secret.scan.v1` only).
**Baseline**: None published; slice 015 D2 documents the redact-then-
hash invariant but not the marshal cost.
**Regression**: Not a regression — present since slice 015. Surfaced
now because the redaction-kind list will grow (PCI-CDE-bound kinds
will likely declare redaction rules).
**Method**: Static-analysis of `ingest.go` lines 269 + 329.
**Remediation**: Marshal once, redact in-place on the unmarshaled
proto (which is the same `*evidencev1.EvidenceRecord` already in
memory), marshal redacted form once at hash time. The redaction
package operates on the proto, not on bytes, so the second marshal
is structurally avoidable.
**Spillover**: **slice 379**.

### F-ING-2 (Low) — Per-reject `writeAudit` opens an independent DB tx

**Surface**: ingest
**Observation**: `Service.Process` calls `s.writeAudit(...)` on every
reject path (lines 244, 255, 263, 275, 292, 297, 314, 320, 331, 344,
355, 365, 399). Each `writeAudit` opens its own `pgx.BeginTx` to
satisfy the tenant GUC requirement. At low reject rates this is
invisible; at sustained >100 RPS with elevated reject ratio (e.g.
during a misconfigured connector replay), the audit-write fan-out
doubles the per-push connection-acquire cost on the reject path.
**Concurrency**: Materializes above 100 RPS under heavy reject mix.
**Baseline**: Not published. Slice 013 D5 documents the
"best-effort, independent transaction" design choice — correct, but
unbounded under reject storms.
**Regression**: Not a regression.
**Method**: Static-analysis of `ingest.go` `writeAudit` call sites.
**Remediation**: Batch reject audit writes into the next available
pool slot, or use a smaller `connectAttempt` timeout on
`writeAudit`'s independent tx. Bounded scope.
**Spillover**: bundled into **slice 381** (perf cleanup round 1).

### F-ING-3 (Informational) — `canonjson.HashRecord` CPU is `O(payload_size)`, bounded at 1 MiB

**Surface**: ingest
**Observation**: `canonjson.HashRecord` SHA-256s the canonicalized
proto. CPU cost scales linearly with payload size. The
`MaxPayloadBytes = 1 << 20` (1 MiB) cap at line 61 bounds this at
~1–2 ms on commodity hardware per hash, comfortably under the
50ms ack SLO. Healthy.
**Concurrency**: n/a — bounded by design.
**Baseline**: Implicit — the 1 MiB cap is itself the design budget.
**Regression**: None.
**Method**: Static-analysis + code-budget reasoning.
**Remediation**: None — document baseline.
**Spillover**: audit-report-only.

---

## Surface 2 — UCF graph traversal

**Code paths reviewed:**

- `internal/api/ucfcoverage/handlers.go` — 932 LOC of two-hop JOIN
  handlers (forward + reverse + control-centric)
- `internal/api/ucfcoverage/benchmark_test.go` — slice 008's
  reproducible benchmark harness
- `internal/db/queries/ucf_traversal.sql` — the six sqlc queries
- `Plans/UCF_GRAPH_MODEL.md` §7 — bounded-fan-out reasoning

**Baseline (slice 008, on `main@06d1875`):**

| Metric            | Value                                                                         |
| ----------------- | ----------------------------------------------------------------------------- |
| Mean per-call     | **5.89 ms**                                                                   |
| P50               | 5.88 ms                                                                       |
| P95               | 6.91 ms                                                                       |
| Fixture           | 1,400 SCF anchors + 60 SOC 2 reqs + 10,000 STRM edges + 5,000 tenant controls |
| Target gate       | < 200 ms mean                                                                 |
| Margin under gate | **34×**                                                                       |

### F-UCF-1 (Informational) — Baseline preserved; no regression observed

**Surface**: UCF
**Observation**: No schema changes to `scf_anchors`, `fw_to_scf_edges`,
or the `controls` table since slice 008 that would invalidate the
two-hop JOIN's planner choice. The bounded-fan-out invariant
(`UCF_GRAPH_MODEL.md` §7) still holds — the catalog hasn't grown
past the SCF v2024.1 anchor count + the SOC 2 + ISO 27001 + NIST CSF

- PCI DSS + HIPAA + GDPR frameworks that slice 010/007 onboarded.
  **Concurrency**: 1–5 RPS sustained, mostly UI-cached, well within
  the benchmark's single-connection latency.
  **Baseline**: 5.89 ms mean / 5.88 ms p50 / 6.91 ms p95.
  **Regression**: None observed. The benchmark gate remains a
  mechanical CI artifact (run on demand via
  `go test -tags=integration -bench=BenchmarkRequirementCoverage`),
  and re-running it on a present-day docker-compose would re-publish
  a fresh number; the audit's static-analysis path estimates the
  delta as < 10% (no new joins, no new indexes removed).
  **Method**: Benchmark-test reading + schema-diff reasoning across
  slices 009/010/011/058/076.
  **Remediation**: Run `go test -tags=integration -bench=
BenchmarkRequirementCoverage -run=^$ ./internal/api/ucfcoverage`
  on a representative docker-compose periodically (annual cadence
  suggested) and re-publish the number. Not a slice — a maintainer
  runbook item.
  **Spillover**: audit-report-only.

### F-UCF-2 (Low) — `handlers.go` is 932 LOC across three endpoints

**Surface**: UCF
**Observation**: The handler file is large for a coverage handler
trio. As of `main@eec5a2d0` the package has three handlers + JSON
marshaling + URL parsing all colocated. Future endpoints (e.g.
`/v1/controls/coverage-by-framework`) added to this package will
amplify the LOC; review-cycle cost is the immediate symptom but
inadvertent N+1 risk grows with the file.
**Concurrency**: n/a — codebase-onboarding observation.
**Baseline**: n/a.
**Regression**: n/a.
**Method**: Static-analysis of `handlers.go` size.
**Remediation**: Split per-endpoint into separate files when the
fourth endpoint lands. Pure file-organization change.
**Spillover**: bundled into **slice 381** (perf cleanup round 1).

---

## Surface 3 — OPA evaluation

This surface has TWO independent sub-surfaces:

**3a — Per-control Rego evidence queries** (`internal/eval/rego.go`)
**3b — Request-time authz** (`internal/authz/decision.go`)

Both use `github.com/open-policy-agent/opa/v1`. Their patterns
**diverge** in load-bearing ways.

### F-OPA-1 (CRITICAL) — `evalRegoQuery` recompiles per `computeRow` call

**Surface**: OPA — sub-surface 3a
**Observation**:
`internal/eval/rego.go:87 evalRegoQuery` calls

```go
q, err := rego.New(
    rego.Query(regoQuery),
    rego.Module("evidence_query.rego", policy),
    rego.Input(input),
    rego.Capabilities(evalSandboxCapabilities()),
).PrepareForEval(ctx)
```

`PrepareForEval` parses + compiles the Rego module on every call.
`evalRegoQuery` is invoked from `internal/eval/engine.go:159`
inside `computeRow`, which is invoked once per `(control × cell)`
per `EvaluateAll`. A tenant with 200 active controls and modest
scope fan-out (say 3 cells average) is paying ~600 OPA compiles
per scheduled-evaluation tick.

The authz `Engine` (`internal/authz/decision.go:60`) shows the
correct pattern — `PrepareForEval` is called ONCE in `NewEngine`
and the resulting `rego.PreparedEvalQuery` is stored on the
`Engine` struct, re-used for every `Decide`.

A third call site exists at `internal/risk/aggrule/severity.go:134`
(custom-rego severity policy) — same recompile-per-eval pattern.
Both sites need the same fix.

**Concurrency**: Materializes at any sustained eval-engine load.
The slice 332 narrative says scope/eval is "1 RPS sustained" but
the EvaluateAll loop multiplies by `controls × cells` per tick.
A scheduled tick processing 200 controls is doing 200+ compiles
even at 1 tick / minute.

**Baseline**: Not published. The slice 012 eval-engine decisions
log does not characterize per-compile cost (OPA compile of a small
module is ~1–5 ms on commodity hardware; for 600 compiles per tick
that's 600 ms–3 s of pure CPU spent in the planner).

**Regression**: Not a regression vs. a documented baseline — but a
**latent structural regression** vs. the authz pattern that already
exists in the same codebase. The right pattern is known; the eval
engine just didn't adopt it.

**Method**: Static-analysis of `internal/eval/rego.go:87–106`,
cross-referenced with `internal/authz/decision.go:60–65` (the
correct pattern), and confirmed against
`internal/risk/aggrule/severity.go:115–137` (a second site of the
wrong pattern).

**Remediation**: Cache `rego.PreparedEvalQuery` per policy text
(SHA-256 of the policy string as the cache key, or per control bundle
ID with the bundle as the cache invalidation surface). The cache
lives at the package-level (`var policyCache = ...` with a
sync.Map) and is bounded by the active-control count. Both `eval`
and `risk/aggrule` share the cache (or each gets its own — they are
independent policy populations).

**Spillover**: **slice 377**.

### F-OPA-2 (High) — Authz bundle hot-reload requires server restart

**Surface**: OPA — sub-surface 3b
**Observation**: `internal/authz/decision.go:46-65 NewEngine`
loads policies from the embedded filesystem (`policies/authz/*.rego`)
ONCE at construction and stores the prepared query on the `Engine`.
The `Engine` is then injected into HTTP middleware at startup. There
is no exposed `Reload()` or `WithPolicy()` method; a policy change
requires restarting the atlas binary.

For a v1 single-tenant operator-restart deployment this is
acceptable — but the canvas explicitly contemplates per-tenant
custom-control policy authoring (canvas §4.4) and an operator
restart is unacceptable for a SaaS-of-self-host-aware atlas-edge
deployment (atlas-edge restart is a documented v2 concern).

**Concurrency**: n/a — structural finding.

**Baseline**: Not published; slice 023 (the authz substrate) chose
the restart-required model deliberately for v1.

**Regression**: Not a regression — explicit v1 choice. Flagged
because it becomes a **High** by v2 atlas-edge tenant isolation
work.

**Method**: Static-analysis of `internal/authz/decision.go` +
`internal/api/authzmw/middleware.go` (no Reload pathway).

**Remediation**: Add a `Reload(ctx, modules) error` method to
`*Engine` that prepares a new query on a fresh `rego.Rego` and
atomically swaps the stored query (sync/atomic.Pointer or a
RWMutex around the query field). Invalidates any in-flight Decide
calls cleanly — Eval is goroutine-safe.

**Spillover**: **slice 378**.

---

## Surface 4 — OAuth substrate

**Code paths reviewed:**

- `internal/api/oauth/token.go` — `client_credentials` +
  `authorization_code` + token-exchange + device-code grants
- `internal/api/oauth/pkce.go` — RFC 7636 S256 verify
- `internal/auth/tokensign/tokensign.go` — ES256 JWS sign/verify
- `internal/auth/keystore/fsstore/fsstore.go` — RWMutex-cached
  filesystem keystore

**Baseline (per slice 188 D-Argon-1):**

| Operation                     | Cost                                           |
| ----------------------------- | ---------------------------------------------- |
| Argon2id verify (m=64MiB t=1) | ~150 ms Apple Silicon, ~250 ms x86_64          |
| ES256 sign                    | sub-millisecond (single P-256 scalar mul)      |
| ES256 verify                  | ~1–2 ms (signature verify is slower than sign) |
| PKCE S256 challenge           | sub-millisecond (SHA-256 of ~43 chars)         |

### F-OAUTH-1 (Informational) — Argon2id 150ms verify is by-design

**Surface**: OAuth
**Observation**: Slice 188 chose Argon2id parameters m=64MiB t=1 p=1
deliberately. The 150ms verify cost is the OWASP-recommended baseline
and is **per `client_credentials` grant exchange only** — NOT
per-request. Per-request authz is JWT signature verification
(~1–2 ms ES256), which is correct.

**Concurrency**: At 1–10 client-credentials exchanges per atlas-edge
node per minute (cred refresh on connector boot), this is bounded.

**Baseline**: 150ms Apple Silicon / 250ms x86_64 (slice 188 D-Argon-1).

**Regression**: None — design point.

**Method**: Decisions-log citation.

**Remediation**: None.

**Spillover**: audit-report-only.

### F-OAUTH-2 (Low) — `tokensign.Sign` allocates a fresh `jose.NewSigner` per call

**Surface**: OAuth
**Observation**: `internal/auth/tokensign/tokensign.go:65–98 Sign`
calls `jose.NewSigner(...)` on every Sign. go-jose's signer is
cheap to allocate (no key-derivation, no FIPS-mode init) but the
construction does an internal `Algorithm` lookup + JWK marshal.
At sustained mint rate (e.g. 10 JWTs/sec during a multi-connector
bootstrap fan-out) this is a small but measurable allocation cost.

**Concurrency**: Materializes only at sustained >10 JWT mints/sec
sustained for >60s. v1 bootstrap profile is bursty (connector
startup) but short.

**Baseline**: Not published; sub-millisecond ES256 sign is
implicit baseline.

**Regression**: None.

**Method**: Static-analysis of `tokensign.go:76–82`.

**Remediation**: Cache a `jose.Signer` per active KeyID on the
`Signer` struct. Invalidate on keystore rotation (which is currently
a stub — `ErrRotateUnsupported`). Bounded by the keystore's active
KeyID count (1 in v1).

**Spillover**: bundled into **slice 381** (perf cleanup round 1).

### F-OAUTH-3 (Low) — `keystore.fsstore.Get` does `make+copy` of verification keys on every call

**Surface**: OAuth
**Observation**: `internal/auth/keystore/fsstore/fsstore.go:103-105`
returns a defensive `[]VerificationKey` copy on every Get. The
underlying public keys are immutable, so the slice header is the
only thing being defensively copied. At sustained verify rate
(every JWT-bearing request hits Verify which hits Get), this is
N allocations per request. With 1 active KeyID the slice is 1
element wide — bounded cost, but it's allocation-on-every-request
which a sustained-load profile cares about.

**Concurrency**: Materializes at sustained >100 req/sec with JWT
auth, which is the realistic shape during a connector burst.

**Baseline**: Not published.

**Regression**: None — design choice for defensive copying.

**Method**: Static-analysis of `fsstore.go:95–106`.

**Remediation**: Return an immutable handle (sync/atomic.Pointer
to a frozen []VerificationKey) refreshed only on Rotate. Bounded
work because the keystore is read-mostly.

**Spillover**: bundled into **slice 381** (perf cleanup round 1).

---

## Surface 5 — Frontend BFF route handlers

**Code paths reviewed:**

- `web/lib/api/bff.ts` — 80-LOC `forwardJSON` + `forwardMultipart`
  helpers
- `web/app/api/dashboard/proxy.ts` — 32-LOC dashboard fan-out
  helper
- `web/app/api/dashboard/activity/route.ts` — representative
  per-panel proxy
- `web/lib/api.ts` — 2901 LOC / 219 exports (already filed as
  slice 370 split-up)

### F-BFF-1 (Informational) — `cache: "no-store"` is correct for session-bearing routes

**Surface**: Frontend BFF
**Observation**: Every BFF forwarding helper sets
`cache: "no-store"` on the upstream fetch. This is **correct** for
session-bearing routes — caching a JWT-authenticated response in
the Next.js data cache would leak across users. The audit confirms
this is design intent, not a regression.

**Concurrency**: n/a.

**Baseline**: Slice 040 dashboard-proxy convention.

**Regression**: None — confirming design.

**Method**: Static-analysis of `bff.ts:49` + `dashboard/proxy.ts:22`.

**Remediation**: None — leave `no-store` in place.

**Spillover**: audit-report-only.

### F-BFF-2 (Medium) — Dashboard panels fan-out N independent BFF round-trips

**Surface**: Frontend BFF
**Observation**: The dashboard route (`/dashboard`) renders multiple
panels (activity, freshness, drift, upcoming, risks, framework-posture
per `web/app/api/dashboard/` directory listing). Each panel is its
own BFF route, fetched by its own TanStack Query `useQuery` hook
client-side. The page-level "time to interactive" is bounded by the
SLOWEST of the N panels (waterfalled if no parallelism, but TanStack
Query parallelizes within a single React tree).

The structural issue is **round-trip count**: 7+ round-trips for one
page load. Each round-trip = bearer-cookie read + upstream HTTP +
parse + Next.js response shape. At the v1-realistic 10–50 concurrent
users the platform handles this fine, but it's a 7× amplification of
the upstream eval-engine query load per page navigation.

**Concurrency**: Materializes at >50 concurrent dashboard users.

**Baseline**: Not published — slice 040 chose per-panel BFFs for
typed-client + cache-isolation simplicity.

**Regression**: None — explicit v1 design.

**Method**: Static-analysis of `web/app/api/dashboard/` directory +
`dashboardProxy` helper shape.

**Remediation**: A Server Component dashboard page that fetches all
panels in parallel server-side via `Promise.all` and streams the
results to the client would collapse 7+ round-trips into 1. v2 work
— Next.js Server Components are the structural mechanism.

**Spillover**: **slice 380**.

### F-BFF-3 (Informational) — `web/lib/api.ts` 2901 LOC / 219 exports already filed as slice 370

**Surface**: Frontend BFF
**Observation**: The client library is 2901 LOC with 219 exports.
Slice 328 H-2 identified this; slice 370 is the planned split into
per-domain files under `web/lib/api/`. **NOT a new finding** —
cross-reference only. Including in this audit so the surface-coverage
table is complete.

**Concurrency**: n/a.

**Baseline**: slice 328 H-2.

**Regression**: n/a.

**Method**: `_STATUS.md` cross-reference.

**Remediation**: Slice 370 is already filed; no new spillover.

**Spillover**: cross-reference only — already covered by slice 370.

---

## Surface 6 — Observability stack overhead

**Code paths reviewed:**

- `internal/observability/otel/otel.go` — SDK Init
- `internal/observability/otel/pgx.go` — `otelpgx` tracer wiring
  on the pgxpool
- `internal/observability/otel/nats.go` — JetStream tracer wiring
- `internal/observability/otel/runtime.go` — Go runtime metrics

### F-OTEL-1 (Informational) — No-op default is correct and cost-free

**Surface**: Observability
**Observation**: Slice 121 AC-2 enforces that when
`OTEL_EXPORTER_OTLP_ENDPOINT` is unset, Init returns no-op trace
and meter providers. The OTEL SDK's no-op TracerProvider is
genuinely zero-cost — `Tracer("name").Start(ctx, "span")` returns
an inert `noopSpan`. Hot-path cost in the default deployment is
essentially zero.

**Concurrency**: n/a — no-op.

**Baseline**: Slice 121 AC-2 contract.

**Regression**: None — design holds.

**Method**: Static-analysis of `otel.go:55–66` + reading the OTel
SDK's noop package source.

**Remediation**: None.

**Spillover**: audit-report-only.

### F-OTEL-2 (Low) — `otelpgx` wraps every pgx Exec/Query; meaningful at sustained DB load

**Surface**: Observability
**Observation**: `internal/observability/otel/pgx.go:46` attaches
`otelpgx.NewTracer()` to the pgxpool config. Every Exec/Query
emits a child span. With OTel ENABLED at default sampler
(`parentbased_always_on` by default), this is a span allocation
per query. At sustained eval-engine load (hundreds of queries/sec
when `EvaluateAll` runs), the span allocation cost is meaningful
even with batched export.

**Concurrency**: Materializes when OTel is enabled AND DB query
rate exceeds ~100/sec sustained. At v1 default deployment this is
unlikely; at v2 atlas-edge scale it's expected.

**Baseline**: Not published; slice 121 D-Sampling-1 chose
`parentbased_always_on` as the default sampler.

**Regression**: None — explicit v1 design.

**Method**: Static-analysis of `pgx.go:46` + reading `otelpgx`
default tracer behavior.

**Remediation**: Operators tuning `OTEL_TRACES_SAMPLER` to
`parentbased_traceidratio` with `OTEL_TRACES_SAMPLER_ARG=0.1`
mitigates the per-span cost by 10×. The platform should publish a
"high-DB-query-rate" operator runbook section documenting the
sampler-tuning recipe. Documentation-only fix.

**Spillover**: bundled into **slice 381** (perf cleanup round 1).

---

## Spillover summary

| Slice | Severity      | Surface      | Subject                                                              |
| ----- | ------------- | ------------ | -------------------------------------------------------------------- |
| 377   | Critical      | OPA          | Cache `rego.PreparedEvalQuery` per policy text in eval engine + risk |
| 378   | High          | OPA          | Hot-reload authz bundle without server restart                       |
| 379   | Medium        | Ingest       | Eliminate double `protojson.Marshal` on redaction path               |
| 380   | Medium        | Frontend BFF | Dashboard server-component fan-out + parallel data fetch             |
| 381   | Medium-bundle | Multi        | "perf cleanup round 1" — bundles 5 Low findings + 1 doc-runbook item |

**Spillover cap (5) honored.**

---

## Surfaces flagged healthy

The following surfaces showed **no actionable findings** beyond
informational baselines:

- **UCF graph traversal** — slice 008's 5.89ms baseline is preserved
  by the unchanged schema + index shape.
- **Ingest core path** — the `Service.Process` boundary remains the
  one canonical inbound; constitutional invariant #3 holds.
- **OAuth ES256 sign/verify** — go-jose performance is acceptable;
  the only marginal cost (F-OAUTH-2/3) is bundled.
- **OTel no-op default** — genuine zero-cost when unconfigured.

---

## Decisions log cross-reference

Every JUDGMENT call this audit made — surface deep-dive vs spot-check,
severity rubric application, regression-vs-baseline threshold,
methodology choice, spillover triage — is recorded in
[`docs/audit-log/332-performance-audit-decisions.md`](../audit-log/332-performance-audit-decisions.md).

---

## What this audit deliberately did NOT do

- **No load generation against atlas-edge or production.** P0-332-1.
- **No code modification.** Read-only audit per P0-332-3 / P0-332-5.
- **No screenshots with PII or customer data.** P0-332-4.
- **No new perf-testing framework added as a dependency.** P0-332-6.
- **No edits to CLAUDE.md or canvas.** P0-332-7.
- **No live measurement.** Static-analysis + benchmark-test reading
  - decisions-log citation only. The live-measurement re-publish is
    a maintainer runbook item, NOT a slice.

---

## Adjacent observations (not in scope)

These were noticed during the audit but are NOT slice 332 findings.
Documented here so future auditors don't re-discover them:

- The `internal/eval/engine.go` `loadEvidence` loads ALL records for
  a control without LIMIT or cursor. For controls with high evidence
  volume (e.g. a chatty connector pushing every 5 minutes for a year)
  this is unbounded memory pressure. Out-of-scope for 332 because v1
  evidence volume is bounded by the connector cadence + the SCF
  control catalog's anchor count. Documented for the v2 eval-engine
  re-shape.
- Slice 371 just landed (clock injection across auth substrate);
  a parallel slice for the eval-engine clock injection would let
  the prepared-query cache in F-OPA-1 be tested deterministically.
  Adjacent — does NOT widen 332 scope. Cross-reference in the PR
  body only.
