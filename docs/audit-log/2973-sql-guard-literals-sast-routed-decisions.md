# #2973 — SQL guard reads literals as literals; structural timestamp lint; `sast.scan_result` routed; CWE selection (decisions log)

Follow-ups from [#2831](https://github.com/mgoodric/security-atlas/issues/2831)
(PR #2970, its decisions log D2 and D4) and, per the ticket comment, from
[#2939](https://github.com/mgoodric/security-atlas/issues/2939) (PR #2988).

- detection_tier_actual: none
- detection_tier_target: none

No bug surfaced in shipped behaviour. Every new test was watched failing
against a mutation before the final run:

- lexing every `'...'` with backslash escapes failed the
  `'a\' FROM public.users --'` refusal;
- letting `OR` / `NOT` keep a guard failed the OR-ed and negated cases;
- dropping `CWE-89` from pi1_2 and `CWE-639` from pi1_3 failed
  `unsuppressed-cwe-89-fails` and `unsuppressed-cwe-639-fails`;
- dropping the "cwe is not a list" rule failed
  `cwe-not-a-list-is-inconclusive`;
- reading sast at the first cutover (no per-kind row) failed
  `TestRouted2973_PeriodFrozenBeforeSASTCutoverKeepsItsReads` (the period
  frozen between the cutovers listed and resolved the `scf:` row);
- taking sast out of the routed list failed both `TestRouted2973_*` tests.

## D1 — The guard reads tokens from a Postgres lexer

`internal/eval/sqllex.go` follows `scan.l` for the tokens that decide where a
literal ends: standard strings (`''` escape, backslash literal), `E'...'`
(backslash escape), `U&'...'`, `$tag$...$tag$` (not when the `$` continues an
identifier, and `$1` is a parameter), quoted identifiers (`""` escape), `--`
and nested `/* */` comments. Anything unterminated is refused.

- **Why a lexer and not "blank out quoted spans" with a regex.** The guard
  is the static half of the table-reachability seal (decisions log 495
  D-SQL-2). A literal the guard ends earlier or later than Postgres does is
  a hole: `'a\' public.users --'` hides a reference under one reading and
  not the other. A lexer that refuses what it cannot read fails closed.
- **`standard_conforming_strings` is pinned on** in the evaluator's
  subtransaction (`SET LOCAL`). The lexer reads `'...'` that way; a server
  configured off would end that literal later. The author SQL cannot change
  it: `set` is a forbidden keyword.
- **The qualified-name check got stricter as well as looser.** It is now
  "identifier (quoted or not), `.` token, identifier or `*`" over tokens,
  so `public . t`, `public/**/.t`, `"public".t` and `PUBLIC.t` are refused.
  The old regex let the spaced and quoted forms through. `"public.users"` is
  one identifier, not a qualified name, and is allowed; with an empty
  search_path it names nothing.
- **Only the dotted-name check moved to tokens.** The forbidden-keyword,
  semicolon and wall-clock checks still read raw text, so a literal such as
  `'set'` or `'a;b'` is still refused. That is a false positive in the safe
  direction, the ticket does not ask for it, and the wall-clock check
  deliberately matches the `'now'` literal.

## D2 — The timestamp lint is structural

`eval.SQLUnguardedTimestampCasts` now returns the payload value as written
(`payload->>'last_seen_at'`, `e.payload#>>'{a,b}'`) and an error for SQL the
lexer cannot read. It finds `(value)::T` (any number of parens, and chains
such as `::text::timestamptz`), `CAST(value AS T)`, `to_timestamp(value…)`
and `to_date(value…)`; T is `timestamptz`, `timestamp`, `date`, `time` or
`timetz`. A payload value is `payload`, optionally alias-qualified, then any
`->` / `#>` steps and a final `->>` / `#>>`.

- **Guarded means inside the THEN of its own guard.** The conversion must sit
  in the `THEN` branch of a `CASE WHEN` (at any enclosing level) whose
  condition, at that `CASE`'s own level, holds both `value ~ '<RFC3339Pattern>'`
  and `pg_input_is_valid(value, 'timestamptz')` for the same value. A guard
  in a nested `CASE` inside the condition, in another `CASE`, in an `ELSE`,
  in a later `WHEN`, beside the conversion in the condition, or in a `WHERE`
  does not count: Postgres gives `AND` and `WHERE` conjuncts no evaluation
  order.
- **Same value, alias included.** `e.payload->>'t'` is not guarded by a
  guard on `payload->>'t'`: the two may be different rows.
- **Any `OR` or `NOT` in the condition voids its guards.** Telling a
  disjunct from a conjunct needs a parser; refusing both is the cheap
  fail-closed reading, and no stock guard uses either.
- **`payload->>'t'::text`** casts the literal, not the field (`::` binds
  tighter than `->>`), so it is not reported.
- Every stock bundle already passes the stricter lint; no bundle changed for
  it.

## D3 — CWE selection: an optional `cwe` list at schema 1.2.0

#2831 D2 left this as "an additive follow-up if pushers find the mapping a
burden"; the ticket asks for it.

- **Shape.** `cwe` is an optional array of unique strings matching
  `^CWE-[1-9][0-9]*$`, per finding. An array, because a SARIF rule commonly
  carries several CWE tags. A string id rather than an integer, because
  that is how CodeQL (`external/cwe/cwe-89`) and Semgrep (`CWE-89: ...`)
  both write it, minus the decoration the pusher strips. 1.2.0 is additive
  over 1.1.0 (`TestSASTScanResult120_AdditiveOver110`).
- **Sets.** The sets #2831 D2 named: pi1_2 CWE-20, CWE-78, CWE-89, CWE-502;
  pi1_3 CWE-269, CWE-639, CWE-862. CWE-20 is broad (improper input
  validation, a class many scanners attach widely); pi1_2 is the
  input-validation control, so a CWE-20 finding is in scope by definition.
  Neighbours such as CWE-77 (command injection, parent of CWE-78) or CWE-285
  (improper authorisation) were not added: the ticket and D2 fixed these
  sets, and widening them is a control-text change for another ticket.
- **Selection is OR.** A finding is selected by its rule id or by any CWE in
  the set, matched exactly (`?|`). Neither replaces the other, so a 1.1.0
  pusher's mapping keeps working.
- **Score.** Not an object, 1; suppressed, 2; rule id in the set, 0; a cwe
  array overlapping the set, 0; then, not selected but possibly selectable,
  1: a rule id that is not a string, or a `cwe` present but not an array (a
  tenant-private schema could allow `"cwe": "CWE-89"`); otherwise 2. A
  matching CWE decides even when the rule id is unusable
  (`cwe-selects-without-a-string-rule-id`).
- **Rule ids are plain literals** now that the guard reads them as literals
  (D1), so the `'injection' || '.sql'` workaround and its comment are gone.

## D4 — `sast.scan_result.v1` is routed by type

The kind moves into `routed.kinds`; `TestStockBundleKindsAreCategorized` no
longer lists it as bound. One push with no `--control` reaches cc7_1
detection, pi1_2 and pi1_3 (`TestRouted2973_OneSASTPushReachesEveryDeclaringControl`).

- **Existing rows, live reads.** Rows already bound to a control keep
  matching that control by `control_id`. Rows stored with `control_id`
  NULL (a push under `scf:VPM-06`, which no control resolves) become
  readable by every declaring control in live reads, re-evaluations and new
  population draws. This is the reading Matt chose for #2924's existing
  rows (its D5, option A), applied to one more kind.
- **A pipeline still pushing three times** stores three identical scans, and
  each control reads all three. The latest-scan-per-repository rank ties
  them and the worse verdict wins, which is the same verdict. Not deduped.
- The CLI docs, the evidence primitive page and ADR 0028 decision 9 say so;
  the ADR's Bound example now names `access_review.completion.v1`, and an
  amendment paragraph records the move and D5.
- Tests that used sast as "the" bound kind now use a `boundRecord` helper
  (`access_review.completion.v1`) or that kind directly.

## D5 — A per-kind cutover keeps frozen periods frozen

The first routing cutover (`evidence_routing_cutover`, #2924) is one instant.
An audit period frozen after it but before this change read no control-free
sast record (sast was bound), and through the `control_ref` leg it read
legacy control-free rows whose `control_ref` named the control. With sast
simply added to the list, `KindsAsOf` would have put sast in that period's
set: the `scf:` rows would have appeared in it, and `routed.Within` would
have closed its `control_ref` leg for sast. Both change a frozen period
(invariant 10).

- **Migration `20261301000000`** adds `evidence_routing_kind_cutover`
  (`evidence_kind` PK, `cut_at` default `now()`), seeded with sast. Global,
  no RLS, `SELECT` to `atlas_app`, the same shape as the first cutover.
- **`routed.Cutovers`** holds the first cutover and the late kinds' own
  instants; `KindsAsOf` admits each routed kind from its own instant. A
  period frozen between the two now binds every routed kind but sast, so
  `Within` leaves sast's `control_ref` leg open, exactly the pre-change
  read (`TestRouted2973_PeriodFrozenBeforeSASTCutoverKeepsItsReads`).
- **`lateKinds` is a Go list**, and `Cutover` refuses a late kind with no
  row (or a NULL `cut_at`) rather than reading it at the first cutover.
  Rows for kinds not in `lateKinds` are ignored.
- **Kinds already routed keep the first cutover**, including
  `monitoring.host_capacity.v1` (#2935), which was added after it. Giving
  that kind its own instant now would itself change periods frozen since.
  A kind routed from its first record needs no row.
- The integration test moves sast's row a day past the first cutover and
  restores it, because one migrate run applies both within moments.

## D6 — Supersedes two stock bundles

`soc2_pi1_2_input_completeness` (query `input_validation_taint_rules_clean`)
and `soc2_pi1_3_processing_validity` (query `authorization_taint_rules_clean`)
change expression and description, so both manifest hashes change and both
bundles are superseded on the next boot; owners, evidence, exceptions and
risk links stay on the old versions. `stock_manifests_gen.go` gained the two
new hashes and dropped none. `soc2_cc7_1_vulnerability_detection` is not
changed. Upgrade note in `changelog.d/2973.changed.md`.

## Not changed

- The forbidden-keyword, semicolon and wall-clock checks (D1).
- cc7_1 detection's query: it reads severity, not rule ids or CWEs.
- No SARIF converter in `atlas-cli`; #2831's note on that stands.
