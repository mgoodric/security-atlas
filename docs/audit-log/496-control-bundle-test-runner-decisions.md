# 496 — Control-bundle test runner: JUDGMENT decisions log

Slice type: JUDGMENT. This file records the subjective build-time calls the
slice doc named as the implementing agent's to make: the test-fixture format,
how the runner stubs the in-window evidence set without a live ledger, and the
result-rollup / expected-fail semantics. It does NOT block merge — the
maintainer iterates post-deployment from the "Revisit once in use" list.

- detection_tier_actual: unit
- detection_tier_target: unit

(One genuine semantic misunderstanding surfaced DURING the build and was caught
at the green step by the slice's own unit tests, then fixed in the same PR — see
the note below. No shipped-behavior defect escaped; the runner is itself test
infrastructure and its own tests caught the author's wrong mental model before
any fixture would have silently passed.)

## Build-time bug caught (detection-tier note)

My first fixture set asserted that a record observed OUTSIDE the freshness window
should make the control `inconclusive` (I conflated "aged out of window" with
"no evidence"). Two of the slice's own tests
(`TestEvaluateFixture_FreshnessGate_NoEvidenceOverride` and the rego-bundle
`stale-evidence-inconclusive` case) failed at the green step. Reading
`engine.go` `computeRow` showed the live engine only overrides to `inconclusive`
when freshness == `no_evidence` (ZERO records); records that EXIST but are stale
leave `inWindow` empty, so the declared query runs over the empty set and returns
its `default` branch (`fail` for the test policy). The runner must REPRODUCE this,
not "fix" it — so I corrected my fixtures + tests to assert the engine-faithful
behaviour and added an explicit `stale-evidence-query-default` case documenting
it. `detection_tier_actual = unit` (caught by this slice's unit tests);
`detection_tier_target = unit` (a fixture-authoring misunderstanding; unit is the
cheapest and correct tier). This is the slice doc's named risk — "a wrong-query
escape the runner fails to catch would be `target=unit, actual=production` for
the eventual bad control" — landing on the right side: the runner caught the
wrong mental model at unit time.

## Decisions made

### D-FMT-1 — Fixture format: a `tests/` directory of `*.yaml` files, each holding `cases:` (AC-1)

- **Options considered:** (a) a single `tests.yaml` beside `control.yaml`;
  (b) a `tests/` directory of one-case-per-file `<case>.yaml`; (c) a `tests/`
  directory of `*.yaml` files each holding one OR MORE cases under a `cases:`
  key.
- **Chosen:** (c).
- **Rationale:** slice 009 promised "a **tests directory**" and the existing
  `internal/control/testdata/` layout is a directory of YAML beside the manifest
  — (c) pattern-matches both. Allowing multiple cases per file (vs (b)'s
  one-per-file) keeps a small control's tests in one readable file while still
  letting a large bundle split cases across files; files are read in lexical
  order and case order is preserved, so the report is deterministic. Case names
  are unique across the whole directory (a duplicate is a loud load error) so a
  report row maps to exactly one case.
- **Schema shape:** each case = `name` (required, unique) + optional
  `description` + `expected_state` (required; pass|fail|na|inconclusive) +
  `records[]` + optional `evaluated_at`. Each record = `result` (required) +
  optional `observed_at` + optional `payload` (a YAML object, JSON-marshalled
  for the SQL / JSON-path evaluators). `KnownFields(true)` on the decoder makes a
  typo'd key (`expectd_state`) a loud parse error rather than a silently-ignored
  field that would yield a wrong result.
- **`observed_at` defaults to the case's evaluation instant** so a simple
  pass/fail case is in-window without the author reasoning about freshness; an
  author testing staleness sets `observed_at` (and usually `evaluated_at`)
  explicitly. This is the ergonomic default the slice doc asked for.

### D-STUB-1 — In-memory evidence-set injection via a new `eval.EvaluateFixture` seam (AC-2 / AC-9 / invariant #2 / P0-496-1 / P0-496-2)

- **Options considered:** (a) the runner re-implements the
  freshness-filter + per-language dispatch + rollup itself (a "test
  interpreter"); (b) the runner spins up a throwaway tenant + writes fixtures to
  a real `evidence_records` table, then calls the live `Engine.EvaluateControl`;
  (c) extract a DB-free entry point in `internal/eval` that runs the SAME
  unexported helpers (`inWindowRecords` / `evalRegoQuery` / `evalSQLQuery` /
  `evalJSONPathQuery` / `computeResult` / `computeFreshness`) over an in-memory
  record set.
- **Chosen:** (c) — `eval.EvaluateFixture(ctx, freshnessClass, queries, records,
now, tx)`.
- **Rationale:** (a) is the explicit anti-criterion P0-496-1 — a second
  evaluation path would drift from the live engine and the whole value of the
  feature (a passing test ⇒ identical live behaviour) evaporates. (b) violates
  P0-496-2 / invariant #2: it would write the live ledger (or at least a real
  table), the runner would need Postgres + RLS context for EVERY language, and
  AC-9 ("succeeds with no Postgres available") would fail for the common
  Rego/JSON-path case. (c) is the clean seam the slice doc pointed at
  ("an in-memory implementation of the record-source interface the engine
  already reads through"). `EvaluateFixture` performs the IDENTICAL three steps
  `engine.go` `computeRow` performs (in-window filter → run every query → roll up
  → no_evidence override), calling the SAME unexported functions — there is no
  parallel logic to drift. The ONLY difference is the source of the records: the
  live path reads them from the ledger; the fixture path receives them as Go
  structs. No Store, no pool, no write.
- **Why a `pgx.Tx` parameter at all:** the SQL language is the one evaluator that
  genuinely needs a database — the slice-495 SQL sandbox materialises the records
  into a CTE and runs the author SELECT inside a read-only subtransaction. Rather
  than fork the SQL evaluator, `EvaluateFixture` takes an OPTIONAL `tx`: nil for
  the Rego/JSON-path-only common case (no DB, AC-9 holds), and a tenant-scoped tx
  when the caller wants SQL coverage. The runner's `Options.Tx` threads it
  through. A SQL query with a nil tx returns a typed `ErrFixtureSQLNeedsDB` —
  fail-loud, never a false pass (mirrors the engine's never-silently-skip-a-query
  posture).

### D-ROLLUP-1 — Result mapping + expected-fail semantics (AC-3 / AC-7)

- **Chosen:** the runner compares the engine's produced `Result` string against
  the case's `expected_state` string for exact equality; equal ⇒ the test
  PASSES.
- **Rationale:** this makes AC-7 read naturally with zero special-casing. A case
  whose query SHOULD return `fail` declares `expected_state: fail`; when the
  query returns `fail`, `actual == expected` ⇒ the test is a PASS. "Expected-fail
  is a passing test" falls out of plain string equality — no separate
  `expect_failure` boolean, no inverted logic. `na` and `inconclusive` are
  first-class expected states too, so a freshness/no-evidence case can assert
  `inconclusive` directly.
- **Report shape:** each case is PASS (ran, actual==expected), FAIL (ran,
  actual!=expected), or ERROR (the query could not run — compile error, SQL with
  no DB, cancelled context). The aggregate `AllPassed()` is true only when
  Failed==0 AND Errored==0; the CLI exits non-zero otherwise (AC-5 / P0-496-6).
  An ERROR is distinct from a FAIL so an author can tell "my query is broken"
  from "my query ran but gave the wrong answer".
- **Freshness faithfulness (recorded so it is not later "fixed"):** see the
  build-time-bug note above. `stale` (records exist, aged out) ≠ `no_evidence`
  (zero records). Only `no_evidence` overrides to `inconclusive`; `stale` lets the
  query run over the empty in-window set and return its default. The runner
  surfaces `FreshnessStatus` in the report so an author debugging an unexpected
  result can see which case they are in.

### D-LIMITS-1 — Threat-model bounds (AC-8 / threat-model D)

- **Chosen:** the per-query timeouts the live evaluators already enforce apply
  unchanged (the Rego sandbox, the slice-495 SQL `statement_timeout`, the
  JSON-path 5s ceiling); the runner adds a per-case fixture-count bound
  (`maxFixtureRecordsPerCase = 1000`) and a per-file byte cap
  (`maxTestFileBytes = 4 MB`) so a pathological fixture set cannot OOM or hang
  CI. A cancelled context surfaces as a per-case ERROR, never a hang (proven by
  `TestRun_CancelledContextErrors`).

## Scope honored (anti-criteria)

- **P0-496-1** — no test interpreter; `EvaluateFixture` reuses the live engine's
  exact helpers.
- **P0-496-2** — no live ledger / tenant evidence; fixtures are in-memory, the
  runner performs zero writes.
- **P0-496-3** — the runner grants queries no capability the live sandbox
  wouldn't: it calls the SAME `evalRegoQuery` (capability-restricted),
  `evalSQLQuery` (read-only subtxn, empty search_path), `evalJSONPathQuery`
  (in-process, no DB).
- **P0-496-4** — no unbounded query; live per-query timeouts + a fixture-count
  bound apply.
- **P0-496-5** — no authoring UI, no fixture record-and-replay. CLI only.
- **P0-496-6** — `controls test` exits non-zero on any failing or errored case.

## Revisit once in use

- **Per-cell expectations.** The slice doc floated "optionally per-cell
  expectations". v1 ships whole-control `expected_state` only — the engine
  evaluates the same ledger slice for every applicable cell in v1 (slice 017's
  per-cell evidence partitioning is itself a documented follow-up), so a per-cell
  fixture assertion has nothing to bind to yet. Add when per-cell evidence
  partitioning lands.
- **evidence_kind schema validation of fixture payloads.** The runner does NOT
  validate a fixture record's `payload` against the control's declared
  `evidence_kind` JSON Schema — the query is what asserts over the payload, and
  an author's fixture is their own concern. A `--strict` mode that validates
  fixtures against the registered schema is a natural convenience add.
- **Hard upload gate.** Wiring "tests must pass to upload" into
  `POST /v1/controls:upload-bundle` is a policy follow-on (the slice doc scoped it
  out); the runner ships first.
- **Fixture generation / record-and-replay** from live evidence (P0-496-5) — a
  v2 convenience.
