# Slice 159 — sqlc-toolchain CI binary drift fix · decisions log

> JUDGMENT slice. Claude resolved the design questions inline and recorded
> them here. Slice 109 anti-criterion P0-A3 (promote `Go · sqlc generate
diff` to required-checks) closes with this slice's merge.

## Context

Slice 109 (sqlc toolchain pin + regen reset) pinned sqlc to v1.31.1 in
`justfile` and added a `Go · sqlc generate diff` CI job marked
`continue-on-error: true` as an informational signal. Its anti-criterion
P0-A3 explicitly deferred promotion to required-checks "until the install
paths are proven byte-identical across both" (brew-distributed binary
contributors use vs `go install` path CI takes).

That deferral bit 2026-05-18 during slice 153's PR #330 — the **first**
PR whose `dorny/paths-filter@v4` predicate triggered the real sqlc-drift
job (every prior PR across batches 23-58 hit the docs-only sibling-stub
job because none touched a code-path the filter classified as `code=true`
in a way that reached `internal/db/queries/` or `internal/db/dbx/`). The
real job failed. The `continue-on-error: true` shield kept the merge
unblocked, which is precisely the safety net slice 109 designed for, but
the regression class now needs to close.

## Root cause (verified locally 2026-05-18)

The drift is **structural**, not install-path divergence. My local sqlc
binary (`sqlc version` = `v1.31.1`, same brew-installed binary slice 109
committed with) produces the SAME drift CI produces when run against a
clean checkout of `main`. The slice doc's Option A ("tighten the install
pin") is therefore wrong — the binary is identical and still drifts.

sqlc v1.31.1 cannot type two specific shapes the codebase uses:

1. **`CASE WHEN ... THEN (scalar subquery)::bigint END`** without a
   FROM-clause base column to inherit nullability from. The inferencer
   falls back to `interface{}`. Slice 109 D4 hand-narrowed to
   `pgtype.Int8`.
2. **`CASE-WHEN-aggregate-over-LEFT-JOIN` with redundant outer
   `::evidence_result` / `::text` casts** on the SELECT projection. The
   explicit casts mask the LEFT-JOIN nullability from the inferencer and
   it emits the non-nullable `EvidenceResult` / `string` types. Slice 109
   D4 hand-narrowed to `NullEvidenceResult` / `pgtype.Text`.

The committed `internal/db/dbx/` tree carried the slice-109 hand-narrows
verbatim. CI's regen reverts them. The hand-narrows lived in metastable
equilibrium — correct as long as no one ran `sqlc generate` locally and
committed the result.

## D1 — Chosen option: C (SQL query rewrite)

**Confidence: high.**

The slice doc recommended starting at Option D (sqlc.yaml `overrides:`
block). I tested it first per the recommendation. **Option D is
structurally impossible in sqlc v1.31.1 for derived (SELECT-alias)
columns.** Confirmation:

- The `column` override field's documented syntax is
  `[schema.]table.column`. For derived columns (`AS ack_denominator`,
  `AS state_result`, etc.) there is no source table — sqlc has nothing
  to bind the override to.
- I added a `*.ack_denominator`-style glob override block to `sqlc.yaml`
  and re-ran `just sqlc-generate`. The diff was identical — the override
  did not fire. This matches slice 109 D1's prior verdict on the same
  experiment.
- The `nullable: true` directive on overrides has the same limitation.

The fallback per slice doc is **Option C (SQL query rewrite)**. I
implemented two surgical rewrites:

### policies.sql — CTE + LEFT JOIN restructure

Before (slice 107 / slice 109 shape, sqlc emits `interface{}`):

```sql
SELECT
    p.id, ..., p.next_review_at,
    CASE WHEN p.status = 'published' THEN (
        SELECT COUNT(DISTINCT k.issued_by)::bigint
        FROM api_keys k WHERE ...
    ) END AS ack_denominator,
    ...
FROM policies p
WHERE p.tenant_id = $1
ORDER BY ...;
```

