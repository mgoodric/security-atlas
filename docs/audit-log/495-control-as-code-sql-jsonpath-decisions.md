# Slice 495 — control-as-code SQL + JSON-path evidence queries — decisions log

**Type:** JUDGMENT
**Slice doc:** `docs/issues/495-control-as-code-sql-jsonpath-evidence-queries.md`
**Closes:** the slice-009 (bundle accepts `rego|sql|jsonpath`) ↔ slice-012
(engine evaluates `rego` only) language gap.

## The defect (the "actual" detection tier)

`internal/control/manifest.go` validated and persisted three evidence-query
languages; `internal/eval/engine.go` (`firstRegoQuery`, ~L250) filtered to
`q.Language == "rego"` and **silently dropped** `sql` + `jsonpath`. A control
authored entirely in SQL or JSON-path uploaded fine and then evaluated to NO
state.

- **`detection_tier_actual`: `production`.** No unit or integration test ever
  asserted that a `sql`/`jsonpath` control produces state; the gap was latent
  and would have surfaced for a real operator authoring a non-rego control.
- **`detection_tier_target`: `integration` (and `unit`).** The silent-no-op is
  exactly the class an integration test ("a sql control evaluates to expected
  state against real Postgres") catches, plus a pure-Go unit test for the
  jsonpath classification + mixed rollup. Both are now added (AC-8/9/12). The
  recurring `target=integration, actual=production` shape is the canonical
  coverage-tier gap (slice 353 Q-13).

## Decisions

### D-SQL-1 — SQL runs against an evidence-only CTE, never a raw DB handle. **Confidence: HIGH.**

The author SQL is appended after a fixed, author-immutable prelude that
materialises the in-window record set into a single relation named `evidence`
(`result text`, `observed_at timestamptz`, `payload jsonb`) via
`jsonb_array_elements($1::jsonb)`. The records are passed as a JSONB parameter
— the SQL never touches a live table to GET the records. This mirrors the Rego
sandbox's "input is ONLY {records: …}" contract (`internal/eval/rego.go`).
Result contract: SELECT exactly one column; a boolean → pass/fail, a text →
must be a result enum; zero rows → fail; >1 row → rolled up via `computeResult`.

### D-SQL-2 — table-reachability seal is TWO layers (search_path + static guard). **Confidence: HIGH (load-bearing; revised after a RED test).**

**This is the sharpest call and it was revised mid-build by a failing test.**
The first cut prepended the `evidence` CTE and relied on it. The AC-10 test
proved that insufficient: `WITH evidence AS (…) SELECT result FROM evidence_records`
is valid SQL — the CTE is defined but ignored, and the author reaches the live
`evidence_records` table directly (RLS scoped it to the caller's tenant, so no
cross-tenant LEAK, but it IS a reach to a base table the slice forbids
[P0-495-1/2]). The fix layers two controls, both enforced:

1. **Runtime:** `SET LOCAL search_path TO ''` inside the read-only subtxn —
   a bare (unqualified) `evidence_records` no longer resolves; the `evidence`
   CTE name resolves before any schema, so legitimate queries still work.
   (`pg_catalog` is always implicitly present, so `::timestamptz`/
   `jsonb_array_elements` still resolve.)
2. **Static guard:** an empty `search_path` does NOT block a _schema-qualified_
   name (`public.evidence_records` still resolves — verified). So the static
   guard additionally rejects ANY `identifier.identifier` reference whose left
   side is not the `evidence` CTE. Authors only ever need the bare `evidence`
   relation; `evidence.col` (CTE-qualified column) is allowed, everything else
   (`public.*`, `pg_catalog.*`, `information_schema.*`) is rejected before the
   query runs.

Plus the `READ ONLY` transaction (belt-and-suspenders for invariant #2) and a
keyword denylist (write/DDL/DML/`pg_sleep`/`copy`/`set`/…). **Revisit:** a true
SQL parser (e.g. pg_query_go) would be more robust than the regex guard against
exotic obfuscation; deferred — the runtime seals (search_path + READ ONLY + RLS)
are the load-bearing controls and the regex is the early loud rejection, not the
only wall. **Confidence HIGH** that the combination forecloses non-evidence
reach (proven by AC-10 against real Postgres + RLS).

### D-SQL-3 — statement_timeout = 5s, mapped to inconclusive. **Confidence: HIGH.**

`SET LOCAL statement_timeout` (mirrored by a Go context deadline) bounds each
SQL query; a timeout is classified (`classifySQLError`) and bubbles as an
`ErrSQLQuery` → the control evaluates `inconclusive`, never hangs (AC-3/11,
threat-model D). 5s is generous vs. the bounded in-window set; tunable later.

### D-JP-1 — JSON-path runs in-process, per-record, no DB reach. **Confidence: HIGH.**

`github.com/PaesslerAG/jsonpath` (Goessner dialect). Evaluated against each
in-window record's JSONB payload — the records are already tenant+window
filtered, so the evaluator has zero DB reach (threat-model I is N/A for this
path). Compiled once, reused across records; a per-query 5s context deadline
bounds it (AC-5, threat-model D).

### D-JP-2 — match→pass classification: present-and-truthy. **Confidence: MEDIUM.**

A record passes when the path resolves to a present, truthy value (non-empty
array/object, non-empty string, non-zero number, `true`); fails on no-match or
falsy. This makes both idiomatic shapes work: a filter
(`$.checks[?(@.passed==true)]` → pass iff ≥1 match) and a scalar
(`$.encrypted` → pass iff truthy). **Revisit:** some operators may want
"path resolves to ANYTHING (even false)" = pass; the truthy rule is the more
useful default for a compliance check and is documented for authors. **Confidence
MEDIUM** — the rule is a defensible default, not the only reasonable one.

### D-DISPATCH-1 — evaluate ALL queries, roll up via computeResult; unsupported = FAIL LOUD. **Confidence: HIGH.**

`firstRegoQuery` (pick-one) is replaced by `parseEvidenceQueries` (full list) +
`evalQueries` (dispatch each, roll up via the existing `computeResult`
precedence — AC-6). A persisted language outside {rego,sql,jsonpath}, or an
empty expression, returns an error that fails the whole control evaluation —
NEVER a silent skip (the exact pre-495 bug). The slice-009 validator already
blocks unknown languages at upload; the engine's fail-loud is defense-in-depth
and is proven by `TestEvaluateControl_UnsupportedLanguageFailsLoud` (seeds
`sigma` directly into the JSONB, bypassing the upload validator).

### D-ROLLUP-1 — no_evidence still wins. **Confidence: HIGH.**

A control with zero in-window evidence stays `inconclusive` regardless of what
any query's default branch returned (unchanged from slice 012). "Absence of
evidence is not failure."

## Anti-criteria status

| Anti-criterion                                             | Status                                                     |
| ---------------------------------------------------------- | ---------------------------------------------------------- |
| P0-495-1 SQL not a raw handle / arbitrary surface          | MET (evidence-only CTE, D-SQL-1)                           |
| P0-495-2 SQL cannot read other tenant / non-evidence table | MET (search_path + static guard + RLS; AC-10 proves it)    |
| P0-495-3 evaluators never write the ledger                 | MET (READ ONLY subtxn + rollback; invariant #2 structural) |
| P0-495-4 no unbounded query                                | MET (SQL statement_timeout; jsonpath context deadline)     |
| P0-495-5 no Sigma / enforcement hooks / authoring UI       | MET (sigma explicitly rejected; nothing else added)        |
| P0-495-6 sql/jsonpath-only control no longer no-ops        | MET (AC-7/8/9 prove real state)                            |

## Out of scope / spillover

- Aligning the slice-009 manifest validator to REJECT `sigma` (currently the
  validator only accepts `rego|sql|jsonpath`, so it already rejects sigma — no
  change needed; the slice doc's "one-line follow-on" is moot).
- A pg_query_go SQL parser to replace the regex guard (D-SQL-2 Revisit) — not
  filed; the layered runtime seals make it non-urgent.

## Detection-tier classification (slice 353 Q-13)

- `detection_tier_actual`: **production** (the silent no-op was never caught by
  any tier; it would surface for a real non-rego control author).
- `detection_tier_target`: **integration** + **unit** (now closed: AC-8/9/10/11
  integration + AC-12 unit).
