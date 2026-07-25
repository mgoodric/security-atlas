# Slice 449 — OPA 1.4 → 1.17 embedded-engine upgrade — decisions log

**Type:** JUDGMENT
**Cluster:** Security
**Status at write time:** regression gate backfilled; PR open, not merged (P0-449-6: maintainer reviews before merge).

## Framing decision (the load-bearing one)

**D0 — The version bump already landed; this slice delivers the regression
GATE retroactively, not the bump.**

The slice was written to "supersede dependabot PR #953 and replace the
'bump + green-CI auto-merge' with a deliberate regression pass." In fact the
bump **already merged** via dependabot #953 auto-merge —
`02bdea55 deps(deps): bump github.com/open-policy-agent/opa from 1.4.0 to
1.17.0 (#953)` — BEFORE this slice ran. `go.mod` already pins
`github.com/open-policy-agent/opa v1.17.0`; `main` is green on 1.17 (slices
495 / 526 built on top); the `policies/authz/*.rego` files are already on
`rego.v1` syntax (`default allow := false`, `allow := true if { ... }`).

So AC-1 (the literal `go.mod` bump) is a no-op — it is already satisfied on
`main`. The auto-merge happened **without** the deliberate regression gate
449 called for. This slice therefore backfills that gate: the specific
fail-closed / bundle-validation / cache-correctness assertions that lock in
1.17 behavior and would catch a future silent semantics regression on this
security-critical engine. No `go.mod` re-bump, no `.rego` policy-logic
change.

Confidence: HIGH. Evidence: `grep opa go.mod` → `v1.17.0`; `go build ./...`
clean; `git log` shows `02bdea55`; all OPA-importing package unit suites
green on 1.17 before any change in this slice.

## What was already covered (no duplication)

Verified-present-on-main, so NOT re-authored:

- **AC-2 (unit suites green on 1.17):** `internal/authz`, `internal/eval`,
  `internal/eval/regocache`, `internal/api/adminauthzbundle` all green —
  confirmed before touching anything.
- **AC-3 (matrix ground-truth + regression corpus):** `matrix_validator.go`
  - `matrix_integration_test.go` + the `sliceNNN_test.go` corpus
    (025/027/124/135/142/148/156/174/196/269/270/278) — unchanged, green.
- **Empty-roles default-deny:** `decision_test.go::TestDecide_DefaultDenyEmptyRoles`.
- **Bundle compile-error + empty-modules rejection (engine level):**
  `reload_test.go::TestReload_CompileErrorRejected`,
  `TestReload_RejectsEmptyModules`, `TestReload_ValidatorFailureRejected`,
  `TestReload_RaceConcurrentDecideAndReload`.
- **Bundle matrix-failure 422 (handler level):**
  `handler_test.go::TestReload_MatrixFailureReturns422`,
  `TestValidateMatrix_ReceivesNilCandidate`.
- **AC-4 perf benchmark (already exists + still builds under 1.17):**
  `internal/eval/rego_test.go::BenchmarkEvalRegoQueryRepeatedCompile` +
  `_Uncached`. The slice 377 fast-path benchmark was not re-authored; it was
  re-run (see D4).
- **Sandbox capability stripping (network/runtime builtins fail at compile):**
  `rego_test.go::TestEvalRegoQuery_HTTPSendRejectedAtCompileTime`,
  `TestSandboxCapabilities_StripsDeniedBuiltins`.

## Assertions ADDED by this slice (the gap closure)

**D1 — STRIDE-S fail-closed (P0-449-3): added.**
`internal/authz/slice449_test.go`. The existing suite pinned empty-_roles_
but not (a) a fully zero-value subject (`authz.UserInput{}` — no id, no
roles, no attrs) across write / approve / read / upload-bundle actions, nor
(b) a malformed identity claim (non-canonical / injection-shaped /
case-variant / newline-embedded role strings), nor (c) an auditor with
`nil` attrs against a scoped sample. All three MUST deny under 1.17; all
three do. This is the closest thing to a forged/absent-identity probe the
unit tier can express without a live JWT.
Confidence: HIGH (deterministic, no services).

**D2 — regocache correctness contract (AC-4 correctness half): added.**
`internal/eval/regocache/slice449_test.go::TestSlice449_CachedEqualsFresh`.
The existing regocache tests asserted _structural_ contract (same key → same
pointer, distinct keys → distinct entries, hit/miss counts) but NOT that a
**cached** prepared query yields the **same decision** as a **fresh
uncached** prepare under 1.17. Added a fresh-vs-cached oracle over a spread
of control-eval inputs (all-pass / one-fail / empty-records), asserting
equality AND that the fast-path was actually hit (`Snapshot().Hits > 0`).
This is the correctness complement to the perf benchmark.
Confidence: HIGH.