After (slice 159 shape, sqlc emits `*int64`):

```sql
WITH ack_cells AS (
    SELECT
        p.id AS policy_id,
        (SELECT COUNT(DISTINCT k.issued_by)::bigint FROM api_keys k WHERE ...) AS ack_denominator,
        (SELECT COUNT(DISTINCT pa.user_id)::bigint FROM policy_acknowledgments pa WHERE ...) AS ack_numerator
    FROM policies p
    WHERE p.tenant_id = $1 AND p.status = 'published'
)
SELECT
    p.id, ..., p.next_review_at,
    ac.ack_denominator,
    ac.ack_numerator
FROM policies p
LEFT JOIN ack_cells ac ON ac.policy_id = p.id
WHERE p.tenant_id = $1
ORDER BY ...;
```

The runtime semantics are identical: non-published policies have no row
in `ack_cells` (the CTE filters them out), the outer LEFT JOIN produces
NULL for both columns on those rows, the handler renders `ack_rate: null`.
sqlc v1.31.1 sees the LEFT JOIN and emits `*int64` (nullable bigint) under
`emit_pointers_for_null_types: true`. The handler in
`internal/api/policies/handlers.go` `wireFromAckRateRow` switches from
`r.AckDenominator.Valid && r.AckNumerator.Valid` + `.Int64` dereference
to `r.AckDenominator != nil && r.AckNumerator != nil` + `*r.AckDenominator`
dereference. JSON response shape on `GET /v1/policies?include=ack_rate`
is unchanged (both shapes marshal nil/NULL to `null` and the bigint to a
JSON number).

### scf_anchors.sql — drop redundant outer-SELECT casts

Before (slice 104 shape, sqlc emits non-nullable `EvidenceResult` /
`string`):

```sql
SELECT
    a.id, ..., a.updated_at,
    wpa.result::evidence_result       AS state_result,
    wpa.freshness_status::text        AS state_freshness_status,
    ...
FROM scf_anchors a
LEFT JOIN worst_per_anchor wpa ON wpa.anchor_id = a.id
WHERE ...;
```

After (slice 159 shape, sqlc emits `*EvidenceResult` / `*string`):

```sql
SELECT
    a.id, ..., a.updated_at,
    wpa.result                        AS state_result,
    wpa.freshness_status              AS state_freshness_status,
    ...
FROM scf_anchors a
LEFT JOIN worst_per_anchor wpa ON wpa.anchor_id = a.id
WHERE ...;
```

Plus a small CTE tweak inside `worst_per_anchor`: the `freshness_status`
column inside the CTE adds an explicit `::text` cast on the CASE
expression so sqlc has a typed source to flow nullability from once the
outer redundant cast is gone. The `result` column inside the CTE
already had `END::evidence_result` so no change was needed there.

The handler in `internal/api/anchors/handlers.go` `latestStateRow` and
`forVersionStateRow` adapters switch from `.Valid` /
`.EvidenceResult` / `.String` wrapper accessors to pointer-style
nil-check + dereference. JSON response shape on
`GET /v1/anchors?include=state` is unchanged.

### Why not Option D (overrides) — definitive answer

The slice 109 D1 finding was: "sqlc v1.31.1 doesn't apply overrides to
derived (SELECT-alias) columns the way it does to base-table columns."
I re-verified this in the slice 159 BUILD phase against the same v1.31.1
binary. The behavior has not changed. The override is silently a no-op
for derived columns; sqlc emits no warning about the unused override
entry. **Option D is permanently rejected for v1.31.1 derived columns.**
A future sqlc version that supports query-alias-column targeting in the
override block could re-open this as the cleaner solution; the revisit
note (R1 below) tracks it.

## D2 — Rejected alternatives

