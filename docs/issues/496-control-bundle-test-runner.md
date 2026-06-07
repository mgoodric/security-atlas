# 496 — Control-bundle test runner: fixture evidence + expected pass/fail

**Cluster:** control-as-code
**Estimate:** M (1-2d)
**Type:** JUDGMENT (the test-fixture format + how the runner stubs the in-window evidence set are design calls)
**Status:** `ready`

## Narrative

Canvas §4.4 lists, as a first-class part of every control bundle, "**Tests** —
fixture evidence + expected pass/fail," and slice 009 (control bundle format)
promised "a tests directory with fixture evidence + expected pass/fail." The
bundle parser knows about test fixtures — `internal/control/testdata/` holds
bundle fixtures used by the parser's own tests — but **there is no runner that
executes a control bundle's declared tests**: feed the fixture evidence to the
control's evidence query, assert the resulting state matches the declared
expectation. A control author can write a Rego (or, after slice 495, SQL /
JSON-path) evidence query and **cannot verify it does what they think** short of
uploading the bundle and manually inspecting live control state.

This is the control-as-code analogue of "tests before code" — and its absence is
felt most by exactly the v1 persona. A solo security leader authoring a custom
control bundle has no fast feedback loop: they cannot run `atlas control test
<bundle>` and learn "your MFA query passes the all-users-MFA fixture but fails
the one-user-without-MFA fixture as expected." Without it, control authoring is
write-upload-eyeball-debug — slow, error-prone, and the path by which a subtly
wrong query ships and silently mis-states compliance. The control-as-code thesis
("anyone can write a YAML control bundle") is hollow if authors can't test their
queries the way they test code.

This slice ships the **control-bundle test runner**:

- A **test-fixture format** in the bundle's tests directory: each test case
  declares a name, a set of fixture evidence records (matching the control's
  `evidence_kind` schema), and an `expected_state`
  (pass/fail/inconclusive — and optionally per-cell expectations).
- A **runner** that, for each test case, feeds the fixture evidence to the
  control's evidence query through the **same evaluation engine** the live path
  uses (`internal/eval`), against an **in-memory / ephemeral** in-window record
  set (no live ledger, no tenant data), and asserts the produced state equals
  `expected_state`.
- A **CLI**: `atlas control test <bundle-dir>` reporting per-test pass/fail
  (text + `--json`), exit non-zero on any failure (so it slots into CI for
  community-contributed bundles).

The runner reuses the live evaluation engine deliberately: a bundle that passes
its tests is verified against the _same_ code that evaluates it in production —
no separate "test interpreter" that could drift from real behavior.

**Scope discipline.** The runner + fixture format + CLI only. Does NOT add a
control-authoring UI or an in-app "run tests" button (CLI is the authoring
loop; a UI is a follow-on). Does NOT add fixture _generation_ / record-and-replay
from live evidence (a v2 convenience). Does NOT change the evaluation engine's
semantics — it consumes the engine. Does NOT wire bundle tests into the upload
API as a hard gate (a follow-on can make "tests must pass to upload" a policy;
this slice ships the runner first).

## Threat model (STRIDE)

The runner executes **author-supplied evidence queries against author-supplied
fixtures**, locally / in CI — it does NOT touch live tenant evidence or the
production ledger. The threat surface is mostly the same sandbox concern as the
live evaluator, scoped to a no-live-data context, plus a CLI-input surface.

**S — Spoofing.** N/A — the runner is a local/CI authoring tool; no
authenticated network surface, no tenant identity.

**T — Tampering.** A test fixture or query could attempt to reach beyond the
ephemeral fixture set into the host or a real database.
**Mitigation:** the runner feeds fixtures through the **same sandboxed evaluator**
as the live path (the Rego capability-restricted sandbox; the slice-495 read-only
SQL surface; the in-process JSON-path) — so the same deny-by-default posture
applies. The runner uses an **in-memory / ephemeral** record set, not a real
ledger connection; a fixture query has no live DB to reach. No write to any
persistent store.

**R — Repudiation.** N/A — the runner is a stateless verification tool; its
output is the report.

**I — Information disclosure.** Because the runner uses only the author's own
fixtures (no live tenant data, no real ledger), there is no cross-tenant data to
leak. The risk reduces to "does a malicious query in a community-contributed
bundle exfiltrate from the CI host?"
**Mitigation:** the same sandbox the live evaluator uses (no network/filesystem
builtins for Rego; no DB reach for the in-memory SQL/JSON-path paths); the runner
does not grant the query any capability the live engine wouldn't. Document that
running an untrusted bundle's tests executes its queries in the sandbox — same
trust posture as uploading it.

**D — Denial of service.** A pathological query or a huge fixture set hangs CI.
**Mitigation:** the same per-query timeout the live evaluator enforces applies in
the runner; a per-test-case fixture-count bound; the CLI has an overall timeout.

**E — Elevation of privilege.** N/A — no authz surface; the CLI runs with the
invoking user's local privileges and grants the query nothing beyond the sandbox.

