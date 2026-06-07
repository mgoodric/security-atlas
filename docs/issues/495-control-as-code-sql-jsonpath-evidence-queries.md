# 495 — Control-as-code: evaluate SQL + JSON-path evidence queries (not just Rego)

**Cluster:** control-as-code
**Estimate:** L (3-4d)
**Type:** JUDGMENT (the SQL sandbox shape + JSON-path dialect + result-rollup semantics are subjective expressiveness/security calls)
**Status:** `ready`

## Narrative

Canvas §4.4 (control-as-code) says a control bundle declares "one or more
**evidence queries** — Rego/SQL/JSON-path/Sigma over the evidence ledger," and
slice 009 (control bundle format) explicitly accepts and persists evidence
queries in three languages — its validator allows `rego | sql | jsonpath`
(`internal/control/manifest.go`: "evidence_queries[%d].language %q is not one of
rego|sql|jsonpath"). But the **evaluation engine only runs Rego.**
`internal/eval/engine.go` (`firstRegoQuery`, ~L250) carries the comment
"Non-rego languages (sql, jsonpath) are not evaluated in [the] result rollup"
and filters to `q.Language == "rego"`. A control author can write — and the
upload API will accept and persist — a `sql` or `jsonpath` evidence query, and
the engine will **silently ignore it**, evaluating the control as if it had no
query (or only its Rego query).

This is a quiet correctness gap, not just a missing feature: the bundle format
**promises** three languages and validates them as legal, but two of the three
are no-ops at eval time. A control authored entirely in SQL or JSON-path appears
to upload successfully and then never evaluates — the worst failure mode for a
GRC tool (a control that _looks_ configured but produces no state). The slice
009 / 012 split documented "slice 012 will dispatch on Language to invoke the
appropriate [evaluator]" — slice 012 shipped Rego only; SQL + JSON-path dispatch
was never built.

This slice **closes the language gap for SQL + JSON-path** (Sigma stays out of
scope — see below), wiring the engine's `evidence_queries[]` dispatch to evaluate
all three legal languages:

- **SQL** — a read-only, capability-restricted query over the in-window evidence
  records (mirroring the Rego sandbox discipline in `internal/eval/rego.go`):
  the query runs against a constrained read-only view of the tenant's in-window
  evidence (NOT arbitrary tables), returns a boolean/result rollup, and is
  blocked from any write, DDL, or cross-table reach.
- **JSON-path** — a JSON-path expression evaluated against each in-window
  evidence record's JSONB payload, with the same `result` rollup contract as
  Rego (the query yields pass/fail/inconclusive over the matched set).

Both reuse the existing in-window-record evaluation contract and the
result-precedence rollup (`internal/eval/state.go`) so a control with mixed-
language queries rolls up consistently.

**Scope discipline.** SQL + JSON-path only. **Sigma stays out of scope** — Sigma
is detection-as-code (the canvas §4.4 table explicitly lists Sigma under
_detect-as-code_, "Alerts (out of scope)"), so a Sigma evidence-query language
was never actually promised for the eval engine; if anything the manifest
validator should NOT accept `sigma` (a one-line follow-on to align the validator,
noted but not in this slice's ACs). Also out of scope: enforcement hooks
(Kyverno/Custodian — canvas marks them "(optional)"), and a query-authoring UI
(bundles are uploaded YAML).

## Threat model (STRIDE)

This adds **two new query-evaluation engines that run author-supplied
expressions** over tenant evidence — the same class of risk as the existing Rego
sandbox, now doubled. SQL evaluation is the sharpest new edge: an in-database
query language is the classic injection / data-exfiltration surface.

**S — Spoofing.** N/A for the eval path — queries come from already-validated,
already-persisted control bundles (slice 009 upload is the authenticated
ingress; this slice only evaluates).

**T — Tampering.** A malicious or buggy SQL query mutates evidence or control
state.
**Mitigation:** the SQL evaluator runs **read-only** — against a constrained,
read-only view/CTE of the in-window evidence set, in a read-only transaction,
with no write/DDL capability. It mirrors the Rego sandbox's deny-by-default
posture (`internal/eval/rego.go`'s `deniedRegoBuiltins`). The evaluation stage
is a read-only consumer of the ledger (invariant #2) — the SQL evaluator must
not be a hole in that wall.

**I — Information disclosure (PRIMARY).** A SQL evidence query is the obvious
cross-tenant exfiltration vector: `SELECT * FROM evidence_records` reaching
another tenant's rows, or a query reading `api_keys` / `users` / any non-evidence
table.
**Mitigation:** the SQL query is NOT given a raw DB handle. It runs against a
**single, tenant-scoped, read-only view of in-window evidence** (the same
in-window record set the Rego path receives), under `app.current_tenant` RLS, with
no reach to other tables — a parameterized, schema-restricted surface, not
arbitrary SQL against the live schema. JSON-path is evaluated in-process against
records already filtered to the tenant + window, so it has no DB reach at all.
An integration test proves a SQL query that _attempts_ to read another tenant's
evidence (or a non-evidence table) returns nothing / errors — never another
tenant's data.

**D — Denial of service.** An expensive SQL query (cartesian self-join, regex
catastrophe) or a pathological JSON-path expression hangs evaluation.
**Mitigation:** a per-query `statement_timeout` for the SQL path; a per-query
evaluation timeout + complexity bound for JSON-path (reuse the Rego path's
timeout pattern); the in-window record set is already bounded by the freshness
window. A runaway query fails the control as `inconclusive` with an error, never
hangs the engine.

**E — Elevation of privilege.** A query reaches authz/policy tables to read or
infer secrets.
**Mitigation:** the read-only evidence-only view forecloses reach to authz
tables; the evaluator role/connection has no privilege beyond reading the
in-window evidence view. No new authz surface.

## Acceptance criteria

**SQL evidence queries**

- [ ] **AC-1.** The engine dispatches `language == "sql"` evidence queries to a
      read-only SQL evaluator that runs against a tenant-scoped, read-only view
      of the in-window evidence records and returns a pass/fail/inconclusive
      result.
- [ ] **AC-2.** The SQL evaluator is write/DDL-incapable and reaches no table
      beyond the in-window evidence view (read-only txn + restricted surface).
- [ ] **AC-3.** A `statement_timeout` bounds each SQL evidence query; a timeout
      yields `inconclusive` with an error, not a hang.

**JSON-path evidence queries**

- [ ] **AC-4.** The engine dispatches `language == "jsonpath"` evidence queries
      to an in-process JSON-path evaluator over each in-window record's JSONB
      payload, returning the same `result` rollup contract as Rego.
- [ ] **AC-5.** A per-query timeout / complexity bound applies (threat-model D).

**Rollup + consistency**

- [ ] **AC-6.** A control with mixed-language queries (e.g. one Rego + one SQL)
      rolls up through the existing result-precedence logic
      (`internal/eval/state.go`) consistently.
- [ ] **AC-7.** A control whose ONLY query is SQL or JSON-path now evaluates to a
      real state (the prior silent no-op is gone).

**Tests**

- [ ] **AC-8.** Integration test (`//go:build integration`): a control with a SQL
      evidence query evaluates to the expected state against real Postgres +
      fixture evidence.
- [ ] **AC-9.** Integration test: a control with a JSON-path query evaluates to
      the expected state.
- [ ] **AC-10.** **Cross-tenant SQL test (threat-model I — load-bearing):** a SQL
      evidence query cannot read another tenant's evidence or any non-evidence
      table; proven against real Postgres + RLS.
- [ ] **AC-11.** Integration test: a SQL query exceeding `statement_timeout`
      yields `inconclusive`, not a hang (threat-model D).
- [ ] **AC-12.** Pure-Go unit test for JSON-path result classification + the
      mixed-language rollup precedence (no DB).

**Docs**

- [ ] **AC-13.** Control-author docs document the SQL evidence-query surface
      (the read-only evidence view available, the timeout) + JSON-path; note the
      Sigma exclusion; a changelog entry.

## Constitutional invariants honored

- **#2 — Ingestion/evaluation separation, evaluation is a read-only consumer.**
  Both new evaluators are read-only over the ledger; they never write
  source-of-truth evidence (the SQL read-only-view design enforces it).
- **#6 — Tenant isolation via RLS.** The SQL view runs under `app.current_tenant`;
  cross-tenant reach is proven absent (AC-10).
- **Canvas §4.4** — control-as-code's Rego/SQL/JSON-path evidence-query promise
  is now met for all three legal languages.

## Canvas references

- `Plans/canvas/04-evidence-engine.md` §4.4 — control-as-code evidence queries
  in Rego/SQL/JSON-path; the detect-as-code (Sigma) boundary.
- `Plans/canvas/04-evidence-engine.md` §4.3 — ingestion/evaluation separation
  (the evaluator is read-only).

## Dependencies

- **#009** (control bundle format — accepts `rego|sql|jsonpath`) — `merged`. The
  source of the multi-language queries this slice evaluates.
- **#012** (control state evaluation engine — Rego only) — `merged`. The engine
  this slice extends with SQL + JSON-path dispatch.
- **#016** (freshness / in-window record set) — `merged`. The bounded record set
  both evaluators receive.

## Anti-criteria (P0 — block merge)

- **P0-495-1.** The SQL evaluator is NOT given a raw DB handle / arbitrary-SQL
  surface — read-only, evidence-only view (threat-model T/I, AC-2).
- **P0-495-2.** A SQL evidence query CANNOT read another tenant's evidence or any
  non-evidence table (threat-model I, AC-10 — load-bearing).
- **P0-495-3.** The evaluators NEVER write to the evidence ledger or control-state
  source of truth (invariant #2).
- **P0-495-4.** No unbounded query — SQL `statement_timeout` + JSON-path
  complexity bound (threat-model D).
- **P0-495-5.** Does NOT add a Sigma evaluator, enforcement hooks, or a
  query-authoring UI (scope discipline).
- **P0-495-6.** A SQL/JSON-path-only control NO LONGER silently no-ops — it
  produces a real state (AC-7).

## Skill mix (3-5)

`grill-with-docs` · `tdd` (integration-first; the cross-tenant SQL test +
timeout test are load-bearing) · `database-designer` (the read-only tenant-scoped
evidence view / CTE the SQL path runs against) · `security-review` (a new SQL
evaluation surface over tenant data — sharp) · `simplify`.

## Notes for the implementing agent

- **JUDGMENT calls you own:** the exact shape of the read-only evidence view the
  SQL query runs against (a CTE that the author's SQL must `SELECT` from? a
  fixed view name? — pick the most-restrictive surface that is still
  expressive enough to be useful, mirroring how `internal/eval/rego.go` exposes
  only `input` records); the JSON-path library + dialect; the SQL/JSON-path
  result→state mapping. Mirror the Rego sandbox's deny-by-default posture
  exactly. Record in the decisions log with confidence (the SQL sandbox shape is
  the highest-stakes call — lean conservative).
- The Rego path (`internal/eval/rego.go`) is the security template — the SQL +
  JSON-path paths must be at least as locked-down.
- Detection-tier: a cross-tenant SQL reach caught by AC-10 is
  `target=integration, actual=integration`; if it reached production it is the
  canonical `target=integration, actual=production` RLS gap.