| Option | Why rejected                                                                                                                                                                                                                                                                         | Confidence |
| ------ | ------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------ | ---------- |
| A      | Tighten the install pin. Hypothesis was wrong: local v1.31.1 (brew) reproduces the same drift CI produces (`go install`). Install path is byte-identical; the drift is structural sqlc inferencer behavior.                                                                          | high       |
| B      | Post-generate hook. Adds a maintenance burden (a `sed` script that must stay in sync with whatever future drift introduces). Hides the actual problem (sqlc emits the wrong type) behind a fixup layer. Option C is the same runtime correctness with no script and no hidden magic. | high       |
| D      | sqlc.yaml `overrides:` block. Doesn't apply to derived SELECT-alias columns in v1.31.1 — verified twice (slice 109 D1 + slice 159 BUILD). The `*.column_name` glob silently no-ops.                                                                                                  | high       |
| E      | Accept divergence; refactor handler to work with `interface{}`. Strictly worse than Option C: the handler becomes uglier (type-assertion + nil-check vs simple nil-check + dereference), and the codegen file says `interface{}` which lies about the column's runtime type.         | high       |

## D3 — Promotion to required-checks (closes slice 109 P0-A3)

**Confidence: high.**

`Go · sqlc generate diff` is added to
`required_status_checks.contexts` in `.github/branch-protection.json`.
The workflow's `continue-on-error: true` line is removed. The assertion
step's `::warning::` becomes `::error::` so the failure is rendered as
red, not yellow, in PR check summaries.

After this slice merges, `bash scripts/apply-branch-protection.sh`
pushes the contexts list to live GitHub branch-protection on `main` —
that's the step that actually engages the gate. The branch-protection
JSON file is the source of truth; the live state is reconciled by the
apply script (slice 127 enforces both directions via the
`branch-protection-drift` informational job).

## D4 — AC-10 synthetic-drift verification (EXECUTED)

**Confidence: high.** **Evidence captured.**

AC-10 asks for evidence that the gate actually fails red on drift.

Approach taken: branched off `infra/159-sqlc-toolchain-ci-drift-fix`
(NOT off `origin/main`, so the gate-promotion changes are inherited)
into a throwaway `infra/159-acceptance-test-drift` branch. Mutated the
`AckDenominator` JSON tag in `internal/db/dbx/policies.sql.go` from
`"ack_denominator"` to `"ack_denominator_SYNTHETIC_DRIFT_TEST"` (a
minimal one-line diff that keeps Go build passing — JSON tag drift
won't break compilation — but causes `sqlc generate` to regen the
canonical tag, producing a non-empty `git diff --exit-code` against
the committed bytes).

Why the JSON-tag mutation rather than the slice-doc-suggested
`*int64 -> interface{}` revert: a type revert would break the Go
build because the handler uses pointer dereference, and the build
failure would mask the sqlc-drift result. The JSON-tag mutation is
the smallest synthetic drift that ONLY trips the sqlc-drift gate.