## Acceptance criteria

**Fixture format + runner**

- [ ] **AC-1.** A control-bundle test-fixture format is defined + validated: each
      test case has a name, fixture evidence records (schema-matching the
      control's evidence_kind), and an `expected_state`.
- [ ] **AC-2.** A runner feeds each test case's fixtures to the control's
      evidence query through the **live evaluation engine** (`internal/eval`)
      against an in-memory / ephemeral in-window record set (no live ledger).
- [ ] **AC-3.** The runner asserts the produced state equals `expected_state`
      per test case and aggregates pass/fail across the bundle.
- [ ] **AC-4.** The runner works for every evaluated query language the engine
      supports (Rego today; SQL + JSON-path once slice 495 lands — the runner
      dispatches through the same engine, so it gains languages automatically).

**CLI**

- [ ] **AC-5.** `atlas control test <bundle-dir>` runs the bundle's tests and
      reports per-test pass/fail (text + `--json`); exits non-zero on any
      failure (CI-friendly).

**Tests**

- [ ] **AC-6.** Unit/integration test: a bundle whose query is correct passes its
      test cases; a bundle whose query is wrong fails the expected case (the
      runner catches the bug).
- [ ] **AC-7.** Test: a test case with a fixture that should yield `fail` is
      reported as a _passing test_ when the query correctly returns `fail`
      (i.e. expected-fail is a pass for the test runner — semantics are correct).
- [ ] **AC-8.** Test: a query exceeding the per-query timeout is reported as a
      test error, not a hang (threat-model D).
- [ ] **AC-9.** Test: the runner uses no live DB / ledger (the in-memory record
      set is proven — e.g. the runner succeeds with no Postgres available).

**Docs**

- [ ] **AC-10.** Control-author docs document the fixture format + `atlas control
test`, with a worked example (the MFA control + a passing and a failing
      fixture); a changelog entry.

## Constitutional invariants honored

- **Canvas §4.4** — control bundles' "Tests — fixture evidence + expected
  pass/fail" promise is met; control-as-code gets its test-first loop.
- **#2 — Ingestion/evaluation separation.** The runner reuses the read-only
  evaluation engine; it never writes the ledger and uses no live evidence.
- **#9 — Manual evidence is first-class.** Manual-control bundles
  (manual_attested / manual_periodic) can declare attestation fixtures + expected
  state the same way automated ones do.

## Canvas references

- `Plans/canvas/04-evidence-engine.md` §4.4 — control-as-code bundle includes
  Tests (fixture evidence + expected pass/fail).
- `Plans/canvas/04-evidence-engine.md` §4.3 — evaluation is a read-only consumer
  (the runner reuses it).

## Dependencies

- **#009** (control bundle format + parser; the tests directory) — `merged`. The
  bundle shape this runner consumes.
- **#012** (control state evaluation engine) — `merged`. The engine the runner
  feeds fixtures through.
- **#495** (SQL + JSON-path evaluation) — `ready` (this batch). **Soft dep:** the
  runner dispatches through the engine, so it gains SQL/JSON-path coverage when
  495 lands; it ships useful for Rego without 495.

## Anti-criteria (P0 — block merge)

- **P0-496-1.** The runner does NOT use a separate "test interpreter" — it
  reuses the live `internal/eval` engine (no behavior drift).
- **P0-496-2.** The runner does NOT touch live tenant evidence or the production
  ledger — in-memory / ephemeral fixtures only (AC-9).
- **P0-496-3.** The runner grants queries NO capability the live sandbox
  wouldn't (threat-model I).
- **P0-496-4.** No unbounded query — the live per-query timeout applies
  (threat-model D, AC-8).
- **P0-496-5.** Does NOT add an authoring UI or fixture record-and-replay (scope
  discipline).
- **P0-496-6.** The CLI exits non-zero on any test failure (CI-usable, AC-5).

## Skill mix (3-5)

`grill-with-docs` · `tdd` (the runner is itself test infrastructure — its own
tests prove it catches a wrong query) · `database-designer` (only if an ephemeral
record-set abstraction needs schema shaping — likely none) · `security-review`
(running untrusted bundle queries in the sandbox) · `simplify`.

## Notes for the implementing agent

- **JUDGMENT calls you own:** the fixture-format shape (YAML test cases beside
  the bundle? a `tests/` dir of `<case>.yaml`? — pattern-match the existing
  `internal/control/testdata/` layout); how to present the in-window record set
  to the engine without a real ledger (an in-memory implementation of the
  record-source interface the engine already reads through is the clean seam —
  check `internal/eval/store.go` for the interface to satisfy); whether
  expected-fail fixtures read naturally (AC-7's semantics). Record in the
  decisions log.
- Reuse the engine, don't fork it — a bundle that passes `atlas control test`
  MUST behave identically when uploaded and evaluated live. That equivalence is
  the whole value.
- Detection-tier: `none` unless building the runner surfaces an engine bug; a
  wrong-query escape the runner fails to catch would be `target=unit,
actual=production` for the eventual bad control.