**D3 — STRIDE-T bundle-validation under 1.17 (P0-449-4): added.**
`internal/authz/slice449_bundle_test.go`. Two cases the threat model names
that the existing reload tests did not pin explicitly: (1) a
**syntactically malformed** bundle is rejected at `ast.ParseModule` under
1.17 and the prior bundle stays installed (SHA unchanged, admin-write still
allowed); (2) an **oversized** (2000 inert rules) but valid-syntax
**permissive** bundle (`default allow := true`) is rejected by the
production `ValidateMatrix` BEFORE the atomic swap — fail-closed: SHA
unchanged, viewer-write still denied. The oversized case doubles as a
P0-449-1 elevation guard (no blanket-allow smuggled past the matrix).
Confidence: HIGH.

## D4 — regocache perf number under 1.17 (AC-4, measured)

Re-ran the slice 377 benchmark on 1.17 (`-benchtime 200ms`, 28-core host):

| Path                                                                   | ns/op       | B/op    | allocs/op |
| ---------------------------------------------------------------------- | ----------- | ------- | --------- |
| `BenchmarkEvalRegoQueryRepeatedCompile` (cached, slice-377 fast-path)  | **31,664**  | 19,582  | 242       |
| `BenchmarkEvalRegoQueryRepeatedCompile_Uncached` (pre-#377 re-prepare) | **200,907** | 175,738 | 3,466     |

**Speedup under 1.17: ~6.35× (200,907 / 31,664).** Slice 377 claimed ~7×;
this is the same order of magnitude — the prepared-query fast-path is intact
under 1.17, no cache-invalidation regression. Per the project's fuzz/perf
flake discipline, the perf number is **documented here, not asserted as a
hard wall-clock gate** (a flaky ns/op threshold on a shared CI runner is
exactly the kind of false-fail the project rejects). The HARD gate is the
**correctness** assertion in D2 (cached == fresh) plus the existing
benchmark continuing to _build and run_ (AC-4 "benchmark re-run shows the
fast-path is still hit").

## D5 — Rego-semantics diff verdict (AC-6)

No forced `rego.v1` migration was required in this slice: the policies were
ALREADY on `rego.v1` on `main` (the #953 auto-merge era predates this slice,
and the policies migrated separately). The `rego` package API the project
uses — `rego.New(rego.Query, rego.ParsedModule/Module, rego.Store/Capabilities)`
→ `PrepareForEval(ctx)` → `Eval(ctx, rego.EvalInput(...))` — compiles and
behaves identically under 1.17 for atlas's two query shapes:

1. **authz:** `data.authz.allow` over the JWT-derived input document
   (`toRegoInput`) — verified behavior-preserving by the full matrix corpus
   (AC-3) + the new fail-closed assertions (D1).
2. **control-eval:** `data.evidence.query.result` over `{records: [...]}` in
   the capability-restricted sandbox — verified behavior-preserving by the
   existing `rego_test.go` suite + the new cached-equals-fresh oracle (D2).

No evaluation-semantics change across 1.5..1.17 altered an outcome for
atlas's actual query shapes. P0-449-1 (no outcome change) and P0-449-2 (no
`.rego` logic change) both hold — zero `.rego` files were touched.

## Detection-tier classification

- detection_tier_actual: none
- detection_tier_target: none

No bug surfaced during the slice. The OPA 1.17 bump was already green on
`main`; this slice adds regression _assertions_ that all pass on first run.
The assertions are the safety net for a _future_ silent semantics regression
(target tier for such a regression would be `unit` for the fail-closed /
cache-correctness checks and `integration` for the matrix corpus) — none was
present to catch here.

## CI-parity results

- `go build ./...` — clean.
- `go test ./internal/authz/... ./internal/eval/... ./internal/api/adminauthzbundle/...` — green (unit).
- `go test -race ...` on the same packages — green.
- `go test -tags=integration -run xxxNoMatch ...` — integration tier compiles.
- `gofmt -l` — clean (after `gofmt -w`); `go vet` — clean;
  `golangci-lint run` on touched packages — `0 issues.`
- `pre-commit run --all-files` — see PR (CHANGELOG bullet added).

## Anti-criteria status

- P0-449-1 (no outcome change): HELD — zero `.rego` changes; matrix corpus unchanged.
- P0-449-2 (no `.rego` logic change except forced migration): HELD — no `.rego` touched; no migration forced.
- P0-449-3 (fail-closed asserted under 1.17): CLOSED — D1.
- P0-449-4 (bundle-validation not relaxed): CLOSED — D3.
- P0-449-5 (no silent perf regression): HELD — D4 documents 6.35×; correctness gated (D2), perf documented not flaky-asserted.
- P0-449-6 (no auto-merge): HELD — PR opened for maintainer review; not merged by this agent.