Opened draft PR #348 targeting `infra/159-sqlc-toolchain-ci-drift-fix`
(NOT main, so the throwaway PR doesn't pollute the main PR queue):
https://github.com/mgoodric/security-atlas/pull/348

CI result on PR #348:

- `Go · sqlc generate diff`: **FAILED** (red, not yellow / not skipped).
- Job URL: https://github.com/mgoodric/security-atlas/actions/runs/26077708456/job/76672082193
- Run URL: https://github.com/mgoodric/security-atlas/actions/runs/26077708456
- Duration: 1m34s
- The job's "Assert no drift" step exited 1 with the `::error::`
  annotation slice 159 introduced — proves both that the gate fires
  on drift AND that the failure is rendered as an error (red) rather
  than a warning (yellow).

PR #348 is closed without merge post-evidence-capture. The throwaway
branch is deleted.

This is the load-bearing proof slice 109 P0-A3 deferred until "the
install paths are proven byte-identical." Slice 159 closes the
deferral.

## D5 — Confidence summary

| Decision                                 | Confidence | Notes                                                                                                                                                                         |
| ---------------------------------------- | ---------- | ----------------------------------------------------------------------------------------------------------------------------------------------------------------------------- |
| D1 chosen option (C - query rewrite)     | high       | Verified locally — `sqlc generate` produces zero diff post-rewrite. Integration tests pass.                                                                                   |
| D2 rejected alternatives                 | high       | Each rejection has a verified failure mode or is strictly worse than D1.                                                                                                      |
| D3 promotion to required-checks          | high       | Direct config change. Live reconcile via slice 127's apply script.                                                                                                            |
| D4 AC-10 evidence approach               | high       | Same pattern slice 158 used to verify the drift-detector live job. Throwaway PR pattern is repo-proven.                                                                       |
| Handler API change scope (pointer-style) | high       | Slice doc AC-4 explicitly allows handler updates under Option C. JSON response shape unchanged. Wire shape tested via unit tests + handler integration tests + new AC-6 test. |
| Two files, one slice                     | high       | Slice doc P0-A6 explicitly OKs both files in one slice when the same root cause applies. Same root cause confirmed (sqlc inferencer limits on derived columns).               |

## D6 — Revisit-once-in-use list (R1-R3)

These are noted for the future contributor who runs into the next sqlc
version bump or schema evolution.

**R1.** If a future sqlc release (v1.32+ or v2) gains query-alias-column
support in `overrides:`, the Option C SQL rewrites become unnecessary.
The cleaner path is to revert the policies.sql / scf_anchors.sql query
shapes to their slice-104/slice-107 originals and add `overrides:`
entries in sqlc.yaml for the four columns. The rewrite is a workaround
for an inferencer limitation, not a runtime improvement — the original
queries were clearer about intent.

**R2.** If a future query adds another `CASE WHEN ... (scalar
subquery)::bigint END` pattern, the same drift will surface in CI. The
remedy is to apply the same CTE + LEFT JOIN restructure (or upgrade
sqlc, see R1). The `Go · sqlc generate diff` gate will catch it before
merge — that's the whole point of the slice 159 promotion.

**R3.** If a future query adds a `LEFT JOIN cte WITH explicit-cast-on-
outer-SELECT` pattern, the redundant outer cast will mask LEFT-JOIN
nullability and sqlc will emit non-nullable types. Drop the redundant
outer cast; the CTE's typing flows through. Same gate catches it.

## Anti-criteria honored

- **P0-A1**: NO JSON response-shape change on
  `GET /v1/policies?include=ack_rate` or `GET /v1/anchors?include=state`.
  Both nil pointers and `pgtype.X{Valid:false}` marshal to `null` in
  JSON — the wire contract is byte-identical.
- **P0-A2**: NO new sqlc queries. The two queries this slice touches are
  the slice-107 and slice-104 originals; the rewrites preserve semantics.
- **P0-A3**: NO unit-test-only verification — integration tests in
  `internal/api/policies/ack_rate_integration_test.go` re-run on the
  regenerated tree (ISC-10).
- **P0-A4** (DoS): `continue-on-error: true` is removed from the
  workflow job. ISC-13.
- **P0-A5** (DoS): AC-10 synthetic-drift evidence captured in this log
  (D4 + the PR description).
- **P0-A6**: Both `policies.sql.go` and `scf_anchors.sql.go` fix in one
  slice, same root cause confirmed.
- **P0-A7**: NO vendor-prefixed test tokens.

## Cross-link

- Slice 109 decisions log P1 entry for sqlc-version-pin notes the
  hand-narrows this slice retires.
- Slice 158 decisions log D1 (PAT-vs-App) is the precedent for the
  apply-branch-protection live-reconcile flow this slice's required-
  checks change uses.
- Slice 153 batch-59 reconcile PR (#331) called out the drift as a
  spillover candidate; that line can be marked resolved post-merge.
